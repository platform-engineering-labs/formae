// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/platform-engineering-labs/formae"
	"github.com/platform-engineering-labs/formae/internal/cli/agent"
	"github.com/platform-engineering-labs/formae/internal/cli/apply"
	"github.com/platform-engineering-labs/formae/internal/cli/banner"
	"github.com/platform-engineering-labs/formae/internal/cli/cancel"
	"github.com/platform-engineering-labs/formae/internal/cli/clean"
	"github.com/platform-engineering-labs/formae/internal/cli/cmd"
	"github.com/platform-engineering-labs/formae/internal/cli/config"
	"github.com/platform-engineering-labs/formae/internal/cli/destroy"
	"github.com/platform-engineering-labs/formae/internal/cli/dev"
	"github.com/platform-engineering-labs/formae/internal/cli/eval"
	"github.com/platform-engineering-labs/formae/internal/cli/extract"
	"github.com/platform-engineering-labs/formae/internal/cli/inventory"
	"github.com/platform-engineering-labs/formae/internal/cli/plugin"
	"github.com/platform-engineering-labs/formae/internal/cli/profile"
	"github.com/platform-engineering-labs/formae/internal/cli/project"
	"github.com/platform-engineering-labs/formae/internal/cli/refresh"
	"github.com/platform-engineering-labs/formae/internal/cli/status"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/logo"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	"github.com/platform-engineering-labs/formae/internal/cli/update"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func longDescription() string {
	th := theme.New("formae")
	return banner.Tool + ": " + lipgloss.NewStyle().Foreground(th.Palette.Done).Render("A modern infrastructure as code tool that platform engineers deserve")
}

// setSilenceUsageRecursive sets SilenceUsage on a command and all its subcommands.
// This ensures Cobra doesn't print usage on runtime errors (only FlagError should show usage).
func setSilenceUsageRecursive(cmd *cobra.Command) {
	cmd.SilenceUsage = true
	for _, sub := range cmd.Commands() {
		setSilenceUsageRecursive(sub)
	}
}

var rootCmd = &cobra.Command{
	Use:     banner.Tool,
	Short:   banner.Tool + " CLI",
	Long:    longDescription(),
	Version: formae.Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Redirect slog output to discard to prevent it from appearing on screen
		devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		slog.SetDefault(slog.New(slog.NewTextHandler(devNull, nil)))
	},
	// SilenceUsage prevents Cobra from printing usage on every error.
	// Usage is only shown for FlagError (validation errors), not for runtime errors.
	// See Start() for the error handling logic.
	SilenceUsage: true,
	// SilenceErrors prevents Cobra from printing errors, so we can handle
	// error display ourselves with consistent formatting.
	SilenceErrors: true,
}

