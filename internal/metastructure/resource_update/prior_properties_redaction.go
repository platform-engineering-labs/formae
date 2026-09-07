// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae/internal/metastructure/transformations"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// opaqueRedactedSentinel replaces a schema-opaque field's prior value wherever it
// is about to leave the agent as PriorProperties (diff/context for a plugin
// Update, never a value being written). Unlike a $hashed envelope this sentinel
// is not digest-shaped and carries no material a plugin could mistake for, or
// reconstruct into, a real secret write.
func opaqueRedactedSentinel() map[string]any {
	return map[string]any{"$opaque": "redacted"}
}

// StripOpaqueFieldsForPriorProperties replaces every opaque field present in
// props with opaqueRedactedSentinel.
//
// PriorProperties on a plugin Update is prior/diff CONTEXT, not a value being
// written — but the conversion that feeds it (convertResourceForPluginRead) is
// deliberately unguarded, because the pre-update synchronize() Read only merges
// in what the plugin's Read actually returns: a non-enriching (writeOnly)
// secret leaves the stored $hashed envelope untouched, and once converted to
// plugin format that envelope's marker is stripped, leaving a bare digest with
// nothing to distinguish it from a real value. Stripping the field here — after
// conversion, regardless of whether it happens to be enveloped or already a
// bare scalar/digest — closes that: no digest (or plaintext) for an opaque
// field ever leaves via PriorProperties.
//
// Opacity is resolved exactly as the at-rest hashing resolves it, so the two
// cannot disagree about what is secret: the schema-declared opaque fields UNION
// the agent-side known-opaque table keyed on resource type, and the union of
// BOTH schemas — a hint removed or renamed between prior and desired would
// otherwise expose a value that was opaque when it was stored. Nested (dotted)
// hint names match at every level via the shared opaque-path walker, so a
// secret inside a sub-resource is covered as well as a top-level one.
//
// Inline $visibility=Opaque envelopes carrying no hint are deliberately NOT
// redacted here — those reach a plugin only via DesiredState, which stays
// behind the ConvertToPluginFormat guard.
func StripOpaqueFieldsForPriorProperties(
	props json.RawMessage,
	priorSchema, desiredSchema pkgmodel.Schema,
	resourceType string,
) (json.RawMessage, []transformations.Diagnostic, error) {
	opaqueFields := transformations.OpaqueFields(desiredSchema, resourceType)
	for field := range transformations.OpaqueFields(priorSchema, resourceType) {
		opaqueFields[field] = true
	}
	if len(props) == 0 || len(opaqueFields) == 0 {
		return props, nil, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(props, &decoded); err != nil {
		return nil, nil, fmt.Errorf("failed to decode prior properties for opaque redaction: %w", err)
	}

	walk := &transformations.OpaqueWalk{
		Opaque: opaqueFields,
		Match:  func(any) (any, bool) { return opaqueRedactedSentinel(), true },
	}
	walk.WalkProperties(decoded)

	out, err := json.Marshal(decoded)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode redacted prior properties: %w", err)
	}
	return out, walk.Diagnostics(), nil
}
