// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"
	"net/url"

	"github.com/platform-engineering-labs/formae/pkg/ppm"

	"github.com/spf13/cobra"

	_ "ppm/pkg/repo"
)

func RepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Work with repositories",
	}

	cmd.AddCommand(RepoContentsCmd())
	cmd.AddCommand(RepoInitCmd())
	cmd.AddCommand(RepoPublishCmd())
	cmd.AddCommand(RepoRebuildCmd())

	return cmd
}

func RepoContentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contents",
		Short: "List repository contents",

		RunE: func(cmd *cobra.Command, args []string) error {
			uri, _ := cmd.Flags().GetString("uri")
			all, _ := cmd.Flags().GetBool("all")

			uriParsed, err := url.Parse(uri)
			if err != nil {
				return err
			}

			repo, err := ppm.Repo.GetReader(&ppm.RepoConfig{Uri: uriParsed})
			if err != nil {
				return err
			}

			osArch := ppm.CurrentOsArch()
			if all {
				osArch = nil
			}

			err = repo.Fetch()
			if err != nil {
				return err
			}

			data := repo.Data().List("", osArch)
			names := ppm.PkgEntries(data).Names()
			platforms := ppm.PkgEntries(data).Platforms()

			for _, name := range names {
				fmt.Println(name)
				for _, platform := range platforms {
					fmt.Println("  " + platform.String())
					for _, entry := range data {
						if entry.Name == name && entry.OsArch.String() == platform.String() {
							fmt.Printf("    %12v  %s\n", entry.Version, entry.Sha256)
						}
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().Bool("all", false, "List all contents")

	return cmd
}

func RepoInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Remove repository metadata",

		RunE: func(cmd *cobra.Command, args []string) error {
			uri, _ := cmd.Flags().GetString("uri")

			uriParsed, err := url.Parse(uri)
			if err != nil {
				return err
			}

			repo, err := ppm.Repo.GetWriter(&ppm.RepoConfig{Uri: uriParsed})
			if err != nil {
				return err
			}

			err = repo.Init()
			if err != nil {
				return err
			}

			fmt.Println("Repository initialized")

			return nil
		},
	}

	return cmd
}

func RepoPublishCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish packages to a repository",

		RunE: func(cmd *cobra.Command, args []string) error {
			uri, _ := cmd.Flags().GetString("uri")
			prune, _ := cmd.Flags().GetInt("prune")

			if len(cmd.Flags().Args()) == 0 {
				return fmt.Errorf("argument: package files are required")
			}

			uriParsed, err := url.Parse(uri)
			if err != nil {
				return err
			}

			repo, err := ppm.Repo.GetWriter(&ppm.RepoConfig{Uri: uriParsed})
			if err != nil {
				return err
			}

			err = repo.Publish(prune, cmd.Flags().Args()...)
			if err != nil {
				return err
			}

			fmt.Printf("Published (%d) packages to: %s\n", len(cmd.Flags().Args()), uri)

			return nil
		},
	}

	cmd.Flags().Int("prune", 10, "Limit number of packages of each name in repo")

	return cmd
}

func RepoRebuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild repository metadata from the packages hosted at uri",

		RunE: func(cmd *cobra.Command, args []string) error {
			uri, _ := cmd.Flags().GetString("uri")

			uriParsed, err := url.Parse(uri)
			if err != nil {
				return err
			}

			repo, err := ppm.Repo.GetWriter(&ppm.RepoConfig{Uri: uriParsed})
			if err != nil {
				return err
			}

			err = repo.Rebuild()
			if err != nil {
				return err
			}

			fmt.Printf("Rebuilt repo metadata for uri: %s", uri)

			return nil
		},
	}

	cmd.Flags().Int("prune", 10, "Limit number of packages of each name in repo")

	return cmd
}
