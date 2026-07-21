// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package errfmt owns the complete error-message formatting behavior for the
// Formae CLI. It reimplements all branches from internal/cli/renderer/errors.go
// using lipgloss for styling (no gtree, no gookit/display).
package errfmt

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// Render formats err into a human-readable string using lipgloss styling.
// It is behaviorally equivalent to renderer.RenderErrorMessage.
// When err is not a known typed API error, it falls back to the error's plain
// message (err.Error(), nil) so callers surface the actual error rather than a
// double-wrapped "error rendering error message" string. A non-nil error is
// returned only when rendering a recognized error type genuinely fails.
func Render(err error) (string, error) {
	th := theme.New("formae")
	r := &renderer{th: th}
	return r.render(err)
}

// renderer holds the lipgloss styles derived from the active theme.
type renderer struct {
	th *theme.Theme
}

// style helpers — inline, no shared state.
func (r *renderer) error(s string) string {
	return lipgloss.NewStyle().Foreground(r.th.Palette.Error).Render(s)
}

func (r *renderer) errorf(format string, args ...any) string {
	return r.error(fmt.Sprintf(format, args...))
}

func (r *renderer) warning(s string) string {
	return lipgloss.NewStyle().Foreground(r.th.Palette.Warning).Render(s)
}

func (r *renderer) warningf(format string, args ...any) string {
	return r.warning(fmt.Sprintf(format, args...))
}

func (r *renderer) accent(s string) string {
	return lipgloss.NewStyle().Foreground(r.th.Palette.PrimaryAccent).Render(s)
}

func (r *renderer) subtle(s string) string {
	return lipgloss.NewStyle().Foreground(r.th.Palette.TextSubtle).Render(s)
}

func (r *renderer) render(err error) (string, error) {
	var msg string

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]); ok {
		var e error
		msg, e = r.renderConflictingCommands(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]); ok {
		var e error
		msg, e = r.renderReconcileRejected(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaReferencedResourcesNotFoundError]); ok {
		var e error
		msg, e = r.renderReferencedResourcesNotFound(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaPatchRejectedError]); ok {
		var e error
		msg, e = r.renderPatchRejected(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]); ok {
		var e error
		msg, e = r.renderEmptyStackRejected(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if _, ok := err.(*apimodel.ErrorResponse[apimodel.FormaCyclesDetectedError]); ok {
		msg = r.error("forma rejected because one or more cycles were found in the resolvables\n")
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]); ok {
		var e error
		msg, e = r.renderTargetAlreadyExists(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.RequiredFieldMissingOnCreateError]); ok {
		var e error
		msg, e = r.renderRequiredFieldMissing(&errResp.Data)
		if e != nil {
			return "", e
		}
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.InvalidQueryError]); ok {
		msg = r.errorf("invalid query: %s\n", errResp.Data.Reason)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.StackReferenceNotFoundError]); ok {
		msg = r.errorf("stack `%s` was not found, make sure to reference an existing stack, or provide the stack in the Forma.\n", errResp.Data.StackLabel)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.TargetReferenceNotFoundError]); ok {
		msg = r.errorf("target `%s` was not found, make sure to reference an existing target, or provide the target in the Forma.\n", errResp.Data.TargetLabel)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.StackDeletedDuringApplyError]); ok {
		msg = r.errorf("stack `%s` was deleted by a concurrent command during apply setup. Please retry.\n", errResp.Data.StackLabel)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.NonPortableResourcesError]); ok {
		msg = r.renderNonPortableResources(&errResp.Data)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.PluginNotFoundError]); ok {
		msg = r.errorf("plugin '%s' not found\n", errResp.Data.Name)
	}

	if errResp, ok := err.(*apimodel.ErrorResponse[apimodel.PluginDependencyConflictError]); ok {
		msg = r.errorf("plugin dependency conflict: %s\n", errResp.Data.Message)
	}

	if msg == "" {
		// Unrecognized error type: fall back to plain rendering so callers
		// surface the actual error instead of double-wrapping it as
		// "error rendering error message: ...". Start() applies the "Error: "
		// prefix and error styling to the returned string.
		return err.Error(), nil
	}

	return msg, nil
}

