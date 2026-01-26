// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package upgrade

import (
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/masterminds/semver"
	"github.com/spf13/cobra"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/agent"
	clicmd "github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
)

func UpgradeCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Install or list available formae binary updates",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} {{.Command}}",
		},
		SilenceErrors: true,
		PreRun: func(cmd *cobra.Command, args []string) {
			logging.SetupClientLogging(fmt.Sprintf("%s/log/client.log", config.Config.DataDirectory()))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			version, _ := cmd.Flags().GetString("version")
			configFile, _ := cmd.Flags().GetString("config")
			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			if !ppm.Sys.IsPrivilegedUser() {
				fmt.Println("this command requires a privileged user: authentication may be required")

				upgradeArgs := []string{cmd.Name()}
				if version != "" {
					upgradeArgs = append(upgradeArgs, "--version", version)
				}

				err := ppm.Sys.InvokeSelfWithSudo(upgradeArgs...)
				if err != nil {
					return fmt.Errorf("could not escalate to privileged user: %w", err)
				}
			}

			repoUri, err := url.Parse(app.Config.Artifacts.URL)
			if err != nil {
				return fmt.Errorf("could not parse repository URL: %s", app.Config.Artifacts.URL)
			}

			manager, err := ppm.NewManager(
				&ppm.Config{
					Repo: &ppm.RepoConfig{
						Uri:      repoUri,
						Username: app.Config.Artifacts.Username,
						Password: app.Config.Artifacts.Password,
					},
				}, formae.DefaultInstallPrefix)
			if err != nil {
				return err
			}

			var candidate *ppm.PkgEntry

			if version == "" {
				candidate, err = manager.HasUpdate("formae", semver.MustParse(formae.Version))
				if err != nil {
					return err
				}

				if candidate == nil {
					fmt.Println("no update available")
					return nil
				}
			} else {
				parsedVersion, err := semver.NewVersion(version)
				if err != nil {
					return fmt.Errorf("error parsing version %q: ", version)
				}

				candidate, err = manager.HasEntry("formae", parsedVersion)
				if err != nil {
					return fmt.Errorf("no candidate for version %q: ", parsedVersion)
				}
			}

			fmt.Println("stopping formae agent...")
			ag := agent.Agent{}
			err = ag.Stop()
			if err != nil {
				if !strings.Contains(err.Error(), "agent is not running") {
					return err
				}
			}

			fmt.Printf("installing formae version %s\n", candidate.Version.String())
			err = manager.Install(candidate)
			if err != nil {
				return err
			}

			// Install plugin executables to user directory
			err = installPluginExecutables(formae.DefaultInstallPrefix, candidate.Version.String())
			if err != nil {
				return fmt.Errorf("failed to install plugin executables: %w", err)
			}

			// Install resource plugins (full directories with manifest and schema)
			err = installResourcePlugins(formae.DefaultInstallPrefix)
			if err != nil {
				return fmt.Errorf("failed to install resource plugins: %w", err)
			}

			fmt.Println("done.")

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.AddCommand(UpgradeListCmd())

	command.Flags().String("config", "", "Path to config file")
	command.Flags().String("version", "", "Version to install")

	return command
}

func UpgradeListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List available formae upgrades",
		Annotations: map[string]string{
			"type":     "Manage",
			"examples": "{{.Name}} upgrade list",
		},
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("config")
			app, err := clicmd.AppFromContext(cmd.Context(), configFile, "", cmd)
			if err != nil {
				return err
			}

			repoUri, err := url.Parse(app.Config.Artifacts.URL)
			if err != nil {
				return fmt.Errorf("could not parse repository URL: %s", app.Config.Artifacts.URL)
			}

			manager, err := ppm.NewManager(
				&ppm.Config{
					Repo: &ppm.RepoConfig{
						Uri:      repoUri,
						Username: app.Config.Artifacts.Username,
						Password: app.Config.Artifacts.Password,
					},
				}, formae.DefaultInstallPrefix)
			if err != nil {
				return err
			}

			updates, err := manager.AvailableVersions("formae", true)
			if err != nil {
				return err
			}

			fmt.Print("Available formae versions:\n\n")
			for _, entry := range updates {
				fmt.Printf(" %12v  %s\n", entry.Version, entry.Sha256)
			}

			return nil
		},
	}

	command.SetUsageTemplate(clicmd.SimpleCmdUsageTemplate)
	command.Flags().String("config", "", "Path to config file")

	return command
}

// getOriginalUserHome returns the home directory of the user who invoked sudo,
// or the current user's home directory if not running under sudo.
func getOriginalUserHome() (string, error) {
	// Check if running under sudo - SUDO_USER contains the original username
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		u, err := user.Lookup(sudoUser)
		if err != nil {
			return "", fmt.Errorf("failed to lookup sudo user %s: %w", sudoUser, err)
		}
		return u.HomeDir, nil
	}

	// Not running under sudo, use current user
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return home, nil
}

