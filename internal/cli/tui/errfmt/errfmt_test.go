// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package errfmt

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// ── 1. FormaConflictingCommandsError ──────────────────────────────────────────

func TestRender_ConflictingCommands_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]{
		ErrorType: apimodel.ConflictingCommands,
		Data: apimodel.FormaConflictingCommandsError{
			ConflictingCommands: []apimodel.Command{
				{
					CommandID: "cmd-abc123",
					Command:   "apply",
					Mode:      "reconcile",
					State:     "InProgress",
				},
			},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 2. FormaReconcileRejectedError ────────────────────────────────────────────

func TestRender_ReconcileRejected_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]{
		ErrorType: apimodel.ReconcileRejected,
		Data: apimodel.FormaReconcileRejectedError{
			ModifiedStacks: map[string]apimodel.ModifiedStack{
				"production": {
					ModifiedResources: []apimodel.ResourceModification{
						{Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket", Operation: "update"},
						{Stack: "production", Type: "AWS::EC2::Instance", Label: "web-1", Operation: "delete"},
					},
				},
			},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 3. FormaReferencedResourcesNotFoundError ──────────────────────────────────

func TestRender_ReferencedResourcesNotFound_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaReferencedResourcesNotFoundError]{
		ErrorType: apimodel.ReferencedResourcesNotFound,
		Data: apimodel.FormaReferencedResourcesNotFoundError{
			MissingResources: []*pkgmodel.Resource{
				{Label: "my-vpc", Type: "AWS::EC2::VPC", Stack: "networking"},
				{Label: "my-subnet", Type: "AWS::EC2::Subnet", Stack: "networking"},
			},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 4. FormaPatchRejectedError ────────────────────────────────────────────────

func TestRender_PatchRejected_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaPatchRejectedError]{
		ErrorType: apimodel.PatchRejected,
		Data: apimodel.FormaPatchRejectedError{
			UnknownStacks: []*pkgmodel.Stack{
				{Label: "staging"},
				{Label: "canary"},
			},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 5. FormaEmptyStackRejectedError ───────────────────────────────────────────

func TestRender_EmptyStackRejected_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]{
		ErrorType: apimodel.EmptyStackRejected,
		Data: apimodel.FormaEmptyStackRejectedError{
			EmptyStacks: []string{"staging", "canary"},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 6. FormaCyclesDetectedError ───────────────────────────────────────────────

func TestRender_CyclesDetected_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.FormaCyclesDetectedError]{
		ErrorType: apimodel.CyclesDetected,
		Data:      apimodel.FormaCyclesDetectedError{},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 7a. TargetAlreadyExistsError — namespace mismatch ────────────────────────

func TestRender_TargetAlreadyExists_Namespace_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]{
		ErrorType: apimodel.TargetAlreadyExists,
		Data: apimodel.TargetAlreadyExistsError{
			TargetLabel:       "aws-us-east-1",
			MismatchType:      "namespace",
			ExistingNamespace: "arn:aws:iam::111111111111:role/existing-role",
			FormaNamespace:    "arn:aws:iam::222222222222:role/new-role",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 7b. TargetAlreadyExistsError — config mismatch ───────────────────────────

func TestRender_TargetAlreadyExists_Config_Golden(t *testing.T) {
	existingConfig := json.RawMessage(`{"region":"us-east-1","account":"111111111111"}`)
	formaConfig := json.RawMessage(`{"region":"eu-west-1","account":"222222222222"}`)
	err := &apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]{
		ErrorType: apimodel.TargetAlreadyExists,
		Data: apimodel.TargetAlreadyExistsError{
			TargetLabel:    "aws-prod",
			MismatchType:   "config",
			ExistingConfig: existingConfig,
			FormaConfig:    formaConfig,
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 7c. TargetAlreadyExistsError — plain already-exists (default) ─────────────

func TestRender_TargetAlreadyExists_Plain_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]{
		ErrorType: apimodel.TargetAlreadyExists,
		Data: apimodel.TargetAlreadyExistsError{
			TargetLabel:  "aws-staging",
			MismatchType: "",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 8. RequiredFieldMissingOnCreateError ──────────────────────────────────────

func TestRender_RequiredFieldMissing_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.RequiredFieldMissingOnCreateError]{
		ErrorType: apimodel.RequiredFieldMissingOnCreate,
		Data: apimodel.RequiredFieldMissingOnCreateError{
			Label:         "my-bucket",
			Type:          "AWS::S3::Bucket",
			Stack:         "production",
			MissingFields: []string{"BucketName", "Tags"},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 9. NonPortableResourcesError ──────────────────────────────────────────────

func TestRender_NonPortableResources_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.NonPortableResourcesError]{
		ErrorType: apimodel.NonPortableResources,
		Data: apimodel.NonPortableResourcesError{
			TargetLabel: "aws-us-east-1",
			Resources: []string{
				"production/AWS::S3::Bucket/my-bucket",
				"production/AWS::RDS::DBInstance/my-db",
			},
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 10. InvalidQueryError ─────────────────────────────────────────────────────

func TestRender_InvalidQuery_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.InvalidQueryError]{
		ErrorType: apimodel.InvalidQuery,
		Data: apimodel.InvalidQueryError{
			Reason: "field 'foo' is not a valid search field",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 11. StackReferenceNotFoundError ───────────────────────────────────────────

func TestRender_StackNotFound_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.StackReferenceNotFoundError]{
		ErrorType: apimodel.StackReferenceNotFound,
		Data: apimodel.StackReferenceNotFoundError{
			StackLabel: "my-stack",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 12. TargetReferenceNotFoundError ──────────────────────────────────────────

func TestRender_TargetNotFound_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.TargetReferenceNotFoundError]{
		ErrorType: apimodel.TargetReferenceNotFound,
		Data: apimodel.TargetReferenceNotFoundError{
			TargetLabel: "aws-us-west-2",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 13. StackDeletedDuringApplyError ──────────────────────────────────────────

func TestRender_StackDeletedConcurrent_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.StackDeletedDuringApplyError]{
		ErrorType: apimodel.StackDeletedDuringApply,
		Data: apimodel.StackDeletedDuringApplyError{
			StackLabel: "production",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 14. PluginNotFoundError ───────────────────────────────────────────────────

func TestRender_PluginNotFound_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.PluginNotFoundError]{
		ErrorType: apimodel.PluginNotFound,
		Data: apimodel.PluginNotFoundError{
			Name: "my-custom-plugin",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 15. PluginDependencyConflictError ─────────────────────────────────────────

func TestRender_PluginDependencyConflict_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.PluginDependencyConflictError]{
		ErrorType: apimodel.PluginDependencyConflict,
		Data: apimodel.PluginDependencyConflictError{
			Message: "plugin 'aws' requires version >=1.2.0 but 1.0.0 is installed",
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}

// ── 16. Plain error fall-through ──────────────────────────────────────────────

// An unrecognized error type must fall back to plain rendering: Render returns
// the error's message and a nil error, so callers surface the actual error
// instead of double-wrapping it as "error rendering error message: ...".
func TestRender_PlainError_FallThrough(t *testing.T) {
	plain := errors.New("something went wrong")
	out, rerr := Render(plain)
	require.NoError(t, rerr)
	require.Equal(t, plain.Error(), out)
}

// ── 17. TargetAlreadyExists — nil/empty config sub-cases ─────────────────────

func TestRender_TargetAlreadyExists_Config_EmptyConfig_Golden(t *testing.T) {
	err := &apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]{
		ErrorType: apimodel.TargetAlreadyExists,
		Data: apimodel.TargetAlreadyExistsError{
			TargetLabel:    "aws-prod",
			MismatchType:   "config",
			ExistingConfig: nil,
			FormaConfig:    json.RawMessage(`{}`),
		},
	}
	out, rerr := Render(err)
	require.NoError(t, rerr)
	tuitest.RequireGolden(t, []byte(out))
}
