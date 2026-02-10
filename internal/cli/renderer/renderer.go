// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ddddddO/gtree"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"

	"github.com/platform-engineering-labs/formae/internal/cli/display"
	"github.com/platform-engineering-labs/formae/internal/constants"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func RenderSimulation(s *apimodel.Simulation) (string, error) {
	renderHeader := func(cmd apimodel.Command) string { return "Command will" }
	command, err := renderCommand(s.Command, renderHeader, formatSimulatedResourceUpdate, formatSimulatedTargetUpdate, formatSimulatedStackUpdate, formatSimulatedPolicyUpdate)
	if err != nil {
		return "", err
	}
	return command, nil
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
		display.Grey("Canceled"),
		display.LightBlue("Started At"),
		display.LightBlue("Time"))

	data := make([][]any, len(status.Commands))
	for i, command := range status.Commands {
		data[i] = make([]any, 12)
		data[i][0] = display.LightBlue(string(command.CommandID))
		data[i][1] = command.Command
		status := display.Grey(string(command.State))
		switch command.State {
		case "Success":
			status = display.Green(string(command.State))
		case "Failed":
			status = display.Red(string(command.State))
		case "Canceled":
			status = display.Grey(string(command.State))
		}

		data[i][2] = status

		statusData := countUpdateStates(command.ResourceUpdates, command.TargetUpdates, command.StackUpdates, command.PolicyUpdates)

		data[i][3] = fmt.Sprintf("%d", statusData[0])
		data[i][4] = display.Grey(fmt.Sprintf("%d", statusData[1]))
		data[i][5] = fmt.Sprintf("%d", statusData[2])
		data[i][6] = display.Green(fmt.Sprintf("%d", statusData[3]))
		data[i][7] = display.Gold(fmt.Sprintf("%d", statusData[4]))
		data[i][8] = display.Red(fmt.Sprintf("%d", statusData[5]))
		data[i][9] = display.Grey(fmt.Sprintf("%d", statusData[6]))
		data[i][10] = display.LightBlue(command.StartTs.Format("01/02/2006 3:04PM"))
		data[i][11] = display.LightBlue(formatDuration(calculateDuration(command)))
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

// countUpdateStates counts states for resource, target, stack, and policy updates
// Returns [total, waiting, inProgress, success, retry, failed, canceled]
func countUpdateStates(resourceUpdates []apimodel.ResourceUpdate, targetUpdates []apimodel.TargetUpdate, stackUpdates []apimodel.StackUpdate, policyUpdates []apimodel.PolicyUpdate) []int {
	statusData := make([]int, 7)

	// Count resource updates
	for _, rc := range resourceUpdates {
		if rc.Operation != apimodel.OperationRead {
			statusData[0]++ // Total changes

			switch rc.State {
			case apimodel.ResourceUpdateStateNotStarted:
				statusData[1]++ // Waiting
			case apimodel.ResourceUpdateStateInProgress:
				if rc.CurrentAttempt > 1 {
					statusData[4]++ // Retry
				} else {
					statusData[2]++ // In Progress
				}
			case apimodel.ResourceUpdateStateSuccess:
				statusData[3]++ // Success
			case apimodel.ResourceUpdateStateFailed:
				statusData[5]++ // Failed
			case apimodel.ResourceUpdateStateCanceled:
				statusData[6]++ // Canceled
			}
		}
	}

	// Count target updates (create/update operations only, no read operations for targets)
	for _, tu := range targetUpdates {
		statusData[0]++ // Total changes

		switch tu.State {
		case "NotStarted":
			statusData[1]++ // Waiting
		case "Success":
			statusData[3]++ // Success
		case "Failed":
			statusData[5]++ // Failed
		}
	}

	// Count stack updates (create/update operations)
	for _, su := range stackUpdates {
		statusData[0]++ // Total changes

		switch su.State {
		case "NotStarted":
			statusData[1]++ // Waiting
		case "Success":
			statusData[3]++ // Success
		case "Failed":
			statusData[5]++ // Failed
		}
	}

	// Count policy updates
	for _, pu := range policyUpdates {
		statusData[0]++ // Total changes

		switch pu.State {
		case "NotStarted":
			statusData[1]++ // Waiting
		case "Success":
			statusData[3]++ // Success
		case "Failed":
			statusData[5]++ // Failed
		}
	}

	return statusData
}

func RenderStatus(s *apimodel.ListCommandStatusResponse) (string, error) {
	var result string
	renderHeader := func(cmd apimodel.Command) string {
		totalDuration := display.Grey("(total duration: ") + display.LightBlue(formatDuration(calculateDuration(cmd))) + display.Grey(")")
		return fmt.Sprintf("%s %s %s: %s %s",
			cmd.Command,
			display.Grey("command with ID"),
			display.LightBluef("%s", cmd.CommandID),
			coloredCommandState(string(cmd.State)),
			totalDuration)
	}
	for _, cmd := range s.Commands {
		// Create a context-aware formatter for canceled commands
		var formatter func(*gtree.Node, apimodel.ResourceUpdate)
		if cmd.State == "Canceled" {
			formatter = formatResourceUpdateForCanceledCommand
		} else {
			formatter = formatResourceUpdate
		}

		s, err := renderCommand(cmd, renderHeader, formatter, formatTargetUpdate, formatStackUpdate, formatPolicyUpdate)
		if err != nil {
			return "", err
		}
		result += "\n" + s
	}

	return result, nil
}

func renderCommand(cmd apimodel.Command, renderHeader func(apimodel.Command) string, renderResourceUpdate func(*gtree.Node, apimodel.ResourceUpdate), renderTargetUpdate func(*gtree.Node, apimodel.TargetUpdate), renderStackUpdate func(*gtree.Node, apimodel.StackUpdate), renderPolicyUpdate func(*gtree.Node, apimodel.PolicyUpdate)) (string, error) {
	root := gtree.NewRoot(renderHeader(cmd))

	// Render stack updates first (before targets and resources)
	for _, su := range cmd.StackUpdates {
		renderStackUpdate(root, su)
	}

	// Render policy updates (after stacks, before targets)
	for _, pu := range cmd.PolicyUpdates {
		renderPolicyUpdate(root, pu)
	}

	// Render target updates (before resources)
	for _, tu := range cmd.TargetUpdates {
		renderTargetUpdate(root, tu)
	}

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
	if rc.IsCascade {
		line = line + display.Gold(" (cascade)")
	}
	node := root.Add(line)

	node.Add(fmt.Sprintf(display.Grey("of type ")+"%s", rc.ResourceType))
	node.Add(formatStackLine(rc.Operation, rc.OldStackName, rc.StackName))

	if rc.IsCascade && rc.CascadeSource != "" {
		node.Add(display.Grey("because it depends on ") + display.LightBlue(rc.CascadeSource))
	}

	if rc.Operation == apimodel.OperationUpdate && len(rc.PatchDocument) > 0 {
		propertiesNode := node.Add(display.Grey("by doing the following:"))
		refLabels := rc.ReferenceLabels
		if refLabels == nil {
			refLabels = make(map[string]string)
		}
		FormatPatchDocument(propertiesNode, rc.PatchDocument, rc.Properties, rc.OldProperties, refLabels, rc.OldStackName)
	}
}

func formatResourceUpdate(root *gtree.Node, rc apimodel.ResourceUpdate) {
	op := coloredOperation(rc.Operation)

	line := display.Greyf("%s resource %s", op, rc.ResourceLabel)
	line = line + fmt.Sprintf(": %s", coloredUpdateState(rc.State))
	line = line + formatDurationLine(rc.Duration)

	node := root.Add(line)

	node.Add(display.Greyf("of type %s", rc.ResourceType))
	node.Add(formatStackLine(rc.Operation, rc.OldStackName, rc.StackName))
	addStatusDetails(node, rc)
}

// formatResourceUpdateForCanceledCommand formats resource updates for commands that were canceled.
// In the context of a canceled command, the semantics flip:
// - "Canceled" resource updates represent successful cancellations (shown in green)
// - "Success" resource updates represent operations that completed before cancellation (shown in grey)
func formatResourceUpdateForCanceledCommand(root *gtree.Node, rc apimodel.ResourceUpdate) {
	op := coloredOperation(rc.Operation)

	line := display.Greyf("%s resource %s", op, rc.ResourceLabel)
	line = line + fmt.Sprintf(": %s", coloredResourceUpdateStateForCanceledCommand(rc.State))
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

// coloredUpdateState returns the state formatted with appropriate color for both resource and target updates
func coloredUpdateState(state string) string {
	switch state {
	case "Success":
		return display.Green(state)
	case "Failed", "Rejected":
		return display.Red(state)
	default:
		return display.Grey(state)
	}
}

// coloredResourceUpdateStateForCanceledCommand returns the state formatted with appropriate color
// for resource updates belonging to a canceled command. The semantics are flipped:
// - "Canceled" is considered success (the user wanted to cancel) -> shown in green
// - "Failed" and "Rejected" are similar as "Canceled" as they did not execute, which is what the user wanted -> shown in green
// - "Success" means the operation completed before cancellation -> shown in red with different text
func coloredResourceUpdateStateForCanceledCommand(state string) string {
	switch state {
	case "Canceled":
		return display.Green(string(state))
	case "Success":
		return display.Red("Completed (unable to cancel)")
	case "Failed", "Rejected":
		return display.Green(string(state))
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

// addUpdateStatusDetails adds status details for both resource and target updates
func addUpdateStatusDetails(node *gtree.Node, state, errorMessage, statusMessage string, currentAttempt, maxAttempts int) {
	switch state {
	case "Failed", "Rejected":
		if errorMessage != "" {
			node.Add(display.Grey("reason for failure: ") + display.Red(errorMessage))
		}
	case "InProgress":
		if maxAttempts > 0 {
			node.Add(display.Greyf("attempt: %d/%d", currentAttempt, maxAttempts))
		}

		if statusMessage != "" {
			node.Add(display.Grey("reason: ") + display.Gold(statusMessage))
		}
	}
}

func addStatusDetails(node *gtree.Node, rc apimodel.ResourceUpdate) {
	addUpdateStatusDetails(node, rc.State, rc.ErrorMessage, rc.StatusMessage, rc.CurrentAttempt, rc.MaxAttempts)
}

// formatSimulatedTargetUpdate formats a target update for simulation view
func formatSimulatedTargetUpdate(root *gtree.Node, tu apimodel.TargetUpdate) {
	op := coloredOperation(tu.Operation)

	var line string
	if tu.Operation == "create" {
		line = display.Greyf("%s target %s", op, tu.TargetLabel)
	} else if tu.Operation == "update" {
		discoverableText := "discoverable"
		if !tu.Discoverable {
			discoverableText = "not discoverable"
		}
		line = display.Greyf("%s target %s to %s", op, tu.TargetLabel, discoverableText)
	} else {
		line = display.Greyf("%s target %s", op, tu.TargetLabel)
	}

	root.Add(line)
}

// formatTargetUpdate formats a target update for status view
func formatTargetUpdate(root *gtree.Node, tu apimodel.TargetUpdate) {
	op := coloredOperation(tu.Operation)

	var line string
	if tu.Operation == "create" {
		line = display.Greyf("%s target %s", op, tu.TargetLabel)
	} else if tu.Operation == "update" {
		discoverableText := "discoverable"
		if !tu.Discoverable {
			discoverableText = "not discoverable"
		}
		line = display.Greyf("%s target %s to %s", op, tu.TargetLabel, discoverableText)
	} else {
		line = display.Greyf("%s target %s", op, tu.TargetLabel)
	}

	line = line + fmt.Sprintf(": %s", coloredUpdateState(tu.State))

	node := root.Add(line)

	// Use unified status details function (targets don't have attempt counts or status messages currently)
	addUpdateStatusDetails(node, tu.State, tu.ErrorMessage, "", 0, 0)
}

// formatSimulatedStackUpdate formats a stack update for simulation view
func formatSimulatedStackUpdate(root *gtree.Node, su apimodel.StackUpdate) {
	op := coloredOperation(su.Operation)
	line := display.Greyf("%s stack %s", op, su.StackLabel)
	if su.Description != "" {
		line += display.Greyf(" (description: %s)", su.Description)
	}
	root.Add(line)
}

// formatStackUpdate formats a stack update for status view
func formatStackUpdate(root *gtree.Node, su apimodel.StackUpdate) {
	op := coloredOperation(su.Operation)
	line := display.Greyf("%s stack %s", op, su.StackLabel)
	line = line + fmt.Sprintf(": %s", coloredUpdateState(su.State))
	line = line + formatDurationLine(su.Duration)

	node := root.Add(line)

	if su.Description != "" {
		node.Add(display.Greyf("description: %s", su.Description))
	}

	// Add error details if failed
	if su.State == "Failed" && su.ErrorMessage != "" {
		node.Add(display.Grey("reason for failure: ") + display.Red(su.ErrorMessage))
	}
}

// formatSimulatedPolicyUpdate formats a policy update for simulation view
func formatSimulatedPolicyUpdate(root *gtree.Node, pu apimodel.PolicyUpdate) {
	op := coloredOperation(pu.Operation)
	label := formatPolicyLabel(pu)
	root.Add(display.Greyf("%s policy %s", op, label))
}

// formatPolicyUpdate formats a policy update for status view
func formatPolicyUpdate(root *gtree.Node, pu apimodel.PolicyUpdate) {
	op := coloredOperation(pu.Operation)
	label := formatPolicyLabel(pu)
	line := display.Greyf("%s policy %s", op, label)
	line = line + fmt.Sprintf(": %s", coloredUpdateState(pu.State))
	line = line + formatDurationLine(pu.Duration)

	node := root.Add(line)

	// Add error details if failed
	if pu.State == "Failed" && pu.ErrorMessage != "" {
		node.Add(display.Grey("reason for failure: ") + display.Red(pu.ErrorMessage))
	}
}

// formatPolicyLabel formats a policy label for display
// For inline policies (auto-generated labels like "stackName-policyType-ksuid"),
// we simplify the display to just show the type and stack.
func formatPolicyLabel(pu apimodel.PolicyUpdate) string {
	// Check if this is an auto-generated label (inline policy)
	// Auto-generated labels follow the pattern: {stackLabel}-{policyType}-{ksuidPrefix}
	isAutoGenerated := false
	if pu.StackLabel != "" && pu.PolicyLabel != "" {
		prefix := pu.StackLabel + "-" + pu.PolicyType + "-"
		if strings.HasPrefix(pu.PolicyLabel, prefix) {
			isAutoGenerated = true
		}
	}

	var result string
	if isAutoGenerated || pu.PolicyLabel == "" {
		// For inline policies, just show the type
		result = pu.PolicyType
	} else {
		// For standalone policies with explicit labels, show label and type
		result = fmt.Sprintf("%s (%s)", pu.PolicyLabel, pu.PolicyType)
	}

	if pu.StackLabel != "" {
		result += display.Greyf(" on stack %s", pu.StackLabel)
	}
	return result
}

// PromptForOperations returns a prompt based on the operations to be performed
func PromptForOperations(cmd *apimodel.Command) string {
	targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces := analyzeCommands(cmd)

	if targetCreates == 0 && targetUpdates == 0 && stackCreates == 0 && stackUpdates == 0 && policyCreates == 0 && policyUpdates == 0 && resourceCreates == 0 && resourceUpdates == 0 && resourceDeletes == 0 && resourceReplaces == 0 {
		return ""
	}

	summary := operationSummary(targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces)
	if summary == "" {
		return ""
	}

	prompt := summary + "\n\nDo you want to continue?"

	return prompt
}

// analyzeCommands analyzes the resource and target commands and returns operation type counts
func analyzeCommands(cmd *apimodel.Command) (targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces int) {
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
			resourceReplaces++
		} else if hasDelete {
			resourceDeletes++
		} else if hasCreate {
			resourceCreates++
		} else if hasUpdate {
			resourceUpdates++
		}
	}

	// Count ungrouped operations
	for _, rc := range ungroupedOperations {
		switch rc.Operation {
		case apimodel.OperationCreate:
			resourceCreates++
		case apimodel.OperationUpdate:
			resourceUpdates++
		case apimodel.OperationDelete:
			resourceDeletes++
		case apimodel.OperationReplace:
			resourceReplaces++
		}
	}

	for _, tu := range cmd.TargetUpdates {
		switch tu.Operation {
		case "create":
			targetCreates++
		case "update":
			targetUpdates++
		}
	}

	// Count stack updates
	for _, su := range cmd.StackUpdates {
		switch su.Operation {
		case "create":
			stackCreates++
		case "update":
			stackUpdates++
		}
	}

	// Count policy updates
	for _, pu := range cmd.PolicyUpdates {
		switch pu.Operation {
		case "create":
			policyCreates++
		case "update":
			policyUpdates++
		}
	}

	return
}

func operationSummary(targetCreates, targetUpdates, stackCreates, stackUpdates, policyCreates, policyUpdates, resourceCreates, resourceUpdates, resourceDeletes, resourceReplaces int) string {
	if targetCreates == 0 && targetUpdates == 0 && stackCreates == 0 && stackUpdates == 0 && policyCreates == 0 && policyUpdates == 0 && resourceCreates == 0 && resourceUpdates == 0 && resourceDeletes == 0 && resourceReplaces == 0 {
		return ""
	}

	var parts []string

	// Destructive resource ops first
	if resourceDeletes > 0 {
		parts = append(parts, display.Redf("delete %d resource(s)", resourceDeletes))
	}
	if resourceReplaces > 0 {
		parts = append(parts, display.Redf("replace %d resource(s)", resourceReplaces))
	}

	// Creates (stacks, policies, targets, resources)
	if stackCreates > 0 {
		parts = append(parts, display.Greenf("create %d stack(s)", stackCreates))
	}
	if policyCreates > 0 {
		parts = append(parts, display.Greenf("create %d policy(ies)", policyCreates))
	}
	if targetCreates > 0 {
		parts = append(parts, display.Greenf("create %d target(s)", targetCreates))
	}
	if resourceCreates > 0 {
		parts = append(parts, display.Greenf("create %d resource(s)", resourceCreates))
	}

	// Updates (stacks, policies, targets, resources)
	if stackUpdates > 0 {
		parts = append(parts, display.Goldf("update %d stack(s)", stackUpdates))
	}
	if policyUpdates > 0 {
		parts = append(parts, display.Goldf("update %d policy(ies)", policyUpdates))
	}
	if targetUpdates > 0 {
		parts = append(parts, display.Goldf("update %d target(s)", targetUpdates))
	}
	if resourceUpdates > 0 {
		parts = append(parts, display.Goldf("update %d resource(s)", resourceUpdates))
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

// RenderInventoryTargets renders a list of targets in a table format
func RenderInventoryTargets(targets []*pkgmodel.Target, maxRows int) (string, error) {
	var buf strings.Builder
	table := tablewriter.NewTable(&buf,
		tablewriter.WithRowAutoWrap(tw.WrapBreak),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On, ShowHeader: tw.On}},
		})))
	table.Header(display.LightBlue("Label"), "Namespace", "Discoverable", "Config")

	effectiveMaxRows := len(targets)
	if maxRows > 0 && maxRows < len(targets) {
		effectiveMaxRows = maxRows
	}

	data := make([][]string, effectiveMaxRows)
	for i := 0; i < effectiveMaxRows; i++ {
		target := targets[i]

		data[i] = make([]string, 4)
		data[i][0] = display.LightBlue(target.Label)
		data[i][1] = target.Namespace

		if target.Discoverable {
			data[i][2] = display.Green("true")
		} else {
			data[i][2] = "false"
		}

		// Format config with sensitive field filtering
		data[i][3] = formatTargetConfig(target.Config)
	}

	err := table.Bulk(data)
	if err != nil {
		return "", fmt.Errorf("error rendering targets: %v", err)
	}

	if len(targets) == 0 {
		return display.Gold("No targets found.\n"), nil
	}

	err = table.Render()
	if err != nil {
		return "", fmt.Errorf("error rendering targets: %v", err)
	}

	summary := fmt.Sprintf("\n%s Showing %d of %d total targets",
		display.Gold("Summary:"),
		effectiveMaxRows,
		len(targets))

	if maxRows > 0 && len(targets) > maxRows {
		summary += fmt.Sprintf(" (use --max-results %d to see all)", len(targets))
	}

	return buf.String() + summary + "\n", nil
}

