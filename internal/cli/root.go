// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/apply"
	"github.com/platform-engineering-labs/formae/internal/cli/clean"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/destroy"
	"github.com/platform-engineering-labs/formae/internal/cli/dev"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/cli/eval"
	"github.com/platform-engineering-labs/formae/internal/cli/extract"
	"github.com/platform-engineering-labs/formae/internal/cli/inventory"
	"github.com/platform-engineering-labs/formae/internal/cli/plugin"
	"github.com/platform-engineering-labs/formae/internal/cli/project"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/cli/upgrade"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func longDescription() string {
	return display.Tool + ": " + display.Green("A modern infrastructure as code tool that platform engineers deserve")
}

var rootCmd = &cobra.Command{
	Use:     display.Tool,
	Short:   display.Tool + " CLI",
	Long:    longDescription(),
	Version: formae.Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Redirect slog output to discard to prevent it from appearing on screen
		devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		slog.SetDefault(slog.New(slog.NewTextHandler(devNull, nil)))
	},
}

func init() {
	hp := rootCmd.HelpFunc()
	longestFlagName := 0
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		display.PrintBanner()
		hp(cmd, args)
	})

	rootCmd.SetHelpCommand(&cobra.Command{
		Hidden: true,
	})

	rootCmd.CompletionOptions.DisableDefaultCmd = true

	cobra.AddTemplateFunc("typeMap", func(cmds []*cobra.Command) map[string][]*cobra.Command {
		m := make(map[string][]*cobra.Command)
		for _, c := range cmds {
			if c.IsAvailableCommand() {
				t := c.Annotations["type"]
				if t == "" {
					t = "Tooling"
				}

				m[t] = append(m[t], c)
			}
		}
		return m
	})

	cobra.AddTemplateFunc("formatExamples", func(examples string, cmd *cobra.Command) string {
		cliName := cmd.Root().Name()
		cmdName := cmd.Name()
		replaced := strings.ReplaceAll(examples, "{{.Name}}", cliName)
		return strings.ReplaceAll(replaced, "{{.Command}}", cmdName)
	})

	cobra.AddTemplateFunc("formatDoc", func(doc string, cmd *cobra.Command) string {
		lines := strings.Split(doc, "\n")
		for i, line := range lines {
			lines[i] = "                     " + line
		}

		return strings.Join(lines, "\n")
	})

	cobra.AddTemplateFunc("optionsUsage", func(f *pflag.FlagSet) []string {
		var usage []string

		f.VisitAll(func(flag *pflag.Flag) {
			if flag.Annotations != nil {
				if _, ok := flag.Annotations["forma.property"]; ok {
					return
				}
			}

			length := len(flag.Name)
			if flag.Shorthand != "" {
				length += 6
			}

			if length > longestFlagName {
				longestFlagName = length
			}
		})

		longestFlagName += 10

		f.VisitAll(func(flag *pflag.Flag) {
			if flag.Annotations != nil {
				if _, ok := flag.Annotations["forma.property"]; ok {
					return
				}
			}

			s := fmt.Sprintf("      --%s ", flag.Name)
			if flag.Shorthand != "" {
				s = fmt.Sprintf("  -%s, --%s ", flag.Shorthand, flag.Name)
			}

			s = fmt.Sprintf("%-*s%s", longestFlagName, s, flag.Usage)
			if flag.DefValue != "" &&
				flag.DefValue != "[]" &&
				flag.Name != "help" &&
				flag.Name != "version" {
				s += display.Grey(fmt.Sprintf(" [default: %q]", flag.DefValue))
			}

			usage = append(usage, s)
		})
		return usage
	})

	cobra.AddTemplateFunc("propertyUsage", func(f *pflag.FlagSet) []string {
		var usage []string
		f.VisitAll(func(flag *pflag.Flag) {
			if flag.Annotations == nil {
				return
			}

			if _, ok := flag.Annotations["forma.property"]; !ok {
				return
			}

			s := fmt.Sprintf("      --%s ", flag.Name)
			if flag.Shorthand != "" {
				s = fmt.Sprintf("  -%s, --%s ", flag.Shorthand, flag.Name)
			}
			s = fmt.Sprintf("%-*s%s", longestFlagName, s, flag.Usage)

			if _, ok := flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]; ok {
				s += display.Gold(" [required]")
			} else if flag.DefValue != "" {
				s += display.Grey(fmt.Sprintf(" [default: %q]", flag.DefValue))
			}

			usage = append(usage, s)
		})
		return usage
	})

	cobra.AddTemplateFunc("hasPropertyFlags", func(f *pflag.FlagSet) bool {
		result := false
		f.VisitAll(func(flag *pflag.Flag) {
			if flag.Annotations != nil {
				if _, ok := flag.Annotations["forma.property"]; ok {
					result = true
				}
			}
		})
		return result
	})

	rootCmd.SetUsageTemplate(cmd.RootCmdUsageTemplate)

	rootCmd.AddCommand(apply.ApplyCmd())
	rootCmd.AddCommand(clean.CleanCmd())
	rootCmd.AddCommand(eval.EvalCmd())
	rootCmd.AddCommand(agent.AgentCmd())
	rootCmd.AddCommand(plugin.PluginCmd())
	rootCmd.AddCommand(project.ProjectCmd())
	rootCmd.AddCommand(destroy.DestroyCmd())
	rootCmd.AddCommand(status.StatusCmd())
	rootCmd.AddCommand(extract.ExtractCmd())
	rootCmd.AddCommand(upgrade.UpgradeCmd())
	rootCmd.AddCommand(inventory.InventoryCmd())

	if !strings.HasSuffix(os.Args[0], "formae") {
		rootCmd.AddCommand(dev.DevCmd())
	}

	rootCmd.PersistentFlags().BoolP("help", "h", false, "Show help for "+rootCmd.Use)
	for _, cmd := range rootCmd.Commands() {
		cmd.PersistentFlags().BoolP("help", "h", false, fmt.Sprintf("Show help for %s command", cmd.Name()))
	}

	rootCmd.PersistentFlags().BoolP("version", "v", false, "Show "+rootCmd.Use+" version information")
	rootCmd.SetVersionTemplate(fmt.Sprintf("formae version: %s\ngo version: %s\n", formae.Version, runtime.Version()))
}

func Start() {
	err := config.Config.EnsureConfigDirectory()
	if err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}

	err = config.Config.EnsureDataDirectory()
	if err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}

	cmd, err := cmd.InitCommandWithContext(rootCmd)
	if err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		fmt.Println(display.Red("Error: " + err.Error()))
		os.Exit(1)
	}
}