func init() {
	hp := rootCmd.HelpFunc()
	longestFlagName := 0
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		banner.PrintBanner()
		hp(cmd, args)
	})

	rootCmd.SetHelpCommand(&cobra.Command{
		Hidden: true,
	})

	rootCmd.CompletionOptions.DisableDefaultCmd = false

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

	// formatExamplesMultiline renders pipe-separated examples one per line
	// for the leaf-command help view. Subcommand listing keeps the single-
	// line form via formatExamples.
	cobra.AddTemplateFunc("formatExamplesMultiline", func(examples string, cmd *cobra.Command) string {
		cliName := cmd.Root().Name()
		cmdName := cmd.Name()
		replaced := strings.ReplaceAll(examples, "{{.Name}}", cliName)
		replaced = strings.ReplaceAll(replaced, "{{.Command}}", cmdName)
		// Split on either pipe or newline so both separator conventions render
		// one example per line, each aligned under the first (which the usage
		// template already indents by two spaces).
		parts := strings.FieldsFunc(replaced, func(r rune) bool { return r == '|' || r == '\n' })
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}
		return strings.Join(parts, "\n  ")
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
				th := theme.New("formae")
				s += lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(fmt.Sprintf(" [default: %q]", flag.DefValue))
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
				th := theme.New("formae")
				s += lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(" [required]")
			} else if flag.DefValue != "" {
				th := theme.New("formae")
				s += lipgloss.NewStyle().Foreground(th.Palette.TextSubtle).Render(fmt.Sprintf(" [default: %q]", flag.DefValue))
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
	rootCmd.AddCommand(cancel.CancelCmd())
	rootCmd.AddCommand(clean.CleanCmd())
	rootCmd.AddCommand(eval.EvalCmd())
	rootCmd.AddCommand(agent.AgentCmd())
	rootCmd.AddCommand(plugin.PluginCmd())
	rootCmd.AddCommand(profile.ProfileCmd())
	rootCmd.AddCommand(project.ProjectCmd())
	rootCmd.AddCommand(destroy.DestroyCmd())
	rootCmd.AddCommand(status.StatusCmd())
	rootCmd.AddCommand(extract.ExtractCmd())
	rootCmd.AddCommand(update.UpdateCmd())
	rootCmd.AddCommand(refresh.RefreshCmd())
	rootCmd.AddCommand(inventory.InventoryCmd())

	if !strings.HasSuffix(os.Args[0], "formae") {
		rootCmd.AddCommand(dev.DevCmd())
	}

	rootCmd.PersistentFlags().BoolP("help", "h", false, "Show help for "+rootCmd.Use)
	for _, cmd := range rootCmd.Commands() {
		cmd.PersistentFlags().BoolP("help", "h", false, fmt.Sprintf("Show help for %s command", cmd.Name()))
		// Ensure all subcommands have SilenceUsage set so Cobra doesn't print
		// usage on runtime errors (only FlagError should show usage)
		setSilenceUsageRecursive(cmd)
	}

	rootCmd.PersistentFlags().BoolP("version", "v", false, "Show "+rootCmd.Use+" version information")
	rootCmd.SetVersionTemplate(fmt.Sprintf("formae version: %s\ngo version: %s\n", formae.Version, runtime.Version()))
}

func Start() {
	// Seed lipgloss's global background ONCE so AdaptiveColor resolves
	// consistently without a per-render OSC-11 query. That query is flaky (it
	// mis-detects as light after the banner's graphics probe, turning body text
	// unreadable on dark backgrounds) and leaks escape codes into the shell.
	// COLORFGBG when the terminal exports it, otherwise assume dark.
	lipgloss.SetHasDarkBackground(logo.HasDarkBackground())

	err := config.Config.EnsureConfigDirectory()
	if err != nil {
		fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error).Render("Error: "+err.Error()))
		os.Exit(1)
	}

	err = config.Config.EnsureDataDirectory()
	if err != nil {
		fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error).Render("Error: "+err.Error()))
		os.Exit(1)
	}

	rootCommand, err := cmd.InitCommandWithContext(rootCmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error).Render("Error: "+err.Error()))
		os.Exit(1)
	}

	if err := rootCommand.Execute(); err != nil {
		errStyle := lipgloss.NewStyle().Foreground(theme.New("formae").Palette.Error)

		// Usage errors (bad flag, bad args, unknown command) get the offending
		// command's usage printed after the error so the user can self-correct.
		// Runtime errors just print the error.
		if isUsageError(err) {
			fmt.Fprintln(os.Stderr, errStyle.Render("Error: "+err.Error()))
			fmt.Println()
			if activeCmd, _, findErr := rootCommand.Find(os.Args[1:]); findErr == nil && activeCmd != nil {
				fmt.Println(activeCmd.UsageString())
			} else {
				fmt.Println(rootCommand.UsageString())
			}
			os.Exit(1)
		}

		fmt.Fprintln(os.Stderr, errStyle.Render("Error: "+err.Error()))
		os.Exit(1)
	}
}

// isUsageError reports whether err is a CLI-usage error — a bad flag, bad
// arguments, or an unknown command/subcommand — for which we print command
// usage, as opposed to a runtime error. It recognises both our own FlagError
// and Cobra's native flag/arg/command parse errors.
func isUsageError(err error) bool {
	var flagError *cmd.FlagError
	if errors.As(err, &flagError) {
		return true
	}
	msg := err.Error()
	for _, marker := range []string{
		"unknown command",
		"unknown flag:",
		"unknown shorthand flag:",
		"flag needs an argument:",
		"invalid argument \"",
		"required flag(s)",
		"accepts ",         // cobra ExactArgs/RangeArgs: "accepts 1 arg(s), received 2"
		"arg(s), received", // cobra MinimumNArgs/MaximumNArgs
		"requires at least",
		"requires exactly",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
