# `$applied` Provenance for Reference-Fed Properties — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop reference-fed properties from perpetually diffing against provider-echoed value forms (and silently planning delete+create replacements) by recording the value formae actually wrote (`$applied`) in each resolvable envelope and comparing within one provenance domain at diff time.

**Architecture:** Stored `$ref`/`$res` envelopes gain an `$applied` field, written only on the echo-merge of a successful formae-originated Create/Update and invalidated when sync absorbs a genuinely different echo. At diff time, `resolveRefs` compares the fresh reference resolution against `$applied` (written vs written); when equal it substitutes the stored echo so jsonpatch sees zero diff. Legacy rows (no `$applied`) keep today's behavior and converge through one corrective write that backfills `$applied`. Canonical design: Linear Document "Spec: $applied provenance for reference-fed properties" on the reference-echo diff issue.

**Tech Stack:** Go 1.26, gjson/sjson, testify. Unit tests run with `go test -tags=unit`.

## Global Constraints

- Every new file starts with the FSL header used repo-wide:
  ```
  // © 2025 Platform Engineering Labs Inc.
  //
  // SPDX-License-Identifier: FSL-1.1-ALv2
  ```
- NEVER put ticket numbers (PLA-xxx), tracker links, or bug-history narration in file names, symbols, comments, string literals, test names, or commit messages. Comments describe behavior only.
- Commit messages: conventional style matching `git log` (`feat(...):`, `fix(...):`), no AI attribution footers of any kind.
- Working directory: `~/dev/pel/formae/.worktrees/pla-510-applied-provenance` (branch `jeroensoeters/pla-510-applied-provenance`).
- Run tests as `go test -tags=unit ./internal/metastructure/patch/... ./internal/metastructure/resource_update/...` unless a step says otherwise.
- Opaque envelopes (`"$visibility":"Opaque"`) are FULLY exempt from every new behavior in this plan: never write `$applied` on them, never apply provenance diff rules to them.

---

### Task 1: Thread stored envelopes into GeneratePatch (no behavior change)

**Files:**
- Modify: `internal/metastructure/patch/patch_document.go` (`GeneratePatch` :32, `generatePatch` :68, `flattenAndResolveRefs` :1074, `resolveRefs` :892)
- Modify: `internal/metastructure/resource_update/resource_update_factory.go` (:88 call)
- Modify: `internal/metastructure/resource_update/resource_update.go` (`regeneratePatchDocument`, the `patch.GeneratePatch` call near :170)
- Test: `internal/metastructure/patch/patch_document_test.go` (existing tests updated mechanically)

**Interfaces:**
- Produces: `GeneratePatch(document, patch, storedEnvelopes []byte, properties resolver.ResolvableProperties, schema pkgmodel.Schema, mode pkgmodel.FormaApplyMode) (json.RawMessage, json.RawMessage, error)` — `storedEnvelopes` is the UNCONVERTED existing-state properties JSON (envelopes intact); `nil` means "no provenance data, behave exactly as before".
- Produces: `resolveRefs(current, mod, stored map[string]any, resolvableProperties resolver.ResolvableProperties) error` — `stored` may be nil.

- [ ] **Step 1: Change the signatures.** Add `storedEnvelopes []byte` as the third parameter of `GeneratePatch` and `generatePatch`, and pass it to `flattenAndResolveRefs(document, patch, storedEnvelopes, resolvableProperties)`. In `flattenAndResolveRefs`, unmarshal it into `var stored map[string]any` when non-nil (on unmarshal error return the error), and pass it to `resolveRefs(current, mod, stored, resolvableProperties)`. In `resolveRefs`, add the `stored map[string]any` parameter and thread it through both recursion sites exactly like `current` is threaded today: for the nested-map recursion pass `stored[k]` if it is a `map[string]any` else an empty map; for the array-element recursion wrap like `wrappedCurrent`, i.e. `wrappedStored := map[string]any{k: storedElem}` where `storedElem` is `storedArr[i]` if a `[]any` counterpart exists at `stored[k]` and is long enough (Task 3 replaces this index pairing for ref elements; plain index threading is fine for this task). Do not add any comparison logic yet.