// RenderInventoryStacks renders a list of stacks in a table format
func RenderInventoryStacks(stacks []*pkgmodel.Stack, maxRows int) (string, error) {
	var buf strings.Builder
	table := tablewriter.NewTable(&buf,
		tablewriter.WithRowAutoWrap(tw.WrapBreak),
		tablewriter.WithHeaderAutoFormat(tw.Off),
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Settings: tw.Settings{Separators: tw.Separators{BetweenRows: tw.On, ShowHeader: tw.On}},
		})))
	table.Header(display.LightBlue("Label"), "Description", "Policies")

	effectiveMaxRows := len(stacks)
	if maxRows > 0 && maxRows < len(stacks) {
		effectiveMaxRows = maxRows
	}

	data := make([][]string, effectiveMaxRows)
	for i := 0; i < effectiveMaxRows; i++ {
		stack := stacks[i]

		data[i] = make([]string, 3)
		data[i][0] = display.LightBlue(stack.Label)
		data[i][1] = stack.Description
		data[i][2] = formatStackPolicies(stack.Policies)
	}

	err := table.Bulk(data)
	if err != nil {
		return "", fmt.Errorf("error rendering stacks: %v", err)
	}

	if len(stacks) == 0 {
		return display.Gold("No stacks found.\n"), nil
	}

	err = table.Render()
	if err != nil {
		return "", fmt.Errorf("error rendering stacks: %v", err)
	}

	summary := fmt.Sprintf("\n%s Showing %d of %d total stacks",
		display.Gold("Summary:"),
		effectiveMaxRows,
		len(stacks))

	if maxRows > 0 && len(stacks) > maxRows {
		summary += fmt.Sprintf(" (use --max-results %d to see all)", len(stacks))
	}

	return buf.String() + summary + "\n", nil
}