// renderConflictingCommands formats FormaConflictingCommandsError.
// It renders the list of conflicting command IDs (accent) and a short
// summary of each command's state (using the plain command status lines).
func (r *renderer) renderConflictingCommands(data *apimodel.FormaConflictingCommandsError) (string, error) {
	n := len(data.ConflictingCommands)
	subject, them := "Another command is", "it"
	if n > 1 {
		subject, them = fmt.Sprintf("%d other commands are", n), "them"
	}

	lines := make([]string, 0, n)
	for _, cmd := range data.ConflictingCommands {
		lines = append(lines, r.conflictLine(cmd))
	}

	// Keep every styled span single-line: newlines inside a lipgloss-rendered
	// string get padded to the span's width, which injected stray whitespace.
	return r.error(subject+" already working on the same resources:") + "\n\n" +
		strings.Join(lines, "\n") + "\n\n" +
		r.error("Wait for "+them+" to finish, or cancel with `formae cancel`, then retry.") + "\n", nil
}

// conflictLine renders one conflicting command as an indented summary: the verb
// and mode, the command id (accent, so it's easy to copy into `formae cancel`),
// and the current state.
func (r *renderer) conflictLine(cmd apimodel.Command) string {
	verb := string(cmd.Command)
	if cmd.Mode != "" {
		verb += " · " + string(cmd.Mode)
	}
	return fmt.Sprintf("  %s   %s   %s", verb, r.accent(cmd.CommandID), cmd.State)
}

// renderReconcileRejected formats FormaReconcileRejectedError.
func (r *renderer) renderReconcileRejected(data *apimodel.FormaReconcileRejectedError) (string, error) {
	var commands string
	var deletions string
	for _, change := range data.ModifiedStacks {
		for _, triplet := range change.ModifiedResources {
			if triplet.Operation != string(apimodel.OperationDelete) {
				commands += fmt.Sprintf("       formae extract --query='stack:%s type:%s label:%s' <target forma file>\n", triplet.Stack, triplet.Type, triplet.Label)
			} else {
				deletions += fmt.Sprintf("       manually delete this resource from your code: 'stack: %s, type: %s, label: %s'\n", triplet.Stack, triplet.Type, triplet.Label)
			}
		}
	}
	msg := r.error("forma rejected because the stacks it references have been modified since the last reconcile command.\n\n") +
		r.warning("There are two options to resolve this issue:\n"+
			"  1) use the '--force' flag to apply the forma anyway (this will overwrite any changes made since the last reconcile), or\n"+
			"  2) manually adjust your own code:\n")
	if commands != "" {
		msg += fmt.Sprintf("     - extract the changes made since the last reconcile and incorporate them in your forma before applying it again.\n\n       Here is the list of extract commands to use (use different target file names):\n\n%s\n", commands)
	}
	if deletions != "" {
		msg += fmt.Sprintf("    - manually delete these resources that are no longer present in your infrastructure: \n\n%s\n", deletions)
	}

	return msg, nil
}

// renderReferencedResourcesNotFound formats FormaReferencedResourcesNotFoundError
// as an indented list (replaces gtree).
func (r *renderer) renderReferencedResourcesNotFound(data *apimodel.FormaReferencedResourcesNotFoundError) (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintln(&b, r.error("forma command rejected because the following referenced resources were not found:"))
	for _, resource := range data.MissingResources {
		_, _ = fmt.Fprintf(&b, "  %s\n", resource.Label)
		_, _ = fmt.Fprintf(&b, "    %s%s\n", r.subtle("of type "), resource.Type)
		_, _ = fmt.Fprintf(&b, "    %s%s\n", r.subtle("from stack "), resource.Stack)
	}
	return b.String(), nil
}

// renderPatchRejected formats FormaPatchRejectedError as an indented list.
func (r *renderer) renderPatchRejected(data *apimodel.FormaPatchRejectedError) (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintln(&b, r.error("forma patch rejected because the following stacks do not exist:"))
	for _, stack := range data.UnknownStacks {
		_, _ = fmt.Fprintf(&b, "  %s\n", stack.Label)
	}
	b.WriteString("\n")
	b.WriteString(r.warning("A patch can only modify existing stacks. Use '--mode reconcile' to create new stacks.\n"))
	return b.String(), nil
}

// renderEmptyStackRejected formats FormaEmptyStackRejectedError as an indented list.
func (r *renderer) renderEmptyStackRejected(data *apimodel.FormaEmptyStackRejectedError) (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintln(&b, r.error("forma rejected because creating empty stacks is not allowed:"))
	for _, stackLabel := range data.EmptyStacks {
		_, _ = fmt.Fprintf(&b, "  %s\n", stackLabel)
	}
	b.WriteString("\n")
	b.WriteString(r.warning("Stacks must contain at least one resource. Empty stacks are automatically cleaned up when the last resource is removed.\n"))
	return b.String(), nil
}

