// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package renderer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ddddddO/gtree"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/cli/display"
)

const (
	RequiredFieldMissingOnCreate = "humanTemplateOut8"
)

func RenderErrorMessage(err error) (string, error) {
	var msg string
	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]); ok {
		msg, err = renderConflictingCommandsErrorMessage(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]); ok {
		msg, err = renderReconcileRejectedErrorMessage(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaReferencedResourcesNotFoundError]); ok {
		msg, err = renderReferencedResourcesNotFoundError(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaPatchRejectedError]); ok {
		msg, err = renderPatchRejectedError(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if _, ok := err.(*apimodel.ErrorResponse[apimodel.FormaCyclesDetectedError]); ok {
		msg = display.Red("forma rejected because one or more cycles were found in the resolvables\n")
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]); ok {
		msg, err = renderTargetAlreadyExistsError(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.RequiredFieldMissingOnCreateError]); ok {
		msg, err = renderRequiredFieldMissingOnCreateError(&errResp.Data)
		if err != nil {
			return "", err
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.InvalidQueryError]); ok {
		msg = display.Redf("invalid query: %s\n", errResp.Data.Reason)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.StackReferenceNotFoundError]); ok {
		msg = display.Redf("stack `%s` was not found, make sure to reference an existing stack, or provide the stack in the Forma.\n", errResp.Data.StackLabel)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.TargetReferenceNotFoundError]); ok {
		msg = display.Redf("target `%s` was not found, make sure to reference an existing target, or provide the target in the Forma.\n", errResp.Data.TargetLabel)
	}

	if msg == "" {
		return "", err
	}

	return msg, nil
}

func renderConflictingCommandsErrorMessage(conflictingCommands *apimodel.FormaConflictingCommandsError) (string, error) {
	commandIds := make([]string, 0, len(conflictingCommands.ConflictingCommands))
	statuses := make([]string, 0, len(conflictingCommands.ConflictingCommands))
	for _, conflict := range conflictingCommands.ConflictingCommands {
		commandIds = append(commandIds, conflict.CommandID)
		status, err := RenderStatus(&apimodel.ListCommandStatusResponse{Commands: []apimodel.Command{conflict}})
		if err != nil {
			return "", err
		}
		statuses = append(statuses, status)
	}
	message := display.Red("forma command rejected because the following forma command is currently modifying same resources: ") +
		display.LightBluef("%s\n\n", strings.Join(commandIds, ",")) + strings.Join(statuses, "\n") + "\n"
	return message, nil
}

func renderReconcileRejectedErrorMessage(reconcileRejected *apimodel.FormaReconcileRejectedError) (string, error) {
	var commands string
	var deletions string
	for _, change := range reconcileRejected.ModifiedStacks {
		for _, triplet := range change.ModifiedResources {
			if triplet.Operation != string(apimodel.OperationDelete) {
				commands += fmt.Sprintf("       formae extract --query='stack:%s type:%s label:%s' <target forma file>\n", triplet.Stack, triplet.Type, triplet.Label)
			} else {
				deletions += fmt.Sprintf("       manually delete this resource from your code: 'stack: %s, type: %s, label: %s'\n", triplet.Stack, triplet.Type, triplet.Label)
			}
		}
	}
	message := display.Red("forma rejected because the stacks it references have been modified since the last reconcile command.\n\n") +
		display.Gold("There are two options to resolve this issue:\n"+
			"  1) use the '--force' flag to apply the forma anyway (this will overwrite any changes made since the last reconcile), or\n"+
			"  2) manually adjust your own code:\n")
	if commands != "" {
		message += fmt.Sprintf("     - extract the changes made since the last reconcile and incorporate them in your forma before applying it again.\n\n       Here is the list of extract commands to use (use different target file names):\n\n%s\n", commands)
	}
	if deletions != "" {
		message += fmt.Sprintf("    - manually delete these resources that are no longer present in your infrastructure: \n\n%s\n", deletions)
	}

	return message, nil
}

