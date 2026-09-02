// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Re-applying an identical forma whose target config references a secret must
// plan no target update: the stored config's reference metadata is stripped
// symmetrically before comparison, so a secret-sourced config is idempotent.
func TestApplyForma_SecretSourcedTargetConfig_IdempotentReapply(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		secretProps := `{"BucketName":"vault","SecretString":"hunter2"}`
		overrides := &plugin.ResourcePluginOverrides{
			Create: func(request *resource.CreateRequest) (*resource.CreateResult, error) {
				return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
					Operation:          resource.OperationCreate,
					OperationStatus:    resource.OperationStatusSuccess,
					RequestID:          "1234",
					NativeID:           "native-" + request.Label,
					ResourceProperties: request.Properties,
				}}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: request.ResourceType,
					Properties:   secretProps,
				}, nil
			},
		}

		m, def, err := test_helpers.NewTestMetastructure(t, overrides)
		defer def()
		require.NoError(t, err)

		secretKsuid := "2MiD2rA1SJbLMGZgTL0hCxjkjjr"
		buildForma := func() *pkgmodel.Forma {
			return &pkgmodel.Forma{
				Stacks: []pkgmodel.Stack{{Label: "infra"}},
				Resources: []pkgmodel.Resource{{
					Label: "vault", Type: "FakeAWS::S3::Bucket", Stack: "infra", Target: "provider",
					Managed: true, Ksuid: secretKsuid,
					Schema: pkgmodel.Schema{
						Identifier: "BucketName", Portable: true,
						Fields: []string{"BucketName", "SecretString"},
						Hints:  map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}},
					},
					Properties: json.RawMessage(`{"BucketName":"vault","SecretString":"hunter2"}`),
				}},
				Targets: []pkgmodel.Target{
					{Label: "provider", Namespace: "FakeAWS", Config: json.RawMessage(`{"region":"us-east-1"}`)},
					{Label: "consumer", Namespace: "FakeAWS", Config: json.RawMessage(fmt.Sprintf(`{
						"token": {"$ref": "formae://%s#/SecretString"}
					}`, secretKsuid))},
				},
			}
		}

		_, err = m.ApplyForma(buildForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			fas, err := m.Datastore.LoadFormaCommands()
			require.NoError(t, err)
			incomplete, err := m.Datastore.LoadIncompleteFormaCommands()
			require.NoError(t, err)
			return len(fas) == 1 && len(incomplete) == 0
		}, 10*time.Second, 100*time.Millisecond)

		// Identical re-apply: nothing changed, so no command may be created
		// and no target update planned.
		resp, err := m.ApplyForma(buildForma(),
			&config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: false},
			"test-client-id", "", "")
		require.NoError(t, err)
		assert.False(t, resp.Simulation.ChangesRequired,
			"an identical re-apply of a secret-sourced target config must plan nothing")

		fas, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		assert.Len(t, fas, 1, "no second command may be created")
	})
}
