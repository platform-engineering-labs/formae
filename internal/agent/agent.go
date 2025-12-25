// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package agent

import (
	"context"
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/api"
	"github.com/platform-engineering-labs/formae/internal/imconc"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	pidFile = "/tmp/formae.pid"
)

type Agent struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	cfg    *pkgmodel.Config
	id     string
}

func New(cfg *pkgmodel.Config, id string) *Agent {
	ctx, cancel := context.WithCancel(context.Background())
	return &Agent{
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		cfg:    cfg,
		id:     id,
	}
}

func (a *Agent) Start() error {
	// Check if already running
	if _, err := os.Stat(pidFile); err == nil {
		return fmt.Errorf("agent appears to be already running (PID file exists)")
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Write PID
	pid := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", pid)), 0600); err != nil {
		return fmt.Errorf("failed to write pid file: %w", err)
	}

	imwg := imconc.NewConcGroup()
	go func() {
		// Important: Setup all OTel providers before db connections are created -
		// otelsql captures the tracer provider at driver registration time.
		otelLogHandler, metricsHandler, shutdownOTel := api.SetupOTelProviders(&a.cfg.Agent.OTel)
		defer func() {
			shutdownOTel()
			a.cleanup()
			close(a.done)
		}()

		// Setup logging with OTel handler
		logging.SetupBackendLogging(&a.cfg.Agent.Logging, otelLogHandler)

		// Migrate plugin executables from system directory to user directory
		// This is a one-time migration for users who upgraded from older versions
		// where the upgrade command didn't copy plugins to the user directory.
		if err := migratePluginExecutables(a.cfg.Plugins.PluginDir); err != nil {
			slog.Warn("Failed to migrate plugin executables", "error", err)
			// Non-fatal - continue anyway, plugins might already be in place
		}

		pluginManager := plugin.NewManager(util.ExpandHomePath(a.cfg.Plugins.PluginDir))
		pluginManager.Load()

		slog.Info("Starting agent", "id", a.id)

		ms, err := metastructure.NewMetastructure(a.ctx, a.cfg, pluginManager, a.id)
		if err != nil {
			slog.Error("Failed to create ms", "error", err)
			return
		}
		imwg.Add(ms)

		if err := ms.Start(); err != nil {
			slog.Error("Failed to start metastructure", "error", err)
			return
		}

		// Start Ergo actor metrics collection (only if OTel is enabled)
		if a.cfg.Agent.OTel.Enabled {
			if err := api.StartErgoMetrics(ms.Node); err != nil {
				slog.Error("Failed to start Ergo metrics", "error", err)
				// Non-fatal - continue without Ergo metrics
			}

			// Start Formae stats metrics collection
			if err := api.StartFormaeMetrics(ms); err != nil {
				slog.Error("Failed to start Formae metrics", "error", err)
				// Non-fatal - continue without Formae metrics
			}
		}

		slog.Info("Agent started")

		apiServer := api.NewServer(a.ctx, ms, pluginManager, &a.cfg.Agent.Server, &a.cfg.Plugins, metricsHandler)
		imwg.Add(apiServer)
		imwg.Go(func() {
			apiServer.Start()
		})

		// Handle signals and shutdown
		go func() {
			select {
			case sig := <-sigChan:
				slog.Info("Received signal", "signal", sig)
				a.cancel()
			case <-a.ctx.Done():
				slog.Info("Context canceled")
			}
		}()
		<-a.ctx.Done()

		// Ensure app doesn't hang indefinitely on shutdown
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		slog.Info("Stopping metastructure")
		ms.Stop(false)

		done := make(chan struct{})
		go func() {
			imwg.Wait()
			close(done)
		}()

		// Wait for either completion or timeout
		select {
		case <-done:
			slog.Info("Components stopped gracefully")
		case <-shutdownCtx.Done():
			slog.Warn("Shutdown timed out, forcing stop")
			imwg.Stop(true)
		}
	}()

	return nil
}

func (a *Agent) Stop() error {
	// Try to read PID file
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent is not running (no PID file found)")
		}
		return fmt.Errorf("failed to read pid file: %w", err)
	}

	// Parse PID
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		return fmt.Errorf("invalid pid file content: %w", err)
	}

	// Check if we are the process
	if pid == os.Getpid() {
		if a.cancel != nil {
			a.cancel()
		}
		<-a.done
		return nil
	}

	// We are not the process, try to stop it
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// First, try SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if err.Error() == "os: process already finished" {
			a.cleanup()
			return fmt.Errorf("agent is not running (stale PID file)")
		}
		return fmt.Errorf("failed to send signal to process: %w", err)
	}

	if waitForPidFileRemoval(10 * time.Second) {
		return nil
	}

	// If SIGTERM didn't work, try SIGKILL as a last resort
	slog.Warn("SIGTERM timeout, attempting SIGKILL")
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to SIGKILL process: %w", err)
	}

	// Clean up the PID file since SIGKILL won't trigger normal cleanup
	a.cleanup()
	return nil
}

func (a *Agent) Wait() {
	<-a.done
}

func (a *Agent) cleanup() {
	slog.Info("Cleaning up")
	// Remove PID file
	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to remove pid file", "error", err)
	}
}

func waitForPidFileRemoval(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); os.IsNotExist(err) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// migratePluginExecutables checks for plugin executables in the system install
// directory and copies them to the user's plugin directory if not already present.
// This handles the case where users upgraded from older versions that didn't
// properly install plugins to the user directory.
func migratePluginExecutables(userPluginDir string) error {
	systemPluginsDir := filepath.Join(formae.DefaultInstallPath, "plugins")

	entries, err := os.ReadDir(systemPluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No system plugins directory, nothing to migrate
		}
		return fmt.Errorf("failed to read system plugins directory: %w", err)
	}

	userPluginDir = util.ExpandHomePath(userPluginDir)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		srcPath := filepath.Join(systemPluginsDir, entry.Name())

		// Skip .so files - they are shared libraries, not standalone executables
		if strings.HasSuffix(entry.Name(), ".so") {
			continue
		}

		if !isExecutable(srcPath) {
			continue
		}

		name := entry.Name()
		destDir := filepath.Join(userPluginDir, name, "v"+formae.Version)

		// Check if already migrated
		destPath := filepath.Join(destDir, name)
		if _, err := os.Stat(destPath); err == nil {
			continue // Already exists, skip
		}

		// Create destination directory
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return fmt.Errorf("failed to create plugin directory %s: %w", destDir, err)
		}

		// Copy the executable
		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy plugin %s: %w", name, err)
		}

		slog.Info("Migrated plugin executable", "name", name, "dest", destPath)
	}

	return nil
}

// isExecutable checks if the file is an executable binary.
func isExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	switch runtime.GOOS {
	case "darwin":
		_, err := macho.NewFile(f)
		return err == nil
	default:
		_, err := elf.NewFile(f)
		return err == nil
	}
}

// copyFile copies a file from src to dst, preserving the executable permission.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