// isExecutable checks if the file is an executable binary.
func isExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	// Check for executable based on OS
	switch runtime.GOOS {
	case "darwin":
		_, err := macho.NewFile(f)
		return err == nil
	default: // linux and others
		_, err := elf.NewFile(f)
		return err == nil
	}
}

// installPluginExecutables copies plugin executables from the system install
// directory to the user's plugin directory, then removes them from the system
// directory. This matches what setup.sh does for fresh installs.
func installPluginExecutables(installPrefix, version string) error {
	homeDir, err := getOriginalUserHome()
	if err != nil {
		return err
	}

	pluginsDir := filepath.Join(installPrefix, "formae", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No plugins directory, nothing to do
		}
		return fmt.Errorf("failed to read plugins directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		srcPath := filepath.Join(pluginsDir, entry.Name())

		// Skip .so files - they are shared libraries, not standalone executables
		if strings.HasSuffix(entry.Name(), ".so") {
			continue
		}

		if !isExecutable(srcPath) {
			continue
		}

		// Create destination directory: ~/.pel/formae/plugins/<name>/v<version>/
		name := entry.Name()
		destDir := filepath.Join(homeDir, ".pel", "formae", "plugins", name, "v"+version)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create plugin directory %s: %w", destDir, err)
		}

		// Copy the executable
		destPath := filepath.Join(destDir, name)
		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy plugin %s: %w", name, err)
		}

		// Set ownership to the original user if running as root
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			if u, err := user.Lookup(sudoUser); err == nil {
				var uid, gid int
				_, _ = fmt.Sscanf(u.Uid, "%d", &uid)
				_, _ = fmt.Sscanf(u.Gid, "%d", &gid)
				// Chown the plugin directory and file (best-effort, ignore errors)
				_ = os.Chown(filepath.Join(homeDir, ".pel"), uid, gid)
				_ = os.Chown(filepath.Join(homeDir, ".pel", "formae"), uid, gid)
				_ = os.Chown(filepath.Join(homeDir, ".pel", "formae", "plugins"), uid, gid)
				_ = os.Chown(filepath.Join(homeDir, ".pel", "formae", "plugins", name), uid, gid)
				_ = os.Chown(destDir, uid, gid)
				_ = os.Chown(destPath, uid, gid)
			}
		}

		// Remove from system directory
		if err := os.Remove(srcPath); err != nil {
			return fmt.Errorf("failed to remove plugin from system directory: %w", err)
		}

		fmt.Printf("installed plugin: %s\n", name)
	}

	return nil
}

// copyFile copies a file from src to dst, preserving the executable permission.
func copyFile(src, dst string) (err error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// installResourcePlugins copies resource plugins from the system install
// directory to the user's plugin directory. Resource plugins include the
// binary, manifest, and schema files.
func installResourcePlugins(installPrefix string) error {
	homeDir, err := getOriginalUserHome()
	if err != nil {
		return err
	}

	resourcePluginsDir := filepath.Join(installPrefix, "formae", "resource-plugins")
	namespaces, err := os.ReadDir(resourcePluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No resource plugins directory
		}
		return fmt.Errorf("failed to read resource-plugins directory: %w", err)
	}

	for _, nsEntry := range namespaces {
		if !nsEntry.IsDir() {
			continue
		}
		namespace := strings.ToLower(nsEntry.Name())
		nsPath := filepath.Join(resourcePluginsDir, nsEntry.Name())

		versions, err := os.ReadDir(nsPath)
		if err != nil {
			continue
		}

		for _, vEntry := range versions {
			if !vEntry.IsDir() {
				continue
			}
			version := vEntry.Name()
			srcDir := filepath.Join(nsPath, version)
			destDir := filepath.Join(homeDir, ".pel", "formae", "plugins", namespace, version)

			// Create destination and copy entire directory
			if err := copyDir(srcDir, destDir); err != nil {
				return fmt.Errorf("failed to copy plugin %s/%s: %w", namespace, version, err)
			}

			// Chown if running as sudo
			chownRecursive(destDir, homeDir)

			fmt.Printf("installed resource plugin: %s %s\n", namespace, version)
		}
	}

	// Remove from system directory
	if err := os.RemoveAll(resourcePluginsDir); err != nil {
		return fmt.Errorf("failed to remove resource-plugins from system directory: %w", err)
	}

	return nil
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// chownRecursive sets ownership of a directory tree to the sudo user if running as root.
func chownRecursive(path, homeDir string) {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		return
	}

	u, err := user.Lookup(sudoUser)
	if err != nil {
		return
	}

	var uid, gid int
	_, _ = fmt.Sscanf(u.Uid, "%d", &uid)
	_, _ = fmt.Sscanf(u.Gid, "%d", &gid)

	// Chown parent directories
	_ = os.Chown(filepath.Join(homeDir, ".pel"), uid, gid)
	_ = os.Chown(filepath.Join(homeDir, ".pel", "formae"), uid, gid)
	_ = os.Chown(filepath.Join(homeDir, ".pel", "formae", "plugins"), uid, gid)

	// Walk and chown the plugin directory
	_ = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chown(p, uid, gid)
		return nil
	})
}
