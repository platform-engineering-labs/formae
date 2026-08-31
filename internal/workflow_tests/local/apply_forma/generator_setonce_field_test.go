// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package workflow_tests_local

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// setOnceSeedSecret is a secret whose opaque value is written once and never
// updated afterwards: the shape a hand-managed credential takes before anyone
// puts a generator behind it.
func setOnceSeedSecret(stack, label, value string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:  label,
		Type:   "FakeAWS::SecretsManager::Secret",
		Stack:  stack,
		Target: "test-target",
		Schema: secretSchema(),
		Properties: json.RawMessage(`{
			"Name": "` + label + `",
			"SecretString": {"$value":"` + value + `","$visibility":"Opaque","$strategy":"SetOnce"}
		}`),
	}
}

// storedStrategy returns the value strategy a stored resource carries at one
// property path.
func storedStrategy(t *testing.T, m *metastructure.Metastructure, stack, label, path string) string {
	t.Helper()
	resources, err := m.Datastore.LoadResourcesByStack(stack)
	require.NoError(t, err)
	for i := range resources {
		if resources[i].Label == label {
			return gjson.GetBytes(resources[i].Properties, path).Get("$strategy").String()
		}
	}
	t.Fatalf("no stored resource %q in stack %q", label, stack)
	return ""
}

// Putting a generator behind a credential that something downstream holds
// setOnce is refused, and refused before anything is drawn.
//
// A consumer's stored envelope inherits the value strategy of the value it
// resolved from, so a consumer of a setOnce secret is itself setOnce from its
// first apply onwards. Binding that secret to a generator makes the two
// contradictory: the secret takes each newly drawn value and the consumer
// keeps the first one it ever saw. Nothing fails while that happens — the
// secret rotates and the consumer is not even dispatched — and formae keeps a
// digest of a drawn value rather than the value, so no later apply can bring
// the two level again.
//
// The middle apply is half the test. The generator draws for a destination of
// its own while a setOnce credential graph sits beside it in the same stack,
// and that apply must succeed: the refusal is scoped to what the generator
// reaches, not to the stacks the command carries. A stack-wide reading of the
// same rule would turn replacing one seed into a migration of every credential
// around it.
func TestApplyForma_GeneratorReachingASetOnceConsumer_IsRefusedWithoutDrawing(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var consumerUpdates atomic.Int32
		var consumerProps atomic.Value

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(
			t, secretConsumerOverrides(&consumerUpdates, &consumerProps), cfg)
		defer def()
		require.NoError(t, err)

		stack := "test-stack-" + util.NewID()
		apply := func(generators []json.RawMessage, resources ...pkgmodel.Resource) error {
			_, applyErr := m.ApplyForma(&pkgmodel.Forma{
				Stacks:     []pkgmodel.Stack{{Label: stack}},
				Targets:    []pkgmodel.Target{{Label: "test-target", Namespace: "test-namespace"}},
				Generators: generators,
				Resources:  resources,
			}, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
			return applyErr
		}
		generator := func() []json.RawMessage {
			return []json.RawMessage{passwordGeneratorFor(t, "db-password", stack)}
		}

		// A hand-managed credential and a consumer of it. The consumer is
		// setOnce from here on, by inheritance.
		seed := setOnceSeedSecret(stack, "seed", "seed-v1")
		consumer := secretConsumer(stack, "seed")

		require.NoError(t, apply(nil, seed, consumer))
		waitForApplyComplete(t, m)
		require.Equal(t, "SetOnce", storedStrategy(t, m, stack, "my-bucket", "DbPassword"),
			"the consumer must have inherited the seed's strategy, or this test proves nothing")

		// A destination of the generator's own, in the same stack as the
		// setOnce graph and reaching none of it. FakeAWS gives every secret it
		// creates the same native id, so the two secrets are created by
		// separate commands.
		vault := genBoundSecret(stack, "vault", "db-password", "value")
		require.NoError(t, apply(generator(), seed, consumer, vault),
			"a setOnce credential graph the generator does not reach must not stop it drawing")
		waitForApplyComplete(t, m)

		drawn, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		require.NotEmpty(t, drawn.GenerationID, "the generator must have drawn for its own destination")

		// Now put the generator behind the seed, which makes the setOnce
		// consumer a node of the generator's graph.
		err = apply(generator(), genBoundSecret(stack, "seed", "db-password", "value"), consumer, vault)

		var refusal apimodel.FormaGeneratorBoundToSetOnceFieldError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, []apimodel.SetOnceGeneratorField{{
			GeneratorLabel: "db-password",
			GeneratorStack: stack,
			Stack:          stack,
			Label:          "my-bucket",
			Type:           "FakeAWS::S3::Bucket",
			Field:          "DbPassword",
		}}, refusal.Fields)

		refused, err := m.Datastore.GetGeneratorIdentity("db-password", stack)
		require.NoError(t, err)
		assert.Equal(t, drawn.GenerationID, refused.GenerationID,
			"a refused command must not have drawn: the generation the generator held is the one it still holds")
	})
}
