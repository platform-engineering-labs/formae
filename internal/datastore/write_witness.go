// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package datastore

import (
	"encoding/json"
	"strings"
)

// WriteVersion is one genuine-write version of a resource, newest first as
// returned by the dialects: the persisted post-write properties, the owning
// resource update's operation, and its patch document (nil for creates).
type WriteVersion struct {
	Operation  string
	Patch      json.RawMessage
	Properties json.RawMessage
}

// ComposeWriteWitness builds the per-field write witness from a resource's
// genuine-write history (newest first): the newest create or replace echo is
// the base (the cloud's considered state of a fresh resource, including the
// defaults it filled in), and each later update overlays ONLY the top-level
// fields its patch actually wrote, taking their values from that update's
// echo. Fields an update's echo merely carried along (runtime-populated
// content) never enter the witness, and no witness exists before the first
// create in the fetched history.
func ComposeWriteWitness(history []WriteVersion) json.RawMessage {
	base := -1
	for i, v := range history {
		if v.Operation != "update" {
			base = i
			break
		}
	}
	if base < 0 {
		return nil
	}

	var witness map[string]any
	if err := json.Unmarshal(history[base].Properties, &witness); err != nil || witness == nil {
		return nil
	}

	// Overlay updates oldest-first so newer writes win.
	for i := base - 1; i >= 0; i-- {
		v := history[i]
		var echo map[string]any
		if err := json.Unmarshal(v.Properties, &echo); err != nil {
			continue
		}
		for _, field := range patchTopLevelFields(v.Patch) {
			if val, ok := echo[field]; ok {
				witness[field] = val
			} else {
				delete(witness, field)
			}
		}
	}

	out, err := json.Marshal(witness)
	if err != nil {
		return nil
	}
	return json.RawMessage(out)
}

// patchTopLevelFields returns the distinct top-level property names a JSON
// patch document touches, in first-seen order.
func patchTopLevelFields(patch json.RawMessage) []string {
	if len(patch) == 0 {
		return nil
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(patch, &ops); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var fields []string
	for _, op := range ops {
		segs := strings.SplitN(strings.TrimPrefix(op.Path, "/"), "/", 2)
		if len(segs) == 0 || segs[0] == "" {
			continue
		}
		field := strings.ReplaceAll(strings.ReplaceAll(segs[0], "~1", "/"), "~0", "~")
		if !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields
}