// formatTargetConfig converts a target config to a human-readable string
func formatTargetConfig(configJSON json.RawMessage) string {
	if len(configJSON) == 0 {
		return ""
	}

	var config map[string]any
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return string(configJSON) // fallback to raw JSON
	}

	var parts []string
	for key, value := range config {
		var valueStr string
		switch v := value.(type) {
		case string:
			valueStr = v
		case bool:
			valueStr = fmt.Sprintf("%t", v)
		case float64:
			valueStr = fmt.Sprintf("%.0f", v)
		default:
			valueStr = fmt.Sprintf("%v", v)
		}

		parts = append(parts, fmt.Sprintf("%s=%s", key, valueStr))
	}
	sort.Strings(parts)

	return strings.Join(parts, ", ")
}

// formatStackPolicies formats the policies for display in the stacks table
func formatStackPolicies(policies []json.RawMessage) string {
	if len(policies) == 0 {
		return "-"
	}

	var parts []string
	for _, policyJSON := range policies {
		// Parse the policy to get type and details
		var policy map[string]any
		if err := json.Unmarshal(policyJSON, &policy); err != nil {
			continue
		}

		policyType, _ := policy["Type"].(string)
		switch policyType {
		case "ttl":
			ttlSeconds, _ := policy["TTLSeconds"].(float64)
			duration := time.Duration(int64(ttlSeconds)) * time.Second
			label, _ := policy["Label"].(string)
			if label != "" {
				parts = append(parts, fmt.Sprintf("TTL: %s (%s)", formatTTLDuration(duration), label))
			} else {
				parts = append(parts, fmt.Sprintf("TTL: %s", formatTTLDuration(duration)))
			}
		default:
			if policyType != "" {
				parts = append(parts, policyType)
			}
		}
	}

	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// formatTTLDuration formats a duration in a human-friendly way
func formatTTLDuration(d time.Duration) string {
	if d >= 24*time.Hour {
		days := d / (24 * time.Hour)
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		hours := d / time.Hour
		return fmt.Sprintf("%dh", hours)
	}
	if d >= time.Minute {
		minutes := d / time.Minute
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// RenderCancelCommandResponse renders the result of a cancel command
func RenderCancelCommandResponse(response *apimodel.CancelCommandResponse) (string, error) {
	var buf strings.Builder

	if response == nil || len(response.CommandIDs) == 0 {
		return display.Gold("No commands to cancel.\n"), nil
	}

	buf.WriteString(display.Gold("Commands are being canceled:\n\n"))
	for _, cmdID := range response.CommandIDs {
		buf.WriteString(fmt.Sprintf("  %s %s\n", display.Green("•"), cmdID))
	}

	buf.WriteString(fmt.Sprintf("\n%s\n", display.Grey("Use 'formae status' to check the cancellation progress.")))

	return buf.String(), nil
}
