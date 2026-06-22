// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package patch

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/metastructure/resolver"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/jsonpatch"
)

// ----------------------------------------------------------------------------
// CreateOnly × opaque interaction in the patch generator.
//
// These tests call generatePatch directly, the layer where replace-vs-update
// routing is decided. generatePatch returns TWO patches:
//
//   - patchDoc        : the mutable ops, sent to the plugin as an Update.
//   - createOnlyPatch : ops on createOnly fields → the caller plans a
//                       destroy+create (REPLACE). Non-empty ⇒ a replacement
//                       is required for this resource.
//
// Routing is decided by extractCreateOnlyFields / filterCreateOnlyFields keyed
// on schema.CreateOnly(); the writeOnly∩createOnly stripping that guards against
// phantom replaces also runs at this layer.
//
// Opaque input shape: by the time generatePatch runs, ConvertToPluginFormat has
// unwrapped the {$strategy,$visibility,$value} envelope on both sides. The
// existing/document side carries the at-rest opaque value, which is the SHA-256
// hash (PersistValueTransformer stores opaque values hashed, reproduced here
// with pkgmodel.ComputeValueHash), while the desired/patch side carries the
// cleartext the user supplied. So for an opaque field generatePatch diffs
// stored-hash vs desired-cleartext, which is the input shape these tests feed in.
// ----------------------------------------------------------------------------

// hashRe64 matches a bare SHA-256 hex digest (the at-rest opaque value form).
var hashRe64 = regexp.MustCompile(`[0-9a-f]{64}`)

// secretFieldHash is the at-rest stored form of an opaque value V: the SHA-256
// hash of the cleartext, exactly what PersistValueTransformer writes.
func secretFieldHash(cleartext string) string {
	return pkgmodel.ComputeValueHash(cleartext)
}

// dumpPatches logs both returned patches and flags any 64-hex hash leak.
func dumpPatches(t *testing.T, tag string, patchDoc, createOnlyPatch json.RawMessage) {
	t.Helper()
	mutable := "<nil>"
	if patchDoc != nil {
		mutable = string(patchDoc)
	}
	createOnly := "<nil>"
	if createOnlyPatch != nil {
		createOnly = string(createOnlyPatch)
	}
	t.Logf("[%s] mutable patchDoc     = %s", tag, mutable)
	t.Logf("[%s] createOnlyPatch      = %s", tag, createOnly)
	t.Logf("[%s] mutable touches /SecretString  : %v", tag, regexp.MustCompile(`/SecretString`).MatchString(mutable))
	t.Logf("[%s] createOnly touches /SecretString: %v", tag, regexp.MustCompile(`/SecretString`).MatchString(createOnly))
	if h := hashRe64.FindString(mutable); h != "" {
		t.Logf("[%s] *** HASH LEAKED into MUTABLE patch: %s", tag, h)
	}
	if h := hashRe64.FindString(createOnly); h != "" {
		t.Logf("[%s] *** HASH LEAKED into CREATEONLY patch: %s", tag, h)
	}
}

func mustOps(t *testing.T, raw json.RawMessage) []jsonpatch.JsonPatchOperation {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var ops []jsonpatch.JsonPatchOperation
	if err := json.Unmarshal(raw, &ops); err != nil {
		t.Fatalf("failed to unmarshal patch %q: %v", string(raw), err)
	}
	return ops
}

// opOnPath reports whether any op targets exactly the given path.
func opOnPath(ops []jsonpatch.JsonPatchOperation, path string) (jsonpatch.JsonPatchOperation, bool) {
	for _, op := range ops {
		if cleanPath(op.Path) == cleanPath(path) {
			return op, true
		}
	}
	return jsonpatch.JsonPatchOperation{}, false
}

// schemaSecretCreateOnly: SecretString is opaque + CreateOnly (NOT writeOnly).
func schemaSecretCreateOnly() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {CreateOnly: true},
		},
	}
}

// schemaSecretMutable: SecretString is opaque, plain mutable (no CreateOnly).
func schemaSecretMutable() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints:      map[string]pkgmodel.FieldHint{},
	}
}

