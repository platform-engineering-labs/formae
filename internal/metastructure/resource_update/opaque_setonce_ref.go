// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package resource_update

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/internal/metastructure/pathkey"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// FrozenSetOnceRef records a destination frozen at planning, not a claim that
// its source is unchanged. Paths only; the stored envelope remains in
// PreviousProperties. Persisting the decision keeps reads and recovery from
// changing which destinations this update is allowed to write.
type FrozenSetOnceRef struct {
	HintPath     string `json:"HintPath"`
	Path         string `json:"Path"`
	Pointer      string `json:"Pointer"`
	ArrayRoot    string `json:"ArrayRoot,omitempty"`
	ArrayElement bool   `json:"ArrayElement,omitempty"`
}

func classifyFrozenSetOnceRefs(stored, desired json.RawMessage) []FrozenSetOnceRef {
	var refs []FrozenSetOnceRef
	prior := gjson.ParseBytes(stored)
	var walk func(gjson.Result, string, string, string, string, bool)
	walk = func(node gjson.Result, path, pointer, hintPath, arrayRoot string, arrayElement bool) {
		if node.IsObject() && node.Get("$ref").Type == gjson.String {
			old := prior.Get(path)
			// An explicit strategy/visibility change or a reference repoint is
			// not an unresolved restatement of this frozen destination.
			if node.Get("$value").Exists() || old.Get("$hashed").Type != gjson.True ||
				old.Get("$value").Type != gjson.String || old.Get("$strategy").String() != "SetOnce" ||
				old.Get("$visibility").String() != "Opaque" || node.Get("$ref").String() == "" ||
				old.Get("$ref").String() != node.Get("$ref").String() ||
				old.Get("$json").Raw != node.Get("$json").Raw {
				return
			}
			for key, value := range node.Map() {
				switch key {
				case "$ref", "$json":
				case "$strategy":
					if value.String() != "SetOnce" {
						return
					}
				case "$visibility":
					if value.String() != "Opaque" {
						return
					}
				default:
					return
				}
			}
			refs = append(refs, FrozenSetOnceRef{Path: path, Pointer: pointer, HintPath: hintPath, ArrayRoot: arrayRoot, ArrayElement: arrayElement})
			return
		}
		if node.IsArray() && arrayRoot == "" {
			arrayRoot = pointer
		}
		if node.IsObject() || node.IsArray() {
			node.ForEach(func(key, val gjson.Result) bool {
				k := key.String()
				p := strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
				nextHint := hintPath
				if !node.IsArray() {
					nextHint = childName(hintPath, k)
				}
				// Empty keys cannot be addressed safely by gjson/sjson.
				if k != "" {
					walk(val, buildPath(path, k), pointer+"/"+p, nextHint, arrayRoot, node.IsArray())
				}
				return true
			})
		}
	}
	walk(gjson.ParseBytes(desired), "", "", "", "", false)
	return refs
}

// stripFrozenSetOnceRefs operates only on diff/resolution copies. Direct array
// members become null to keep indices intact; real updates involving frozen
// array members are refused because positional matching cannot prove identity.
func stripFrozenSetOnceRefs(props json.RawMessage, refs []FrozenSetOnceRef) (json.RawMessage, error) {
	out := string(props)
	for _, ref := range refs {
		var err error
		if ref.ArrayElement {
			out, err = sjson.SetRaw(out, ref.Path, "null")
		} else {
			out, err = sjson.Delete(out, ref.Path)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to suppress a frozen secret reference: %w", err)
		}
	}
	return json.RawMessage(out), nil
}

func restoreFrozenSetOnceRefs(props, baseline json.RawMessage, refs []FrozenSetOnceRef) (json.RawMessage, error) {
	out := string(props)
	for _, ref := range refs {
		value := gjson.GetBytes(baseline, ref.Path)
		if !value.Exists() {
			return nil, fmt.Errorf("frozen secret reference has no recorded baseline")
		}
		var err error
		out, err = sjson.SetRaw(out, ref.Path, value.Raw)
		if err != nil {
			return nil, fmt.Errorf("failed to retain a frozen secret reference: %w", err)
		}
	}
	return json.RawMessage(out), nil
}

func freezeSetOnceRefsForPlugin(props json.RawMessage, refs []FrozenSetOnceRef) (json.RawMessage, error) {
	out := string(props)
	for _, ref := range refs {
		var err error
		out, err = sjson.SetRaw(out, ref.Path, opaquePreservedSentinel)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare a frozen secret reference: %w", err)
		}
	}
	return json.RawMessage(out), nil
}

// A leaf omitted from both diff inputs is safe only if no enclosing write
// needs it. Never emit a partial atomic object/array or silently omit a field
// required on update. Replacement must fail before scheduling any delete.
func validateFrozenSetOncePatch(patch, createOnly json.RawMessage, refs []FrozenSetOnceRef, required []string) error {
	if len(refs) == 0 {
		return nil
	}
	if len(createOnly) > 0 {
		return fmt.Errorf("replacement requires a usable value for a frozen SetOnce secret reference")
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if len(patch) > 0 {
		if err := json.Unmarshal(patch, &ops); err != nil {
			return err
		}
	}
	if len(ops) == 0 {
		return nil
	}
	ancestor := func(a, b string) bool { return a == b || strings.HasPrefix(b, a+"/") }
	if err := validateFrozenSetOnceWrite(refs, required); err != nil {
		return err
	}
	for _, ref := range refs {
		for _, op := range ops {
			if ancestor(op.Path, ref.Pointer) {
				return fmt.Errorf("enclosing update requires a usable value for a frozen SetOnce secret reference")
			}
		}
	}
	return nil
}

// validateFrozenSetOnceWrite applies whenever a provider Update is dispatched,
// even if stack/metadata changes kept an otherwise empty patch in the plan.
func validateFrozenSetOnceWrite(refs []FrozenSetOnceRef, required []string) error {
	for _, ref := range refs {
		// Positional pairing cannot preserve an array member's identity
		// through a provider response that reorders members. No-op arrays
		// converge, but an update needs a recoverable value for them.
		if ref.ArrayRoot != "" {
			return fmt.Errorf("update requires a usable value for a frozen SetOnce secret reference in an array")
		}
		for _, field := range required {
			if field == ref.HintPath || strings.HasPrefix(ref.HintPath, field+".") {
				return fmt.Errorf("update requires a usable value for a frozen SetOnce secret reference")
			}
		}
	}
	return nil
}

// Attach to the existing immutable, datastore-backed occurrence records.
// Ambiguous legacy dotted paths fail closed rather than attaching a freeze
// to the wrong occurrence.
func recordFrozenSetOnceRefs(records []OccurrenceRecord, refs []FrozenSetOnceRef) error {
	for _, ref := range refs {
		path := strings.Join(pathkey.Split(ref.Path), ".")
		index, count := 0, 0
		for i := range records {
			if records[i].DestinationPath == path {
				index = i
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("cannot record an ambiguous frozen secret reference")
		}
		copy := ref
		records[index].FrozenSetOnce = &copy
	}
	return nil
}

func (ru *ResourceUpdate) frozenSetOnceRefs() []FrozenSetOnceRef {
	var refs []FrozenSetOnceRef
	for _, rec := range ru.ProvenanceRecords {
		if rec.FrozenSetOnce != nil {
			refs = append(refs, *rec.FrozenSetOnce)
		}
	}
	return refs
}
