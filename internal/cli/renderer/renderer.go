// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"fmt"
	"strings"
	"time"

	"github.com/ddddddO/gtree"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/constants"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func RenderSimulation(s *apimodel.Simulation) (string, error) {
	buf := strings.Builder{}
	renderHeader := func(cmd apimodel.Command) string { return "Command will" }

	command, err := renderCommand(s.Command, renderHeader, formatSimulatedResourceUpdate)
	if err != nil {
		return "", err
	}
	root := gtree.NewRoot(command)

	if err := gtree.OutputFromRoot(&buf, root); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func RenderStatusSummary(status *apimodel.ListCommandStatusResponse) (string, error) {
	var buf strings.Builder
	table := tablewriter.NewTable(&buf,
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On}},
		})))
	anyFailed := false
	allSuccess := true
	for _, c := range status.Commands {
		if c.State == "Failed" {
			anyFailed = true
			allSuccess = false
			break
		} else if c.State == "InProgress" {
			allSuccess = false
			break
		}
	}

	h := display.Grey("Status")
	if anyFailed {
		h = display.Red("Status")
	} else if allSuccess {
		h = display.Green("Status")
	}

	table.Header(display.LightBlue("ID"),
		"Command",
		h,
		"Change",
		display.Grey("Wait"),
		"Progress",
		display.Green("Success"),
		display.Gold("Retry"),
		display.Red("Fail"),
		display.LightBlue("Started At"),
		display.LightBlue("Time"))

	data := make([][]any, len(status.Commands))
	for i, command := range status.Commands {
		data[i] = make([]any, 11)
		data[i][0] = display.LightBlue(string(command.CommandID))
		data[i][1] = command.Command
		status := display.Grey(string(command.State))
		switch command.State {
		case "Success":
			status = display.Green(string(command.State))
		case "Failed":
			status = display.Red(string(command.State))
		}

		data[i][2] = status

		statusData := make([]int, 6)
		for _, rc := range command.ResourceUpdates {
			if rc.Operation != apimodel.OperationRead {
				statusData[0]++

				switch rc.State {
				case apimodel.ResourceUpdateStateNotStarted:
					statusData[1]++
				case apimodel.ResourceUpdateStateInProgress:
					if rc.CurrentAttempt > 1 {
						statusData[4]++
					} else {
						statusData[2]++
					}
				case apimodel.ResourceUpdateStateSuccess:
					statusData[3]++
				case apimodel.ResourceUpdateStateFailed:
					statusData[5]++
				}
			}
		}

		data[i][3] = fmt.Sprintf("%d", statusData[0])
		data[i][4] = display.Grey(fmt.Sprintf("%d", statusData[1]))
		data[i][5] = fmt.Sprintf("%d", statusData[2])
		data[i][6] = display.Green(fmt.Sprintf("%d", statusData[3]))
		data[i][7] = display.Gold(fmt.Sprintf("%d", statusData[4]))
		data[i][8] = display.Red(fmt.Sprintf("%d", statusData[5]))
		data[i][9] = display.LightBlue(command.StartTs.Format("01/02/2006 3:04PM"))
		data[i][10] = display.LightBlue(formatDuration(calculateDuration(command)))
	}

	err := table.Bulk(data)
	if err != nil {
		return "", fmt.Errorf("error formatting status summary: %v", err)
	}

	if len(status.Commands) == 0 {
		return display.Gold("No commands found.\n"), nil
	} else {
		err := table.Render()
		if err != nil {
			return "", fmt.Errorf("error rendering status summary: %v", err)
		}

		return buf.String(), nil
	}
}

func RenderStatus(s *apimodel.ListCommandStatusResponse) (string, error) {
	var result string
	renderHeader := func(cmd apimodel.Command) string {
		totalDuration := display.Grey("(total duration: ") + display.LightBlue(formatDuration(calculateDuration(cmd))) + display.Grey(")")
		return fmt.Sprintf("%s %s %s: %s, %s",
			cmd.Command,
			display.Grey("command with ID"),
			display.LightBluef("%s", cmd.CommandID),
			coloredCommandState(string(cmd.State)),
			totalDuration)
	}
	for _, cmd := range s.Commands {
		s, err := renderCommand(cmd, renderHeader, formatResourceUpdate)
		if err != nil {
			return "", err
		}
		result += "\n" + s
	}

	return result, nil
}