// renderTargetAlreadyExists formats TargetAlreadyExistsError.
// Three sub-branches: "namespace", "config", and plain already-exists (default).
func (r *renderer) renderTargetAlreadyExists(data *apimodel.TargetAlreadyExistsError) (string, error) {
	var msg string
	switch data.MismatchType {
	case "namespace":
		msg = r.errorf("target '%s' already exists with a different namespace:\n\n", data.TargetLabel) +
			fmt.Sprintf("  Existing namespace: %s\n", data.ExistingNamespace) +
			fmt.Sprintf("  Forma namespace:    %s\n\n", data.FormaNamespace) +
			r.warning("Please either:\n") +
			"  - Use a different target label in your forma file\n" +
			fmt.Sprintf("  - Update your forma file to use the existing namespace: %s\n", data.ExistingNamespace)
	case "config":
		existingTree, err := r.renderTargetTree("Existing target configuration", data.ExistingConfig)
		if err != nil {
			return "", err
		}
		formaTree, err := r.renderTargetTree("Forma target configuration", data.FormaConfig)
		if err != nil {
			return "", err
		}
		msg = r.errorf("target '%s' already exists with different configuration.\n\n", data.TargetLabel) +
			"  Existing target configuration:\n\n" +
			existingTree + "\n" +
			"  Provided target configuration:\n\n" +
			formaTree + "\n" +
			r.warning("Please either:\n") +
			"  - Use a different target label in your forma file\n" +
			"  - Update your forma file configuration to match the existing target\n"
	default:
		msg = r.warningf("target '%s' already exists.\n\n", data.TargetLabel) +
			r.warning("Please use a different target label in your forma file.\n")
	}
	return msg, nil
}

// renderRequiredFieldMissing formats RequiredFieldMissingOnCreateError.
func (r *renderer) renderRequiredFieldMissing(data *apimodel.RequiredFieldMissingOnCreateError) (string, error) {
	msg := r.error("resource validation failed.\n\n") +
		r.warningf("resource '%s' (type: %s, stack: %s) cannot be created because the following required fields are missing:\n\n",
			data.Label, data.Type, data.Stack) +
		fmt.Sprintf("  %s\n\n", strings.Join(data.MissingFields, ", ")) +
		r.warning("Please add values for these required fields to your forma file and try again.\n")
	return msg, nil
}

// renderNonPortableResources formats NonPortableResourcesError.
func (r *renderer) renderNonPortableResources(data *apimodel.NonPortableResourcesError) string {
	msg := r.errorf("Cannot replace target '%s'\n\n", data.TargetLabel) +
		r.warning("The following resources are bound to the current target configuration and\ncannot be automatically moved to the new target:\n\n")
	for _, res := range data.Resources {
		msg += fmt.Sprintf("  - %s\n", res)
	}
	msg += "\n" + r.warning("To proceed, manually remove these resources first with 'formae destroy',\nthen reapply to recreate them with the new target configuration.\n")
	return msg
}

// renderTargetTree renders a JSON target config as an indented list (replaces gtree).
func (r *renderer) renderTargetTree(label string, target json.RawMessage) (string, error) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "    %s\n", label)

	if len(target) == 0 {
		_, _ = fmt.Fprintln(&b, "      <no configuration>")
		return b.String(), nil
	}

	var data map[string]any
	if err := json.Unmarshal(target, &data); err != nil {
		return "", err
	}

	if len(data) == 0 {
		_, _ = fmt.Fprintln(&b, "      <empty configuration {}>")
		return b.String(), nil
	}

	renderJSONToIndentedList(&b, data, "      ")
	return b.String(), nil
}

// renderJSONToIndentedList renders a JSON value as indented key/value lines.
// This reimplements the gtree-based renderJSONToTree from renderer/errors.go.
func renderJSONToIndentedList(b *strings.Builder, data any, indent string) {
	switch v := data.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = fmt.Fprintf(b, "%s%s:\n", indent, key)
			renderJSONToIndentedList(b, v[key], indent+"  ")
		}
	case []any:
		for i, value := range v {
			_, _ = fmt.Fprintf(b, "%s[%d]:\n", indent, i)
			renderJSONToIndentedList(b, value, indent+"  ")
		}
	default:
		_, _ = fmt.Fprintf(b, "%s%v\n", indent, v)
	}
}
