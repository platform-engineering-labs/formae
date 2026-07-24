// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package theme

import "reflect"

// missingAgainst reports the TOML-qualified names of fields that base has set
// (non-nil pointer, or non-empty slice) but f does not. It compares field
// PRESENCE only, never values, so it is safe to call with quiet as base
// without leaking quiet's colors/glyphs into the result — it only tells the
// caller which required fields a candidate theme is missing.
//
// Used to define completeness: a themeFile is complete against a base iff
// missingAgainst returns no names.
func (f *themeFile) missingAgainst(base *themeFile) []string {
	var missing []string
	collectMissing(reflect.ValueOf(base).Elem(), reflect.ValueOf(f).Elem(), "", &missing)
	return missing
}

// collectMissing walks parallel struct values (base and candidate),
// recursing into nested structs and comparing leaf pointer/slice fields by
// presence. Fields without a toml tag (Name, Extends) are metadata, not
// themeable surface, and are skipped.
func collectMissing(base, cand reflect.Value, prefix string, missing *[]string) {
	t := base.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		name := tag
		if prefix != "" {
			name = prefix + "." + tag
		}

		bf, cf := base.Field(i), cand.Field(i)
		switch bf.Kind() {
		case reflect.Struct:
			collectMissing(bf, cf, name, missing)
		case reflect.Ptr:
			if !bf.IsNil() && cf.IsNil() {
				*missing = append(*missing, name)
			}
		case reflect.Slice:
			if bf.Len() > 0 && cf.Len() == 0 {
				*missing = append(*missing, name)
			}
		}
	}
}