func renderCommand(cmd apimodel.Command, renderHeader func(apimodel.Command) string, renderResourceUpdate func(*gtree.Node, apimodel.ResourceUpdate)) (string, error) {
	root := gtree.NewRoot(renderHeader(cmd))

	groupedUpdates := make(map[string][]apimodel.ResourceUpdate)
	ungroupedUpdates := make([]apimodel.ResourceUpdate, 0)

	for _, rc := range cmd.ResourceUpdates {
		if rc.GroupID != "" {
			groupedUpdates[rc.GroupID] = append(groupedUpdates[rc.GroupID], rc)
		} else {
			ungroupedUpdates = append(ungroupedUpdates, rc)
		}
	}

	// Detect duplicate labels to avoid gtree node collisions
	labelCounts := make(map[string]int)
	for _, rc := range ungroupedUpdates {
		key := fmt.Sprintf("%s-%s", rc.Operation, rc.ResourceLabel)
		labelCounts[key]++
	}

	for _, group := range groupedUpdates {
		if len(group) > 0 {
			displayUpdate := createDisplayUpdateFromGroup(group)
			renderResourceUpdate(root, displayUpdate)
		}
	}

	for _, rc := range ungroupedUpdates {

		// Deduplicate the gtree node using invisible suffix if the label appears multiple times
		key := fmt.Sprintf("%s-%s", rc.Operation, rc.ResourceLabel)
		if labelCounts[key] > 1 {
			rcUniquified := rc
			rcUniquified.ResourceLabel = fmt.Sprintf("%s%s", rc.ResourceLabel, strings.Repeat("\u200B", labelCounts[key]))
			labelCounts[key]--
			renderResourceUpdate(root, rcUniquified)
		} else {
			renderResourceUpdate(root, rc)
		}
	}

	var buf strings.Builder

	if err := gtree.OutputFromRoot(&buf, root); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func coloredCommandState(state string) string {
	switch state {
	case "Success":
		return display.Green(state)
	case "Failed", "Unknown":
		return display.Red(state)
	default:
		return display.Grey(state)
	}
}

func calculateDuration(cmd apimodel.Command) int64 {
	if cmd.State == "InProgress" || cmd.State == "NotStarted" {
		return time.Since(cmd.StartTs).Milliseconds()
	}
	return cmd.EndTs.Sub(cmd.StartTs).Milliseconds()
}

func formatDuration(millis int64) string {
	duration := time.Duration(millis) * time.Millisecond
	if duration > time.Second {
		duration = duration.Round(time.Second)
	} else {
		duration = duration.Truncate(time.Millisecond)
	}
	return duration.String()
}

func createDisplayUpdateFromGroup(group []apimodel.ResourceUpdate) apimodel.ResourceUpdate {
	if len(group) == 0 {
		return apimodel.ResourceUpdate{}
	}

	displayUpdate := group[0]

	hasDelete := false
	hasCreate := false
	hasUpdate := false

	for _, update := range group {
		switch update.Operation {
		case apimodel.OperationDelete:
			hasDelete = true
		case apimodel.OperationCreate:
			hasCreate = true
		case apimodel.OperationUpdate:
			hasUpdate = true
		}
	}

	if hasDelete && hasCreate {
		displayUpdate.Operation = apimodel.OperationReplace
	} else if hasDelete {
		displayUpdate.Operation = apimodel.OperationDelete
	} else if hasCreate {
		displayUpdate.Operation = apimodel.OperationCreate
	} else if hasUpdate {
		displayUpdate.Operation = apimodel.OperationUpdate
	}

	// Find the worst state in the group for display + preserve the op type
	worstStateUpdate := findWorstStateInGroup(group)
	displayUpdate.State = worstStateUpdate.State
	displayUpdate.Duration = worstStateUpdate.Duration

	return displayUpdate
}

// findWorstStateInGroup finds the update with the worst state in a group
func findWorstStateInGroup(group []apimodel.ResourceUpdate) apimodel.ResourceUpdate {
	if len(group) == 0 {
		return apimodel.ResourceUpdate{}
	}

	worstUpdate := group[0]

	for _, update := range group {
		if update.State == "Failed" {
			worstUpdate = update
			break
		}

		if update.State == apimodel.ResourceUpdateStateInProgress &&
			worstUpdate.State != "Failed" {
			worstUpdate = update
		}
	}

	return worstUpdate
}

func formatSimulatedResourceUpdate(root *gtree.Node, rc apimodel.ResourceUpdate) {
	op := coloredOperation(rc.Operation)

	line := display.Greyf("%s resource %s", op, rc.ResourceLabel)
	node := root.Add(line)

	node.Add(fmt.Sprintf(display.Grey("of type ")+"%s", rc.ResourceType))
	node.Add(formatStackLine(rc.Operation, rc.OldStackName, rc.StackName))

	if rc.Operation == apimodel.OperationUpdate && len(rc.PatchDocument) > 0 {
		propertiesNode := node.Add(display.Grey("by doing the following:"))
		refLabels := rc.ReferenceLabels
		if refLabels == nil {
			refLabels = make(map[string]string)
		}
		FormatPatchDocument(propertiesNode, rc.PatchDocument, rc.Properties, rc.OldProperties, refLabels)
	}
}

func formatResourceUpdate(root *gtree.Node, rc apimodel.ResourceUpdate) {
	op := coloredOperation(rc.Operation)

	line := display.Greyf("%s resource %s", op, rc.ResourceLabel)
	line = line + fmt.Sprintf(": %s", coloredResourceUpdateState(rc.State))
	line = line + formatDurationLine(rc.Duration)

	node := root.Add(line)

	node.Add(display.Greyf("of type %s", rc.ResourceType))
	node.Add(formatStackLine(rc.Operation, rc.OldStackName, rc.StackName))
	addStatusDetails(node, rc)
}

func coloredOperation(operation string) string {
	var colored string
	switch operation {
	case apimodel.OperationReplace:
		colored = display.Red(operation)
	case apimodel.OperationDelete:
		colored = display.Red(operation)
	case apimodel.OperationUpdate:
		colored = display.Gold(operation)
	default:
		colored = display.Green(operation)
	}
	return colored
}

func formatDurationLine(duration int64) string {
	if duration <= 0 {
		return ""
	}
	return display.Greyf(" (duration: %s)", display.LightBlue(formatDuration(duration)))
}

// coloredResourceUpdateState returns the state formatted with appropriate color
func coloredResourceUpdateState(state string) string {
	switch state {
	case "Success":
		return display.Green(string(state))
	case "Failed", "Rejected":
		return display.Red(string(state))
	default:
		return display.Grey(string(state))
	}
}

func formatStackLine(operation, oldStackName, stackName string) string {
	// Handle stack migration from unmanaged to managed
	if oldStackName == constants.UnmanagedStack && oldStackName != stackName {
		return fmt.Sprintf(display.Grey("from ")+"%s"+display.Grey(" to ")+"%s",
			display.Red("unmanaged"), stackName)
	}

	var prefix string
	if operation == apimodel.OperationDelete || operation == apimodel.OperationReplace {
		prefix = "from stack"
	} else {
		prefix = "in stack"
	}

	return display.Greyf("%s %s", prefix, stackName)
}

func addStatusDetails(node *gtree.Node, rc apimodel.ResourceUpdate) {
	switch rc.State {
	case "Failed", "Rejected":
		if rc.ErrorMessage != "" {
			node.Add(display.Grey("reason for failure: ") + display.Red(rc.ErrorMessage))
		}
	case "InProgress":
		node.Add(display.Greyf("attempt: %d/%d", rc.CurrentAttempt, rc.MaxAttempts))

		if rc.StatusMessage != "" {
			node.Add(display.Grey("reason: ") + display.Gold(rc.StatusMessage))
		}
	}
}

// PromptForOperations returns a prompt based on the operations to be performed
func PromptForOperations(cmd *apimodel.Command) string {
	creates, updates, deletes, replaces := analyzeCommands(cmd)

	if creates == 0 && updates == 0 && deletes == 0 && replaces == 0 {
		return ""
	}

	summary := operationSummary(creates, updates, deletes, replaces)
	if summary == "" {
		return ""
	}

	prompt := summary + "\n\nDo you want to continue?"

	return prompt
}

// analyzeCommands analyzes the resource commands and returns operation type counts
func analyzeCommands(cmd *apimodel.Command) (creates, updates, deletes, replaces int) {
	// Group operations by GroupId
	groupedOperations := make(map[string][]apimodel.ResourceUpdate)
	ungroupedOperations := make([]apimodel.ResourceUpdate, 0)

	for _, rc := range cmd.ResourceUpdates {
		if rc.Operation == apimodel.OperationRead {
			continue
		}

		if rc.GroupID != "" {
			groupedOperations[rc.GroupID] = append(groupedOperations[rc.GroupID], rc)
		} else {
			ungroupedOperations = append(ungroupedOperations, rc)
		}
	}

	// Count grouped operations
	for _, group := range groupedOperations {
		if len(group) == 0 {
			continue
		}

		hasDelete := false
		hasCreate := false
		hasUpdate := false

		for _, op := range group {
			switch op.Operation {
			case apimodel.OperationDelete:
				hasDelete = true
			case apimodel.OperationCreate:
				hasCreate = true
			case apimodel.OperationUpdate:
				hasUpdate = true
			}
		}

		if hasDelete && hasCreate {
			replaces++
		} else if hasDelete {
			deletes++
		} else if hasCreate {
			creates++
		} else if hasUpdate {
			updates++
		}
	}

	// Count ungrouped operations
	for _, rc := range ungroupedOperations {
		switch rc.Operation {
		case apimodel.OperationCreate:
			creates++
		case apimodel.OperationUpdate:
			updates++
		case apimodel.OperationDelete:
			deletes++
		case apimodel.OperationReplace:
			replaces++
		}
	}

	return
}

func operationSummary(creates, updates, deletes, replaces int) string {
	if deletes == 0 && replaces == 0 && creates == 0 && updates == 0 {
		return ""
	}

	var parts []string

	// Destructive ops first
	if deletes > 0 {
		parts = append(parts, display.Redf("delete %d resource(s)", deletes))
	}
	if replaces > 0 {
		parts = append(parts, display.Redf("replace %d resource(s)", replaces))
	}
	if creates > 0 {
		parts = append(parts, display.Greenf("create %d resource(s)", creates))
	}
	if updates > 0 {
		parts = append(parts, display.Goldf("update %d resource(s)", updates))
	}

	var joinedParts string
	if len(parts) == 1 {
		joinedParts = parts[0]
	} else {
		joinedParts = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}

	return fmt.Sprintf("This operation will %s.", joinedParts)
}

// RenderInventoryResources renders a table of resources, limited to maxRows if > 0
func RenderInventoryResources(resources []pkgmodel.Resource, maxRows int) (string, error) {
	var buf strings.Builder
	table := tablewriter.NewTable(&buf,
		tablewriter.WithRowAutoWrap(tw.WrapBreak),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On, ShowHeader: tw.On}},
		})))
	table.Header(display.LightBlue("NativeID"), "Stack", "Type", "Label")

	effectiveMaxRows := len(resources)
	if maxRows > 0 && maxRows < len(resources) {
		effectiveMaxRows = maxRows
	}

	data := make([][]string, effectiveMaxRows)
	for i := 0; i < effectiveMaxRows; i++ {
		resource := resources[i]

		data[i] = make([]string, 4)
		data[i][0] = display.LightBlue(resource.NativeID)
		if resource.Stack == constants.UnmanagedStack {
			data[i][1] = display.Red("unmanaged")
		} else {
			data[i][1] = resource.Stack
		}

		data[i][2] = resource.Type
		data[i][3] = resource.Label

	}

	err := table.Bulk(data)
	if err != nil {
		return "", fmt.Errorf("error rendering resources: %v", err)
	}

	if len(resources) == 0 {
		return display.Gold("No resources found.\n"), nil
	} else {
		err := table.Render()
		if err != nil {
			return "", fmt.Errorf("error rendering resources: %v", err)
		}

		summary := fmt.Sprintf("\n%s Showing %d of %d total resources",
			display.Gold("Summary:"),
			effectiveMaxRows,
			len(resources))

		if maxRows > 0 && len(resources) > maxRows {
			summary += fmt.Sprintf(" (use --max-results %d to see all)", len(resources))
		}

		return buf.String() + summary + "\n", nil
	}
}
