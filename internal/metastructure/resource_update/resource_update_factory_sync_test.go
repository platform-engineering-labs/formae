// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A sync-driven ResourceUpdate (NewResourceUpdateForSyncWithFilter) only ever drives a Read: it
// never writes PriorState/PreviousProperties to the cloud. PriorState is deliberately built from
// resolver.ConvertExistingStateForRead, which strips a $hashed opaque envelope down to its bare
// digest for that Read-context use.
//
// PreviousProperties must NOT inherit that stripped bare digest: it gets persisted as the
// FormaCommand's record of prior state, and a bare digest carries no $hashed marker, so
// hashOpaqueField would treat it as plaintext and re-hash it (hash-of-hash) on the next boot
// backfill. PreviousProperties must keep the original $hashed envelope intact.
func TestNewResourceUpdateForSyncWithFilter_PreviousPropertiesKeepsHashedEnvelope(t *testing.T) {
	target := pkgmodel.Target{Label: "test-target", Namespace: "aws", Config: json.RawMessage(`{}`)}

	existing := pkgmodel.Resource{
		Ksuid:    "resourceksuid1",
		Label:    "my-secret",
		Type:     "AWS::SecretsManager::Secret",
		Stack:    "default",
		Target:   "test-target",
		NativeID: "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-secret",
		Managed:  true,
		Properties: json.RawMessage(`{
			"Name": "my-secret",
			"SecretString": {"$value": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", "$visibility": "Opaque", "$hashed": true}
		}`),
	}

	update, err := NewResourceUpdateForSyncWithFilter(existing, target, FormaCommandSourceSynchronize)
	require.NoError(t, err)

	// PriorState (the Read context) is allowed to be stripped to a bare digest.
	var priorProps map[string]any
	require.NoError(t, json.Unmarshal(update.PriorState.Properties, &priorProps))
	if secretString, ok := priorProps["SecretString"].(map[string]any); ok {
		// If it stayed a map, it must at least still be marked hashed.
		assert.Equal(t, true, secretString["$hashed"])
	}

	// PreviousProperties must retain the ORIGINAL $hashed envelope, not a bare digest.
	var prevProps map[string]any
	require.NoError(t, json.Unmarshal(update.PreviousProperties, &prevProps))
	secretString, ok := prevProps["SecretString"].(map[string]any)
	require.True(t, ok, "PreviousProperties.SecretString must still be an envelope map, not a bare digest")
	assert.Equal(t, true, secretString["$hashed"], "PreviousProperties must keep the $hashed marker so boot backfill stays idempotent")
	assert.Equal(t, "Opaque", secretString["$visibility"])
	assert.Equal(t, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", secretString["$value"])
}