- [ ] **Step 2: Fix all callers.** `grep -rn "GeneratePatch(\|generatePatch(\|flattenAndResolveRefs(\|resolveRefs(" internal/ --include="*.go"`. Production callers: in `resource_update_factory.go` pass `existingForPatch` (the variable already in scope from `SuppressUnchangedOpaqueValues`, BEFORE `ConvertExistingStateForComparison`) as `storedEnvelopes`; same in `regeneratePatchDocument` in `resource_update.go`. Every existing test caller passes `nil` for the new parameter.

- [ ] **Step 3: Run the full test suite to prove no behavior change.**

Run: `go test -tags=unit ./internal/metastructure/... 2>&1 | tail -20`
Expected: all packages `ok`.

- [ ] **Step 4: Commit.**

```bash
git add -A internal/
git commit -m "refactor(patch): thread unconverted stored envelopes into patch generation"
```

---

### Task 2: Provenance comparison for scalar `$ref` nodes

**Files:**
- Modify: `internal/metastructure/patch/patch_document.go` (`resolveRefs`; new helpers below it)
- Test: `internal/metastructure/patch/patch_document_test.go`

**Interfaces:**
- Consumes: `resolveRefs(current, mod, stored, resolvableProperties)` from Task 1; `normalizeResolvedValue(resolved string, current any) any` (exists, :872).
- Produces: `storedRefCounterpart(storedNode any, modVal map[string]any) map[string]any` and `appliedMatches(fresh string, applied any) bool` (both used again by Task 3).

- [ ] **Step 1: Write the failing tests.** Append to `patch_document_test.go` (mirror the idiom of `TestGeneratePatch_ShouldResolveRefs` at :453; `generatePatch` now takes the stored-envelopes parameter):

```go
// A reference whose fresh resolution equals the value the last write sent
// ($applied) is unchanged intent: the desired side flattens to the stored
// echo and the diff is empty, even though echo and resolution are two
// spellings of one identity (ARN vs bare ID).
func TestGeneratePatch_RefResolutionMatchesApplied_NoPatch(t *testing.T) {
	ksuid := util.NewID()
	arn := "arn:aws:kms:us-east-1:111122223333:key/47110862-aaaa"
	document := []byte(`{"TargetKeyId": "47110862-aaaa"}`)
	stored := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn", "$value": "47110862-aaaa", "$applied": %q}}`, ksuid, arn)
	patch := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn"}}`, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"TargetKeyId"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "Arn", arn)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	assert.Nil(t, patchDoc, "an unchanged reference must reconcile to a no-op regardless of echo form")
}

// A createOnly reference whose fresh resolution equals $applied must not
// plan a replacement.
func TestGeneratePatch_CreateOnlyRefMatchesApplied_NoReplacement(t *testing.T) {
	ksuid := util.NewID()
	arn := "arn:aws:lambda:us-east-1:111122223333:function:fn"
	document := []byte(`{"TargetFunctionArn": "fn", "Cors": "old"}`)
	stored := fmt.Appendf(nil, `{"TargetFunctionArn": {"$ref": "formae://%s#/Arn", "$value": "fn", "$applied": %q}, "Cors": "old"}`, ksuid, arn)
	patch := fmt.Appendf(nil, `{"TargetFunctionArn": {"$ref": "formae://%s#/Arn"}, "Cors": "new"}`, ksuid)
	schema := pkgmodel.Schema{
		Fields: []string{"TargetFunctionArn", "Cors"},
		Hints:  map[string]pkgmodel.FieldHint{"TargetFunctionArn": {CreateOnly: true}},
	}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "Arn", arn)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch, "unchanged createOnly reference must not trigger replacement")
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, "/Cors", ops[0].Path)
}

