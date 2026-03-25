// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"net/url"
	"os"

	"ppm"

	ppmpkg "github.com/platform-engineering-labs/formae/pkg/ppm"
	"github.com/spf13/cobra"
)

func InstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install formae from the PEL repository",
		Long:  "Download and install formae, including plugins and resource plugins.",

		RunE: func(cmd *cobra.Command, args []string) error {
			version, _ := cmd.Flags().GetString("version")
			prefix, _ := cmd.Flags().GetString("prefix")
			localFile, _ := cmd.Flags().GetString("file")
			skipPrompt, _ := cmd.Flags().GetBool("yes")

			if version == "latest" {
				version = ""
			}

			if prefix == "" {
				prefix = os.Getenv("FORMAE_INSTALL_PREFIX")
			}
			if prefix == "" {
				prefix = "/opt/pel"
			}

			repoURI, err := url.Parse(ppm.HTTPSRepository)
			if err != nil {
				return err
			}

			repoCfg := &ppmpkg.RepoConfig{
				Uri:      repoURI,
				Username: os.Getenv("FORMAE_ARTIFACT_USERNAME"),
				Password: os.Getenv("FORMAE_ARTIFACT_PASSWORD"),
			}

			cfg := ppmpkg.InstallConfig{
				Version:    version,
				Prefix:     prefix,
				LocalFile:  localFile,
				SkipPrompt: skipPrompt,
			}

			installer, err := ppmpkg.NewInstaller(repoCfg, cfg)
			if err != nil {
				return err
			}

			return installer.Install()
		},
	}

	cmd.Flags().StringP("version", "v", "latest", "Version to install")
	cmd.Flags().StringP("prefix", "p", "", "Install prefix (default: /opt/pel, or FORMAE_INSTALL_PREFIX env)")
	cmd.Flags().StringP("file", "f", "", "Use local .tgz file instead of downloading")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
