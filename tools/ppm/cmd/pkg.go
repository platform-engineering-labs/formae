// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/masterminds/semver"
	"github.com/platform-engineering-labs/formae/pkg/ppm"
	"github.com/spf13/cobra"
)

func PkgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pkg",
		Short: "Work with binary packages",
	}

	cmd.AddCommand(PkgBuildCmd())
	cmd.AddCommand(PkgVerifyCmd())

	return cmd
}

func PkgBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a package",

		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			version, _ := cmd.Flags().GetString("version")
			oz, _ := cmd.Flags().GetString("os")
			arch, _ := cmd.Flags().GetString("arch")
			out, _ := cmd.Flags().GetString("out")

			smv, err := semver.NewVersion(version)
			if err != nil {
				return fmt.Errorf("invalid semver version: %s", version)
			}

			src := cmd.Flags().Arg(0)
			if src == "" {
				return fmt.Errorf("must specify a source directory")
			}

			platform := ppm.CurrentOsArch()
			if oz != "" {
				platform.OS = oz
			}
			if arch != "" {
				platform.Arch = arch
			}

			pkgName, err := ppm.Package.Build(
				name,
				smv,
				platform,
				src,
				out,
			)
			if err != nil {
				return err
			}

			fmt.Printf("Built package name: %s\n", pkgName)

			return nil
		},
	}

	cmd.Flags().String("name", "", "Package name")
	cmd.Flags().String("version", "", "Package version")
	cmd.Flags().String("os", "", "target os")
	cmd.Flags().String("arch", "", "target arch")
	cmd.Flags().String("out", "./dist/packages", "output directory")

	return cmd
}

func PkgVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "verify a package's checksum",

		RunE: func(cmd *cobra.Command, args []string) error {
			pkgFilePath := cmd.Flags().Arg(0)
			if pkgFilePath == "" {
				return fmt.Errorf("must specify a package file")
			}

			checkSum := cmd.Flags().Arg(1)
			if pkgFilePath == "" {
				return fmt.Errorf("must specify a checksum (sha256)")
			}

			pkgFilePath, err := filepath.Abs(pkgFilePath)
			if err != nil {
				return err
			}

			pkg, err := os.Open(pkgFilePath)
			if err != nil {
				return err
			}
			defer pkg.Close()

			err = ppm.Package.Verify(pkg, checkSum)
			if err != nil {
				return fmt.Errorf("verify failed: %s", err)
			}

			fmt.Printf("Verified package\nchecksum: %s\nfile: %s\n", checkSum, pkgFilePath)

			return nil
		},
	}

	return cmd
}
