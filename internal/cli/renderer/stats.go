// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"fmt"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

func sumMap(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func RenderStats(stats *apimodel.Stats) (string, error) {
	var tablesLine1 []struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}

	totalTargets := sumMap(stats.Targets)

	tablesLine1 = append(tablesLine1, struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}{
		Headline: "Structure",
		Rows: [][]string{
			{"Stacks", fmt.Sprintf("%d", stats.Stacks)},
			{"Targets", fmt.Sprintf("%d", totalTargets)},
		},
	})

	totalCommands := 0
	for _, v := range stats.Commands {
		totalCommands += v
	}

	tablesLine1 = append(tablesLine1, struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}{
		Headline: "Commands",
		Rows: [][]string{
			{"Total", fmt.Sprintf("%d", totalCommands)},
			{display.Green("Successes"), display.Green(fmt.Sprintf("%d", stats.States[string(forma_command.CommandStateSuccess)]))},
			{display.Red("Failures"), display.Red(fmt.Sprintf("%d", stats.States[string(forma_command.CommandStateFailed)]))},
			{display.Grey("In Progress"), display.Grey(fmt.Sprintf("%d", stats.States[string(forma_command.CommandStateInProgress)]))},
		},
	})

	totalManagedResources := sumMap(stats.ManagedResources)
	totalUnmanagedResources := sumMap(stats.UnmanagedResources)
	totalResources := totalManagedResources + totalUnmanagedResources

	managedColor := display.Green
	if totalManagedResources < totalResources {
		managedColor = display.Gold
	}
	managedString := managedColor(fmt.Sprintf("%d", totalManagedResources))
	managedTitle := managedColor("Managed")

	unmanagedColor := display.Green
	if totalUnmanagedResources > 0 {
		unmanagedColor = display.Gold
	}
	unmanagedString := unmanagedColor(fmt.Sprintf("%d", totalUnmanagedResources))
	unmanagedTitle := unmanagedColor("Unmanaged")

	p := 0
	if totalResources > 0 {
		p = int((float64(totalUnmanagedResources)/float64(totalResources))*100 + 0.5)
	}
	unmanagedPercentage := fmt.Sprintf("%d%%", p)
	unmanagedPercentageTitle := "Unmanaged %"
	if p == 0 {
		unmanagedPercentage = display.Green(unmanagedPercentage)
		unmanagedPercentageTitle = display.Green(unmanagedPercentageTitle)
	} else if p <= 75 {
		unmanagedPercentage = display.Gold(unmanagedPercentage)
		unmanagedPercentageTitle = display.Gold(unmanagedPercentageTitle)
	} else {
		unmanagedPercentage = display.Red(unmanagedPercentage)
		unmanagedPercentageTitle = display.Red(unmanagedPercentageTitle)
	}

	tablesLine1 = append(tablesLine1, struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}{
		Headline: "Resources",
		Rows: [][]string{
			{"Total", fmt.Sprintf("%d", totalResources)},
			{managedTitle, managedString},
			{unmanagedTitle, unmanagedString},
			{unmanagedPercentageTitle, unmanagedPercentage},
		},
	})

	output := renderTablesSideBySide(tablesLine1)

	var tablesLine2 []struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}

	resourceTypes := make([][]string, 0, len(stats.ResourceTypes)+1)
	resourceTypes = append(resourceTypes, []string{"Total Types", fmt.Sprintf("%d", len(stats.ResourceTypes))})
	for rt, count := range stats.ResourceTypes {
		resourceTypes = append(resourceTypes, []string{rt, fmt.Sprintf("%d", count)})
	}

	tablesLine2 = append(tablesLine2, struct {
		Headline string
		Headers  []string
		Rows     [][]string
	}{
		Headline: "Resource Types",
		Rows:     resourceTypes,
	})

	if len(stats.ResourceErrors) > 0 {
		resourceErrors := make([][]string, 0, len(stats.ResourceErrors)+1)
		resourceErrors = append(resourceErrors, []string{"Total Errors", fmt.Sprintf("%d", len(stats.ResourceErrors))})
		for err, count := range stats.ResourceErrors {
			resourceErrors = append(resourceErrors, []string{err, fmt.Sprintf("%d", count)})
		}

		tablesLine2 = append(tablesLine2, struct {
			Headline string
			Headers  []string
			Rows     [][]string
		}{
			Headline: "Resource Errors",
			Rows:     resourceErrors,
		})
	}

	output += "\n" + renderTablesSideBySide(tablesLine2)

	return output, nil
}

func renderTablesSideBySide(tables []struct {
	Headline string
	Headers  []string
	Rows     [][]string
}) string {
	var tableOutputs []string

	// Render each table to a string
	for _, table := range tables {
		var buf strings.Builder

		// Add headline
		fmt.Fprintf(&buf, "%s\n", display.LightBlue(table.Headline))

		// Create tablewriter table
		tw := tablewriter.NewTable(&buf,
			tablewriter.WithMaxWidth(100),
			tablewriter.WithRowAutoWrap(tw.WrapBreak),
			tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
				Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On, ShowHeader: tw.On}},
			})))

		for _, row := range table.Rows {
			_ = tw.Append(row)
		}

		_ = tw.Render()

		// Collect the rendered table
		tableOutputs = append(tableOutputs, buf.String())
	}

	// Combine tables side by side
	return combineTablesSideBySide(tableOutputs)
}

func combineTablesSideBySide(tableOutputs []string) string {
	var buf strings.Builder

	// Create a tablewriter table
	table := tablewriter.NewTable(&buf,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows:    tw.Off,
					BetweenColumns: tw.Off,
					ShowHeader:     tw.Off,
				},
				Lines: tw.Lines{
					ShowTop:    tw.Off,
					ShowBottom: tw.Off,
				},
			},
		})))

	// Split each table into rows and columns
	var rows [][]string
	for _, tableOutput := range tableOutputs {
		lines := strings.Split(strings.TrimRight(tableOutput, "\n"), "\n")
		rows = append(rows, lines)
	}

	// Calculate the number of columns per row
	numColumns := 3
	numRows := (len(rows) + numColumns - 1) / numColumns

	// Combine rows into a grid
	for i := range numRows {
		var combinedRow []string
		for j := range numColumns {
			index := i*numColumns + j
			if index < len(rows) {
				combinedRow = append(combinedRow, strings.Join(rows[index], "\n"))
			} else {
				combinedRow = append(combinedRow, "")
			}
		}
		_ = table.Append(combinedRow)
	}

	// Render the table
	_ = table.Render()

	// Post-process the output to remove left and right borders
	output := buf.String()
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "│") && strings.HasSuffix(l, "│") {
			lines[i] = strings.Trim(l, "│") // Remove the left and right borders
		}
	}

	return strings.Join(lines, "\n")
}