// A fresh resolution that differs from $applied is a genuine repoint (the
// referenced resource changed): the update is planned with the fresh value.
func TestGeneratePatch_RefResolutionDiffersFromApplied_PlansUpdate(t *testing.T) {
	ksuid := util.NewID()
	oldArn := "arn:aws:kms:us-east-1:111122223333:key/47110862-aaaa"
	newArn := "arn:aws:kms:us-east-1:111122223333:key/99887766-bbbb"
	document := []byte(`{"TargetKeyId": "47110862-aaaa"}`)
	stored := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn", "$value": "47110862-aaaa", "$applied": %q}}`, ksuid, oldArn)
	patch := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn"}}`, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"TargetKeyId"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "Arn", newArn)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1)
	assert.Equal(t, newArn, ops[0].Value)
}

// An unresolvable reference (no fresh resolution, no cached $value) with an
// $applied-carrying stored counterpart at the same URI flattens to the
// stored echo instead of the empty string.
func TestGeneratePatch_UnresolvableRefWithApplied_UsesStoredEcho(t *testing.T) {
	ksuid := util.NewID()
	document := []byte(`{"TargetKeyId": "47110862-aaaa"}`)
	stored := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn", "$value": "47110862-aaaa", "$applied": "arn:aws:kms:us-east-1:111122223333:key/47110862-aaaa"}}`, ksuid)
	patch := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn"}}`, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"TargetKeyId"}}

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, stored, resolver.NewResolvableProperties(), schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	assert.Nil(t, patchDoc)
}

// A legacy stored row without $applied keeps the pre-provenance behavior:
// fresh resolution vs echo, planning the corrective write that backfills.
func TestGeneratePatch_LegacyRowWithoutApplied_KeepsFreshVsEcho(t *testing.T) {
	ksuid := util.NewID()
	arn := "arn:aws:kms:us-east-1:111122223333:key/47110862-aaaa"
	document := []byte(`{"TargetKeyId": "47110862-aaaa"}`)
	stored := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn", "$value": "47110862-aaaa"}}`, ksuid)
	patch := fmt.Appendf(nil, `{"TargetKeyId": {"$ref": "formae://%s#/Arn"}}`, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"TargetKeyId"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "Arn", arn)

	patchDoc, _, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "legacy rows converge through one corrective write")
}