func renderReferencedResourcesNotFoundError(referencesNotFound *apimodel.FormaReferencedResourcesNotFoundError) (string, error) {
	root := gtree.NewRoot(display.Red("forma command rejected because the following referenced resources were not found:"))
	for _, resource := range referencesNotFound.MissingResources {
		node := root.Add(resource.Label)
		node.Add(fmt.Sprintf(display.Grey("of type ")+"%s", resource.Type))
		node.Add(fmt.Sprintf(display.Grey("from stack ")+"%s", resource.Stack))
	}
	var buf strings.Builder
	if err := gtree.OutputFromRoot(&buf, root); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func renderPatchRejectedError(patchRejected *apimodel.FormaPatchRejectedError) (string, error) {
	var buf strings.Builder

	root := gtree.NewRoot(display.Red("forma patch rejected because the following stacks do not exist:"))
	for _, stack := range patchRejected.UnknownStacks {
		root.Add(stack.Label)
	}
	if err := gtree.OutputFromRoot(&buf, root); err != nil {
		return "", err
	}

	buf.WriteString("\n")
	buf.WriteString(display.Gold("A patch can only modify existing stacks. Use '--mode reconcile' to create new stacks.\n"))

	return buf.String(), nil
}

func renderTargetAlreadyExistsError(errResp *apimodel.TargetAlreadyExistsError) (string, error) {
	var message string
	switch errResp.MismatchType {
	case "namespace":
		message = display.Redf("target '%s' already exists with a different namespace:\n\n", errResp.TargetLabel) +
			fmt.Sprintf("  Existing namespace: %s\n", errResp.ExistingNamespace) +
			fmt.Sprintf("  Forma namespace:    %s\n\n", errResp.FormaNamespace) +
			display.Gold("Please either:\n") +
			"  - Use a different target label in your forma file\n" +
			fmt.Sprintf("  - Update your forma file to use the existing namespace: %s\n", errResp.ExistingNamespace)
	case "config":
		existingTree, err := renderTargetTree("Existing target configuration", errResp.ExistingConfig)
		if err != nil {
			return "", err
		}
		formaTree, err := renderTargetTree("Forma target configuration", errResp.FormaConfig)
		if err != nil {
			return "", err
		}
		message = display.Redf("target '%s' already exists with different configuration.\n\n", errResp.TargetLabel) +
			"  Existing target configuration:\n\n" +
			existingTree + "\n" +
			"  Provided target configuration:\n\n" +
			formaTree + "\n" +
			display.Gold("Please either:\n") +
			"  - Use a different target label in your forma file\n" +
			"  - Update your forma file configuration to match the existing target\n"
	default:
		message = display.Goldf("target '%s' already exists.\n\n", errResp.TargetLabel) +
			display.Gold("Please use a different target label in your forma file.\n")
	}

	return message, nil
}

func renderRequiredFieldMissingOnCreateError(errResp *apimodel.RequiredFieldMissingOnCreateError) (string, error) {
	message := display.Red("resource validation failed.\n\n") +
		display.Goldf("resource '%s' (type: %s, stack: %s) cannot be created because the following required fields are missing:\n\n",
			errResp.Label, errResp.Type, errResp.Stack) +
		fmt.Sprintf("  %s\n\n", strings.Join(errResp.MissingFields, ", ")) +
		display.Gold("Please add values for these required fields to your forma file and try again.\n")

	return message, nil
}

func renderTargetTree(label string, target json.RawMessage) (string, error) {
	var buf strings.Builder
	root := gtree.NewRoot(label)
	var data map[string]any
	if err := json.Unmarshal(target, &data); err != nil {
		return "", err
	}
	if err := renderJSONToTree(root, data); err != nil {
		return "", err
	}
	if err := gtree.OutputFromRoot(&buf, root); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderJSONToTree(node *gtree.Node, data any) error {
	switch v := data.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := node.Add(key)
			if err := renderJSONToTree(child, v[key]); err != nil {
				return err
			}
		}
	case []any:
		for i, value := range v {
			child := node.Add(fmt.Sprintf("[%d]", i))
			if err := renderJSONToTree(child, value); err != nil {
				return err
			}
		}
	default:
		node.Add(fmt.Sprintf("%v", v))
	}
	return nil
}
