// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cmd_test

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/cli/apply"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/destroy"
	"github.com/platform-engineering-labs/formae/internal/cli/eval"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestIsDynamicCommandSelectsFormaInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"resolution JSON before forma", []string{"apply", "--resolution", "controls.json", "forma.pkl"}, "forma.pkl"},
		{"config PKL before forma", []string{"apply", "--config", "client-config.pkl", "forma.pkl"}, "forma.pkl"},
		{"profile before command", []string{"--profile", "ci", "apply", "forma.pkl"}, "forma.pkl"},
		{"resolution JSON after forma", []string{"apply", "forma.pkl", "--resolution", "controls.json"}, "forma.pkl"},
		{"profile after command", []string{"apply", "--profile", "ci", "forma.pkl"}, "forma.pkl"},
		{"scalar dynamic property before forma", []string{"apply", "--team", "payments", "forma.pkl"}, "forma.pkl"},
		{"bare boolean dynamic property", []string{"apply", "--dry", "forma.pkl"}, "forma.pkl"},
		{"unknown file-valued property retains scan", []string{"apply", "--values-file", "inputs.json", "forma.pkl"}, "inputs.json"},
		{"unknown equals property retains scan", []string{"apply", "--values-file=inputs.json", "forma.pkl"}, "--values-file=inputs.json"},
		{"resolution equals", []string{"apply", "--resolution=controls.json", "forma.pkl"}, "forma.pkl"},
		{"config equals", []string{"apply", "--config=client-config.pkl", "forma.pkl"}, "forma.pkl"},
		{"shorthand separate", []string{"apply", "-m", "message.pkl", "forma.pkl"}, "forma.pkl"},
		{"shorthand equals", []string{"apply", "-m=message.pkl", "forma.pkl"}, "forma.pkl"},
		{"shorthand attached", []string{"apply", "-mmessage.pkl", "forma.pkl"}, "forma.pkl"},
		{"builtin boolean", []string{"apply", "--force", "forma.pkl"}, "forma.pkl"},
		{"builtin boolean equals", []string{"apply", "--yes=false", "forma.pkl"}, "forma.pkl"},
		{"end of flags", []string{"apply", "--", "forma.pkl"}, "forma.pkl"},
		{"known flag after end of flags is positional", []string{"apply", "--", "--config", "forma.pkl"}, "forma.pkl"},
		{"destroy config", []string{"destroy", "--config", "client-config.pkl", "forma.pkl"}, "forma.pkl"},
		{"eval config", []string{"eval", "--config", "client-config.pkl", "forma.pkl"}, "forma.pkl"},
		{"non-property command", []string{"status", "foo.pkl"}, ""},
		{"unknown command", []string{"bogus.pkl"}, ""},
		{"no command", nil, ""},
		{"no forma", []string{"apply", "--yes"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "formae"}
			root.AddCommand(apply.ApplyCmd(), destroy.DestroyCmd(), eval.EvalCmd(), status.StatusCmd())
			dynamic, path := cmd.IsDynamicCommand(root, tt.args)
			require.Equal(t, tt.want != "", dynamic)
			require.Equal(t, tt.want, path)
			for _, command := range root.Commands() {
				command.Flags().VisitAll(func(flag *pflag.Flag) {
					require.False(t, flag.Changed, "selection must not parse or set --%s", flag.Name)
				})
			}
		})
	}
}

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
	got := cmd.ExplicitPropertiesFromCmd(command)
	if len(got) != 2 || got["replicas"] != "0" || got["enabled"] != "false" {
		t.Fatalf("explicit inputs: %v", got)
	}
}