// An Opaque envelope is exempt from provenance rules even when it carries
// $applied-shaped data.
func TestGeneratePatch_OpaqueRefIgnoresApplied(t *testing.T) {
	ksuid := util.NewID()
	document := []byte(`{"Secret": "echoed"}`)
	stored := fmt.Appendf(nil, `{"Secret": {"$ref": "formae://%s#/Value", "$value": "echoed", "$applied": "sent", "$visibility": "Opaque"}}`, ksuid)
	patch := fmt.Appendf(nil, `{"Secret": {"$ref": "formae://%s#/Value", "$visibility": "Opaque"}}`, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"Secret"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "Value", "sent")

	patchDoc, _, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	require.Len(t, ops, 1, "opaque envelopes keep pre-provenance diffing")
	assert.Equal(t, "sent", ops[0].Value)
}
```

Check `pkgmodel.FieldHint`'s createOnly field name before using it: `grep -n "CreateOnly" pkg/model/*.go` — if the schema expresses createOnly differently (e.g. a `Schema.CreateOnly()` method backed by a field list), build the schema the way `TestGeneratePatch_NestedCreateOnlyTriggersReplacement` (:397) does and adjust the test accordingly.

- [ ] **Step 2: Run to verify the new tests fail** (compile error on arity is expected to be fixed by using the Task 1 signature; the assertions must fail for behavioral reasons).

Run: `go test -tags=unit -run 'TestGeneratePatch_(RefResolution|CreateOnlyRef|Unresolvable|LegacyRow|OpaqueRef)' ./internal/metastructure/patch/ -v 2>&1 | tail -20`
Expected: FAIL for the `$applied` cases (extra ops / non-nil patchDoc); the legacy and opaque tests may already pass — that is fine, they pin behavior.

- [ ] **Step 3: Implement.** In `patch_document.go`, add below `normalizeResolvedValue`:

```go
// storedRefCounterpart returns the stored envelope that corresponds to a
// desired-side reference node: a map carrying the same $ref URI, not marked
// Opaque on either side. Opaque envelopes are handled by the dedicated
// opaque-suppression path and never participate in provenance comparison.
func storedRefCounterpart(storedNode any, modVal map[string]any) map[string]any {
	storedMap, ok := storedNode.(map[string]any)
	if !ok {
		return nil
	}
	if storedMap["$ref"] != modVal["$ref"] {
		return nil
	}
	if storedMap["$visibility"] == pkgmodel.VisibilityOpaque || modVal["$visibility"] == pkgmodel.VisibilityOpaque {
		return nil
	}
	return storedMap
}

// appliedMatches reports whether a fresh reference resolution equals the
// value the last formae-originated write sent. The fresh value arrives as a
// string (ResolvableProperties stores raw JSON text for structured values);
// $applied holds the JSON-native form that was sent, so structured values
// are compared after parsing the string into the same shape.
func appliedMatches(fresh string, applied any) bool {
	if s, ok := applied.(string); ok {
		return fresh == s
	}
	return reflect.DeepEqual(normalizeResolvedValue(fresh, applied), applied)
}
```

(Add `"reflect"` to imports.) Then rewrite the `hasRef` branch of `resolveRefs` (:896-915) to:

```go
if ref, hasRef := modVal["$ref"]; hasRef {
	uri := pkgmodel.FormaeURI(ref.(string))
	ksuid := uri.KSUID()
	property := uri.PropertyPath()

	counterpart := storedRefCounterpart(stored[k], modVal)

	val, found := resolvableProperties.Get(ksuid, property)
	if found {
		resolved := val
		if jsonPath, ok := modVal["$json"].(string); ok && jsonPath != "" {
			extracted, err := resolver.ExtractJSONPath(val, jsonPath)
			if err != nil {
				return err
			}
			resolved = extracted
		}
		if counterpart != nil {
			if applied, hasApplied := counterpart["$applied"]; hasApplied && appliedMatches(resolved, applied) {
				// The reference still resolves to what the last write sent;
				// flatten to the stored echo so the diff compares within the
				// observed domain and sees no change.
				modVal["$value"] = counterpart["$value"]
			} else {
				modVal["$value"] = normalizeResolvedValue(resolved, current[k])
			}
		} else {
			modVal["$value"] = normalizeResolvedValue(resolved, current[k])
		}
	} else if counterpart != nil {
		if _, hasApplied := counterpart["$applied"]; hasApplied {
			if _, hasVal := modVal["$value"]; !hasVal {
				// No resolution available but a prior write attests this exact
				// reference; treat the gap as transient, not as a change.
				modVal["$value"] = counterpart["$value"]
			}
		}
	}
	// Otherwise keep the $ref as-is for late-binding resolution
	// at execution time (forward references to new resources).
}
```

- [ ] **Step 4: Run the new tests and the full patch package.**

Run: `go test -tags=unit ./internal/metastructure/patch/ -v -run TestGeneratePatch 2>&1 | tail -30` then `go test -tags=unit ./internal/metastructure/...  2>&1 | tail -10`
Expected: all PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/metastructure/patch/
git commit -m "feat(patch): compare reference resolutions against the applied baseline"
```

---

### Task 3: Array-element counterpart matching by reference URI

**Files:**
- Modify: `internal/metastructure/patch/patch_document.go` (`resolveRefs` array branch :925-951)
- Test: `internal/metastructure/patch/patch_document_test.go`

**Interfaces:**
- Consumes: `storedRefCounterpart`, `appliedMatches` from Task 2.
- Produces: `storedRefElementByURI(storedArr []any, uri any) map[string]any` — returns the unique stored array element carrying `$ref == uri`, nil when absent or ambiguous.

- [ ] **Step 1: Write the failing tests.**

```go
// Stored arrays are persisted in plugin-returned order, so an element's
// provenance counterpart is located by its reference URI, not its index.
func TestGeneratePatch_ArrayRefCounterpartMatchedByURI_NotIndex(t *testing.T) {
	ksuid := util.NewID()
	arnA := "arn:aws:sns:us-east-1:111122223333:topic-a"
	arnB := "arn:aws:sns:us-east-1:111122223333:topic-b"
	document := []byte(`{"Topics": ["name-b", "name-a"]}`)
	stored := fmt.Appendf(nil, `{"Topics": [
		{"$ref": "formae://%s#/ArnB", "$value": "name-b", "$applied": %q},
		{"$ref": "formae://%s#/ArnA", "$value": "name-a", "$applied": %q}
	]}`, ksuid, arnB, ksuid, arnA)
	patch := fmt.Appendf(nil, `{"Topics": [
		{"$ref": "formae://%s#/ArnA"},
		{"$ref": "formae://%s#/ArnB"}
	]}`, ksuid, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"Topics"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "ArnA", arnA)
	props.Add(ksuid, "ArnB", arnB)

	patchDoc, createOnlyPatch, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	assert.Empty(t, createOnlyPatch)
	assert.Nil(t, patchDoc, "reordered echoes of unchanged references must reconcile to a no-op")
}

// Duplicate reference URIs in a stored array are ambiguous: fail closed to
// pre-provenance behavior rather than guessing a counterpart.
func TestGeneratePatch_ArrayRefDuplicateURIs_FailsClosed(t *testing.T) {
	ksuid := util.NewID()
	arn := "arn:aws:sns:us-east-1:111122223333:topic-a"
	document := []byte(`{"Topics": ["name-a", "name-a"]}`)
	stored := fmt.Appendf(nil, `{"Topics": [
		{"$ref": "formae://%s#/ArnA", "$value": "name-a", "$applied": %q},
		{"$ref": "formae://%s#/ArnA", "$value": "name-a", "$applied": %q}
	]}`, ksuid, arn, ksuid, arn)
	patch := fmt.Appendf(nil, `{"Topics": [
		{"$ref": "formae://%s#/ArnA"},
		{"$ref": "formae://%s#/ArnA"}
	]}`, ksuid, ksuid)
	schema := pkgmodel.Schema{Fields: []string{"Topics"}}
	props := resolver.NewResolvableProperties()
	props.Add(ksuid, "ArnA", arn)

	patchDoc, _, err := generatePatch(document, patch, stored, props, schema, pkgmodel.FormaApplyModeReconcile)
	require.NoError(t, err)
	var ops []jsonpatch.JsonPatchOperation
	require.NoError(t, json.Unmarshal(patchDoc, &ops))
	assert.NotEmpty(t, ops, "ambiguous counterparts must not silently equal")
}
```

- [ ] **Step 2: Run to verify the first test fails** (index pairing associates the wrong stored element, so the ARN-vs-name diff survives).

Run: `go test -tags=unit -run 'TestGeneratePatch_ArrayRef' ./internal/metastructure/patch/ -v`
Expected: `ArrayRefCounterpartMatchedByURI` FAIL, `DuplicateURIs` may pass already.

- [ ] **Step 3: Implement.** Add the helper:

```go
// storedRefElementByURI finds the stored array element whose $ref equals uri.
// Ambiguity (zero or multiple matches) returns nil: with no unique
// counterpart the element gets no provenance treatment.
func storedRefElementByURI(storedArr []any, uri any) map[string]any {
	var match map[string]any
	for _, elem := range storedArr {
		m, ok := elem.(map[string]any)
		if !ok || m["$ref"] != uri {
			continue
		}
		if match != nil {
			return nil
		}
		match = m
	}
	return match
}
```

In the array branch of `resolveRefs`, when `elemMap` carries a `$ref`, look up the stored counterpart by URI instead of index: build `storedArr, _ := stored[k].([]any)` once before the loop, and for each ref-carrying element pass `wrappedStored := map[string]any{k: storedRefElementByURI(storedArr, elemMap["$ref"])}` into the recursive call (a nil element makes `stored[k]` a nil `map[string]any` lookup inside the recursion, which `storedRefCounterpart` rejects — verify this and coerce to `map[string]any{}` if needed). Non-ref elements keep index threading from Task 1. Desired-side duplicates resolve to the same unique-or-nil answer by construction.

- [ ] **Step 4: Run the tests.**

Run: `go test -tags=unit ./internal/metastructure/patch/ 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/metastructure/patch/
git commit -m "feat(patch): match array reference counterparts by URI with fail-closed ambiguity"
```

---

### Task 4: Stamp `$applied` on write-origin merges

**Files:**
- Modify: `internal/metastructure/resource_update/resource_update.go` (`mergeRefsPreservingUserRefs` :475, `propertyMerger` struct :518, `mergeRefObject` :602, `mergeResObject` :646, `updateProperties` :408 and its two wrappers :397-403, `updateResourceUpdateFromProgress` :253)
- Test: Create `internal/metastructure/resource_update/applied_provenance_test.go`

**Interfaces:**
- Consumes: `mergeRefsPreservingUserRefs(userProperties, pluginProperties json.RawMessage, schema pkgmodel.Schema) (json.RawMessage, error)`.
- Produces: `mergeRefsPreservingUserRefs(userProperties, pluginProperties json.RawMessage, schema pkgmodel.Schema, writeOrigin bool) (json.RawMessage, error)`; `propertyMerger.writeOrigin bool`; `updateProperties(incomingProperties string, targetProperties, targetReadOnlyProperties *json.RawMessage, writeOrigin bool) error`.

- [ ] **Step 1: Write the failing tests** (new file, FSL header, `package resource_update`, imports `encoding/json`, `testing`, `github.com/tidwall/gjson`, testify, `pkgmodel`):

```go
// A write-origin merge (the echo of formae's own Create/Update) records the
// resolution that was sent as $applied alongside the absorbed echo.
func TestMergeRefs_WriteOrigin_StampsApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)

	env := gjson.GetBytes(merged, "TargetKeyId")
	assert.Equal(t, "4711", env.Get("$value").String(), "echo absorbed into $value")
	assert.Equal(t, "arn:aws:kms:us-east-1:111122223333:key/4711", env.Get("$applied").String(), "sent resolution kept as $applied")
}

// A read-origin merge never creates $applied.
func TestMergeRefs_ReadOrigin_DoesNotStampApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}

// Opaque envelopes never receive $applied, even on write-origin merges.
func TestMergeRefs_WriteOrigin_OpaqueEnvelopeExempt(t *testing.T) {
	user := json.RawMessage(`{"Secret": {"$ref": "formae://abc#/Value", "$value": "cleartext", "$visibility": "Opaque"}}`)
	plugin := json.RawMessage(`{"Secret": "cleartext"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Secret"}}, true)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "Secret.$applied").Exists())
}

// $res envelopes get the same write-origin stamping as $ref envelopes.
func TestMergeRes_WriteOrigin_StampsApplied(t *testing.T) {
	user := json.RawMessage(`{"Image": {"$res": "resolve:image", "$value": "ami-sent"}}`)
	plugin := json.RawMessage(`{"Image": "ami-echoed"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"Image"}}, true)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "Image")
	assert.Equal(t, "ami-echoed", env.Get("$value").String())
	assert.Equal(t, "ami-sent", env.Get("$applied").String())
}

// A write-origin merge with no pre-merge $value (nothing was resolved and
// sent for this path) must not fabricate an $applied baseline.
func TestMergeRefs_WriteOrigin_NoSentValue_NoApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)
	assert.False(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}
```

First check how the `$res` detection routes into `mergeResObject` (`grep -n '"\$res"' internal/metastructure/resource_update/resource_update.go`) and adjust the `$res` test's envelope shape to whatever the dispatcher keys on.

- [ ] **Step 2: Run to verify failure** (compile error on arity first; fix call sites in the SAME step by adding the `writeOrigin` parameter everywhere with `false`, then re-run to see the write-origin assertions fail behaviorally).

Run: `go test -tags=unit -run 'TestMergeRefs_|TestMergeRes_' ./internal/metastructure/resource_update/ -v`
Expected: FAIL on the three write-origin stamping assertions.

- [ ] **Step 3: Implement.**
  - Add `writeOrigin bool` to `propertyMerger` and to `mergeRefsPreservingUserRefs`; set it in the constructor literal.
  - In `mergeRefObject`, after `updatedRef` is computed (and after the existing `$hashed` block), add:

```go
	// Provenance baseline: on the echo-merge of formae's own successful
	// write, the envelope's pre-merge $value is the resolution that was
	// actually sent; keep it as $applied so later diffs can compare the
	// written domain against itself. Opaque envelopes are exempt: their
	// value is hashed at rest and has a dedicated suppression path.
	if userVal.Get("$visibility").String() != pkgmodel.VisibilityOpaque {
		if m.writeOrigin {
			if userValue.Exists() && userValue.Value() != nil {
				updatedRef, _ = sjson.Set(updatedRef, "$applied", userValue.Value())
			}
		}
	}
```

  - Mirror the same block in `mergeResObject` (before `*m.result` is set, operating on `updatedRes`).
  - `updateProperties` gains the `writeOrigin bool` parameter and passes it to `mergeRefsPreservingUserRefs`. `updateResourceProperties`/`updateExistingResourceProperties` gain and forward the parameter. In `updateResourceUpdateFromProgress`, compute the origin from the progress operation — the echo of our own write is a Create or Update progress; every Read-shaped merge (sync, discovery, the pre-update out-of-band read into PriorState) is read-origin:

```go
	writeOrigin := progress.Operation == resource.OperationCreate || progress.Operation == resource.OperationUpdate
```

  pass `writeOrigin` to `updateResourceProperties`, and hard-code `false` for the `updateExistingResourceProperties` branch (it only ever absorbs the pre-update Read).
  - Fix remaining callers found by `grep -rn "mergeRefsPreservingUserRefs(\|updateResourceProperties(\|updateExistingResourceProperties(" internal/ --include="*.go"` (tests pass explicit `false` unless the test exercises stamping).

- [ ] **Step 4: Run the package suite.**

Run: `go test -tags=unit ./internal/metastructure/resource_update/ 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/metastructure/resource_update/
git commit -m "feat(resource-update): record the applied resolution when absorbing write echoes"
```

---

### Task 5: Preserve and invalidate `$applied` on read-origin merges

**Files:**
- Modify: `internal/metastructure/resource_update/resource_update.go` (`mergeRefObject`, `mergeResObject`)
- Test: `internal/metastructure/resource_update/applied_provenance_test.go`

**Interfaces:**
- Consumes: everything from Task 4.

- [ ] **Step 1: Write the failing tests** (append to the Task 4 file):

```go
// A sync read that echoes the same value leaves $applied untouched.
func TestMergeRefs_ReadOrigin_SameEcho_PreservesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "4711"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(merged, "TargetKeyId.$applied").Exists())
}

// A sync read that adopts a DIFFERENT echo is real out-of-band drift on the
// path: the baseline is invalidated so the next plan falls back to the
// corrective fresh-vs-echo diff.
func TestMergeRefs_ReadOrigin_ChangedEcho_InvalidatesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "9988"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "TargetKeyId")	
	assert.Equal(t, "9988", env.Get("$value").String())
	assert.False(t, env.Get("$applied").Exists(), "a differing adopted echo must invalidate the baseline")
}

// A plugin that omits the path (or returns null/empty) is an unobservable
// read, not drift: the stored value is kept and $applied survives.
func TestMergeRefs_ReadOrigin_OmittedEcho_PreservesApplied(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "4711", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)

	for _, plugin := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"TargetKeyId": null}`),
		json.RawMessage(`{"TargetKeyId": ""}`),
	} {
		merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, false)
		require.NoError(t, err)
		env := gjson.GetBytes(merged, "TargetKeyId")
		assert.Equal(t, "4711", env.Get("$value").String())
		assert.True(t, env.Get("$applied").Exists(), "unobservable reads must not invalidate: %s", plugin)
	}
}

// A write-origin merge refreshes $applied rather than invalidating it, even
// though the echo differs from the pre-merge $value.
func TestMergeRefs_WriteOrigin_RestampsOverInvalidation(t *testing.T) {
	user := json.RawMessage(`{"TargetKeyId": {"$ref": "formae://abc#/Arn", "$value": "arn:aws:kms:us-east-1:111122223333:key/9988", "$applied": "arn:aws:kms:us-east-1:111122223333:key/4711"}}`)
	plugin := json.RawMessage(`{"TargetKeyId": "9988"}`)

	merged, err := mergeRefsPreservingUserRefs(user, plugin, pkgmodel.Schema{Fields: []string{"TargetKeyId"}}, true)
	require.NoError(t, err)
	env := gjson.GetBytes(merged, "TargetKeyId")
	assert.Equal(t, "9988", env.Get("$value").String())
	assert.Equal(t, "arn:aws:kms:us-east-1:111122223333:key/9988", env.Get("$applied").String())
}
```

- [ ] **Step 2: Run to verify the invalidation test fails.**

Run: `go test -tags=unit -run 'TestMergeRefs_ReadOrigin' ./internal/metastructure/resource_update/ -v`
Expected: `ChangedEcho_InvalidatesApplied` FAIL ($applied still present); the preserve cases pass (sjson.Set on userVal.Raw keeps unknown keys — if they FAIL, stop and investigate before proceeding).

- [ ] **Step 3: Implement.** Extend the Task 4 block in `mergeRefObject` with the read-origin arm — invalidation is derived from the absorption decision so the two can never disagree:

```go
	if userVal.Get("$visibility").String() != pkgmodel.VisibilityOpaque {
		if m.writeOrigin {
			if userValue.Exists() && userValue.Value() != nil {
				updatedRef, _ = sjson.Set(updatedRef, "$applied", userValue.Value())
			}
		} else if userVal.Get("$applied").Exists() &&
			!m.keptUserValue(userValue, pluginVal) &&
			!reflect.DeepEqual(valueToSet, userValue.Value()) {
			// The merger adopted a plugin echo that differs from the absorbed
			// one: out-of-band drift in the observed domain. Drop the baseline
			// so the next plan runs the corrective fresh-vs-echo diff.
			updatedRef, _ = sjson.Delete(updatedRef, "$applied")
		}
	}
```

Mirror in `mergeResObject` using its already-computed `keptUser` and `valueToSet` (compare against `userValue.Value()` the same way). Add `"reflect"` to imports.

- [ ] **Step 4: Run the full package plus patch package.**

Run: `go test -tags=unit ./internal/metastructure/... 2>&1 | tail -10`
Expected: all `ok`.

- [ ] **Step 5: Commit.**

```bash
git add internal/metastructure/resource_update/
git commit -m "feat(resource-update): invalidate the applied baseline when sync absorbs real drift"
```

---

### Task 6: Whole-tree verification

**Files:** none new.

- [ ] **Step 1: Confirm `$applied` cannot leak to plugins or extract output.** `grep -rn '"\$applied"' internal/ --include="*.go" | grep -v _test` must show only `patch/patch_document.go` and `resource_update/resource_update.go`. Then check the two egress paths: `toPluginFormat` replaces resolved envelopes with their scalar `$value` (envelope keys never reach plugins), and the PKL extract renderer emits reference expressions from `$ref` only — find it with `grep -rn 'renderRef\|\$ref' internal/pkl_export/ internal/extract/ 2>/dev/null | head` (adjust to the actual package; locate via `grep -rn "func.*[Ee]xtract.*[Pp]kl\|toPkl" internal/ --include="*.go" | head`). If either path would serialize unknown envelope keys verbatim into output a USER sees or a PLUGIN receives, add a strip of `$applied` at that boundary with a behavior-describing comment, plus a unit test proving the strip.

- [ ] **Step 2: Run everything the repo's unit gate runs.**

Run: `make test-unit 2>&1 | tail -15`
Expected: all `ok`. Investigate any failure before proceeding — datastore round-trip suites exercise stored-property JSON and may pin envelope shapes.

- [ ] **Step 3: Lint.**

Run: `golangci-lint run ./internal/metastructure/... 2>&1 | tail -10` (fall back to `make lint` if the target exists)
Expected: clean.

- [ ] **Step 4: Commit any Step 1 boundary fixes.**

```bash
git add -A internal/
git commit -m "fix(extract): keep provenance markers out of rendered output"
```

(Skip the commit if Step 1 required no changes.)
