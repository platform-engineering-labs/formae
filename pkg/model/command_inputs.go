// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// UnmarshalJSON preserves the effective input number exactly. Decoding it
// through float64 would round large integer inputs before history is stored.
// Default retains its existing representation for CLI flag construction.
func (p *Prop) UnmarshalJSON(data []byte) error {
	type plain Prop
	var decoded plain
	wire := struct {
		*plain
		Value json.RawMessage `json:"Value"`
	}{plain: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if len(wire.Value) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(wire.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded.Value); err != nil {
			return err
		}
	}
	*p = Prop(decoded)
	return nil
}

// SnapshotInputProperties records input metadata independently of resource
// declarations. Explicitly public values are recorded, sensitive values are
// redacted, and older unclassified inputs remain unavailable. Missing manifests
// remain unavailable; no source is inferred from defaults.
func SnapshotInputProperties(properties map[string]Prop) json.RawMessage {
	if properties == nil {
		return nil
	}
	snapshot := make(map[string]map[string]any, len(properties))
	for name, prop := range properties {
		entry := make(map[string]any)
		if prop.Source == "supplied" || prop.Source == "declaration" {
			entry["source"] = prop.Source
		}
		if prop.Type != "" {
			entry["type"] = prop.Type
		}
		if prop.Flag != "" {
			entry["flag"] = prop.Flag
		}
		if prop.Sensitive == nil {
			entry["unavailable"] = true
		} else if *prop.Sensitive {
			entry["redacted"] = true
		} else {
			value, safe := safeInputScalar(prop.Value)
			if safe {
				entry["value"] = value
			} else {
				entry["unavailable"] = true
			}
		}
		snapshot[name] = entry
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		// Every dynamic value above is either metadata text, a boolean, or an
		// individually validated RawMessage. Reaching this indicates a broken
		// invariant in this function rather than malformed manifest input.
		panic(fmt.Sprintf("marshal validated input-property snapshot: %v", err))
	}
	return data
}

// safeInputScalar accepts only JSON scalar forms and returns their already
// validated encoding. Keeping the encoded value prevents a later map marshal
// from encountering an invalid number and dropping otherwise valid entries.
func safeInputScalar(value any) (json.RawMessage, bool) {
	if value == nil {
		return json.RawMessage("null"), true
	}

	if number, ok := value.(json.Number); ok {
		encoded, err := json.Marshal(number)
		return encoded, err == nil
	}

	kind := reflect.TypeOf(value).Kind()
	switch kind {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		encoded, err := json.Marshal(value)
		return encoded, err == nil
	default:
		return nil, false
	}
}

// RecordInputPropertySources annotates the evaluated manifest with actual
// evaluation inputs. Declaration values include defaults and values authored
// directly in Pkl; neither is inferred from equality with a default.
func RecordInputPropertySources(properties map[string]Prop, supplied map[string]string) {
	for name, prop := range properties {
		flag := prop.Flag
		if flag == "" {
			flag = name
		}
		prop.Source = "declaration"
		if _, present := supplied[flag]; present {
			prop.Source = "supplied"
		}
		properties[name] = prop
	}
}
