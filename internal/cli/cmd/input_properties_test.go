// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cmd

import (
	"context"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
	"testing"
)

func TestExplicitPropertiesRetainsFalseAndZeroWithoutSupplyingDefaults(t *testing.T) {
	command := &cobra.Command{}
	command.Flags().Int("replicas", 2, "")
	command.Flags().Bool("enabled", true, "")
	command.Flags().String("suffix", "default", "")
	command.SetContext(context.WithValue(context.Background(), "forma.properties", map[string]pkgmodel.Prop{
		"replicas": {Flag: "replicas", Type: "Int"},
		"enabled":  {Flag: "enabled", Type: "Boolean"},
		"suffix":   {Flag: "suffix", Type: "String"},
	}))
	if err := command.Flags().Set("replicas", "0"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("enabled", "false"); err != nil {
		t.Fatal(err)
	}
	got := ExplicitPropertiesFromCmd(command)
	if len(got) != 2 || got["replicas"] != "0" || got["enabled"] != "false" {
		t.Fatalf("explicit inputs: %v", got)
	}
}
