// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// opaqueRedactedSentinel replaces a schema-opaque field's prior value wherever it
// is about to leave the agent as PriorProperties (diff/context for a plugin
// Update, never a value being written). Unlike a $hashed envelope this sentinel
// is not digest-shaped and carries no material a plugin could mistake for, or
// reconstruct into, a real secret write.
var opaqueRedactedSentinel = map[string]any{"$opaque": "redacted"}

// StripOpaqueFieldsForPriorProperties replaces every schema-opaque top-level
// field present in props with opaqueRedactedSentinel.
//
// PriorProperties on a plugin Update is prior/diff CONTEXT, not a value being
// written — but the conversion that feeds it (convertResourceForPluginRead) is
// deliberately unguarded, because the pre-update synchronize() Read only merges
// in what the plugin's Read actually returns: a non-enriching (writeOnly)
// secret leaves the stored $hashed envelope untouched, and once converted to
// plugin format that envelope's marker is stripped, leaving a bare digest with
// nothing to distinguish it from a real value. Stripping the field here — after
// conversion, regardless of whether it happens to be enveloped or already a
// bare scalar/digest — closes that: no digest (or plaintext) for a
// schema-opaque field ever leaves via PriorProperties.
//
// Only top-level schema-declared opaque fields (schema.Opaque()) are touched.
// Nested/inline $visibility=Opaque envelopes are not addressed here — they
// reach the plugin only via DesiredState, which stays behind the
// ConvertToPluginFormat guard.
func StripOpaqueFieldsForPriorProperties(props json.RawMessage, opaqueFields []string) (json.RawMessage, error) {
	if len(props) == 0 || len(opaqueFields) == 0 {
		return props, nil
	}

	out := string(props)
	for _, field := range opaqueFields {
		if !gjson.Get(out, field).Exists() {
			continue
		}
		updated, err := sjson.Set(out, field, opaqueRedactedSentinel)
		if err != nil {
			return nil, fmt.Errorf("failed to strip opaque field %q from prior properties: %w", field, err)
		}
		out = updated
	}

	return json.RawMessage(out), nil
}