// schemaSecretCreateOnlyWriteOnly: SecretString is opaque + CreateOnly + WriteOnly.
func schemaSecretCreateOnlyWriteOnly() pkgmodel.Schema {
	return pkgmodel.Schema{
		Identifier: "Name",
		Fields:     []string{"Name", "Description", "SecretString"},
		Hints: map[string]pkgmodel.FieldHint{
			"SecretString": {CreateOnly: true, WriteOnly: true},
		},
	}
}

// -------------------------------------------------------------------------
// CASE 1: Changed opaque CreateOnly, sibling unchanged.
// existing: stored HASH(V); desired: cleartext V2 (V != V2).
// EXPECT: the secret op lands in the createOnlyPatch (→ REPLACE), NOT the
// mutable patch. Confirm routing + what value the createOnly op carries.
// -------------------------------------------------------------------------
func TestCharacterize_CreateOnly_ChangedOpaque_SiblingUnchanged(t *testing.T) {
	document := []byte(`{"Name":"my-secret","Description":"same","SecretString":"` + secretFieldHash("value-V") + `"}`)
	patch := []byte(`{"Name":"my-secret","Description":"same","SecretString":"value-V2"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schemaSecretCreateOnly(), pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatalf("generatePatch error: %v", err)
	}
	dumpPatches(t, "C1-changed-createonly", patchDoc, createOnlyPatch)

	mutable := mustOps(t, patchDoc)
	createOnly := mustOps(t, createOnlyPatch)
	if _, inMutable := opOnPath(mutable, "/SecretString"); inMutable {
		t.Logf("[C1] OBSERVED: /SecretString present in MUTABLE patch (would be sent as Update — wrong for createOnly)")
	}
	if op, inCreateOnly := opOnPath(createOnly, "/SecretString"); inCreateOnly {
		t.Logf("[C1] OBSERVED: /SecretString present in CREATEONLY patch (→ REPLACE). op=%s value=%v", op.Operation, op.Value)
	} else {
		t.Logf("[C1] OBSERVED: /SecretString ABSENT from createOnly patch")
	}
}

// -------------------------------------------------------------------------
// CASE 2: Unchanged opaque CreateOnly + sibling change. existing: stored
// HASH(V); desired: cleartext V (same secret). A sibling mutable field
// (Description) changes A→B. Because the document carries HASH(V) and desired
// carries cleartext V, the byte-level diff sees a change; this observes whether
// the secret then appears in the createOnly patch (a phantom replace of an
// unchanged secret).
// -------------------------------------------------------------------------
func TestCharacterize_CreateOnly_UnchangedOpaque_SiblingChange(t *testing.T) {
	document := []byte(`{"Name":"my-secret","Description":"A","SecretString":"` + secretFieldHash("value-V") + `"}`)
	patch := []byte(`{"Name":"my-secret","Description":"B","SecretString":"value-V"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schemaSecretCreateOnly(), pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatalf("generatePatch error: %v", err)
	}
	dumpPatches(t, "C2-unchanged-createonly-sibling", patchDoc, createOnlyPatch)

	mutable := mustOps(t, patchDoc)
	createOnly := mustOps(t, createOnlyPatch)

	if _, descMutable := opOnPath(mutable, "/Description"); descMutable {
		t.Logf("[C2] OBSERVED: /Description present in MUTABLE patch (expected — the real change)")
	}
	if op, inCreateOnly := opOnPath(createOnly, "/SecretString"); inCreateOnly {
		t.Logf("[C2] OBSERVED: DESTRUCTIVE PHANTOM REPLACE — /SecretString in CREATEONLY patch despite UNCHANGED secret. op=%s value=%v", op.Operation, op.Value)
	} else {
		t.Logf("[C2] OBSERVED: no /SecretString op in createOnly patch — no phantom replace")
	}
	if len(createOnly) == 0 {
		t.Logf("[C2] OBSERVED: createOnly patch is EMPTY → no replacement planned")
	} else {
		t.Logf("[C2] OBSERVED: createOnly patch NON-EMPTY → caller plans a REPLACE (destroy+create)")
	}
}

// -------------------------------------------------------------------------
// CASE 3: Changed opaque MUTABLE (non-createOnly). existing: stored HASH(V);
// desired: cleartext V2. Expect the secret op in the MUTABLE patch (→ Update),
// createOnly patch empty.
// -------------------------------------------------------------------------
func TestCharacterize_Mutable_ChangedOpaque(t *testing.T) {
	document := []byte(`{"Name":"my-secret","Description":"same","SecretString":"` + secretFieldHash("value-V") + `"}`)
	patch := []byte(`{"Name":"my-secret","Description":"same","SecretString":"value-V2"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schemaSecretMutable(), pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatalf("generatePatch error: %v", err)
	}
	dumpPatches(t, "C3-changed-mutable", patchDoc, createOnlyPatch)

	mutable := mustOps(t, patchDoc)
	if op, inMutable := opOnPath(mutable, "/SecretString"); inMutable {
		t.Logf("[C3] OBSERVED: /SecretString in MUTABLE patch (→ Update). op=%s value=%v", op.Operation, op.Value)
	}
	if len(createOnlyPatch) == 0 {
		t.Logf("[C3] OBSERVED: createOnly patch EMPTY (expected for non-createOnly field)")
	}
}

// -------------------------------------------------------------------------
// CASE 4: createOnly + writeOnly + opaque, secret UNCHANGED + sibling change.
// existing: stored HASH(V); desired: cleartext V (same). Description A→B.
// Observes whether the intersectFields(WriteOnly,CreateOnly) stripping prevents
// a replace here (no /SecretString op in the createOnly patch). That stripping
// covers writeOnly∩createOnly but not plain opaque.
// -------------------------------------------------------------------------
func TestCharacterize_CreateOnlyWriteOnly_UnchangedOpaque_SiblingChange(t *testing.T) {
	document := []byte(`{"Name":"my-secret","Description":"A","SecretString":"` + secretFieldHash("value-V") + `"}`)
	patch := []byte(`{"Name":"my-secret","Description":"B","SecretString":"value-V"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schemaSecretCreateOnlyWriteOnly(), pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatalf("generatePatch error: %v", err)
	}
	dumpPatches(t, "C4-createonly-writeonly", patchDoc, createOnlyPatch)

	createOnly := mustOps(t, createOnlyPatch)
	if _, inCreateOnly := opOnPath(createOnly, "/SecretString"); inCreateOnly {
		t.Logf("[C4] OBSERVED: /SecretString STILL in createOnly patch → NOT protected (phantom replace)")
	} else {
		t.Logf("[C4] OBSERVED: /SecretString stripped → PROTECTED (no phantom replace)")
	}
	if len(createOnly) == 0 {
		t.Logf("[C4] OBSERVED: createOnly patch EMPTY → no replacement planned (protected)")
	}
}

// -------------------------------------------------------------------------
// CASE 5: changed opaque CreateOnly + writeOnly + sibling change.
// existing: stored HASH(V); desired: cleartext V2 (different). Description A→B.
// When the writeOnly+createOnly secret actually changes, observes whether the
// stripping also suppresses a legitimate replace (no /SecretString op in the
// createOnly patch despite a real change).
// -------------------------------------------------------------------------
func TestCharacterize_CreateOnlyWriteOnly_ChangedOpaque_SiblingChange(t *testing.T) {
	document := []byte(`{"Name":"my-secret","Description":"A","SecretString":"` + secretFieldHash("value-V") + `"}`)
	patch := []byte(`{"Name":"my-secret","Description":"B","SecretString":"value-V2"}`)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, resolver.NewResolvableProperties(), schemaSecretCreateOnlyWriteOnly(), pkgmodel.FormaApplyModeReconcile)
	if err != nil {
		t.Fatalf("generatePatch error: %v", err)
	}
	dumpPatches(t, "C5-createonly-writeonly-changed", patchDoc, createOnlyPatch)

	createOnly := mustOps(t, createOnlyPatch)
	if _, inCreateOnly := opOnPath(createOnly, "/SecretString"); inCreateOnly {
		t.Logf("[C5] OBSERVED: changed writeOnly+createOnly secret → /SecretString in createOnly patch (REPLACE)")
	} else {
		t.Logf("[C5] OBSERVED: changed writeOnly+createOnly secret → NO /SecretString op (stripping also suppresses the legitimate replace)")
	}
}
