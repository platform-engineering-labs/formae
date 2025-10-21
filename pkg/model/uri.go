// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/tidwall/gjson"
)

type TripletKey struct {
	Stack string
	Label string
	Type  string
}

type TripletURI string

func (t TripletURI) IsValid() bool {
	if string(t) == "" {
		return false
	}

	fu, err := url.Parse(string(t))
	if err != nil {
		return false
	}

	if fu.Scheme != "formae" {
		return false
	}

	if len(strings.Split(fu.Path, "/")) < 1 {
		return false
	}

	return true
}

func (t TripletURI) Fqn() string {
	fu, _ := url.Parse(string(t))

	return fu.Host
}

func (t TripletURI) Namespace() string {
	fu, _ := url.Parse(string(t))
	parts := strings.Split(fu.Host, ".")

	return parts[len(parts)-1]
}

func (t TripletURI) PropertyPath() string {
	fu, _ := url.Parse(string(t))

	return strings.TrimLeft(fu.Fragment, "/")
}

func (t TripletURI) Stack() string {
	fu, _ := url.Parse(string(t))
	frags := strings.Split(strings.Trim(fu.Path, "/"), "/")

	return frags[0]
}

func (t TripletURI) ResourceLabel() string {
	fu, _ := url.Parse(string(t))
	frags := strings.Split(strings.Trim(fu.Path, "/"), "/")

	return frags[1]
}

func (t TripletURI) ResourceType() string {
	fu, _ := url.Parse(string(t))
	frags := strings.Split(strings.Trim(fu.Host, "/"), ".")
	slices.Reverse(frags)

	return strings.ToUpper(strings.Join(frags, "::"))
}

// FormaeURI represents a KSUID-based resource URI
type FormaeURI string

func NewFormaeURI(uri, property string) FormaeURI {
	if property == "" {
		return FormaeURI(fmt.Sprintf("formae://%s#", uri))
	}
	return FormaeURI(fmt.Sprintf("formae://%s#/%s", uri, property))
}

func (f FormaeURI) IsValid() bool {
	s := string(f)
	if !strings.HasPrefix(s, "formae://") {
		return false
	}

	remainder := s[9:]
	if !strings.Contains(remainder, "#") {
		return false
	}

	parts := strings.SplitN(remainder, "#", 2)
	if len(parts) != 2 || len(parts[0]) == 0 {
		return false
	}

	fragment := parts[1]
	return fragment == "" || strings.HasPrefix(fragment, "/")
}

func (f FormaeURI) KSUID() string {
	s := string(f)
	if !strings.HasPrefix(s, "formae://") {
		return ""
	}

	remainder := s[9:]
	parts := strings.SplitN(remainder, "#", 2)
	if len(parts) != 2 {
		return ""
	}

	return parts[0]
}

func (f FormaeURI) PropertyPath() string {
	s := string(f)
	if !strings.HasPrefix(s, "formae://") {
		return ""
	}

	remainder := s[9:]
	parts := strings.SplitN(remainder, "#", 2)
	if len(parts) != 2 {
		return ""
	}

	fragment := parts[1]
	if fragment == "" || fragment == "/" {
		return ""
	}

	if strings.HasPrefix(fragment, "/") {
		return fragment[1:]
	}

	return fragment
}

func (f FormaeURI) Stripped() FormaeURI {
	return NewFormaeURI(f.KSUID(), "")
}

func (f FormaeURI) ToTripletURI(triplet TripletKey) string {
	if !f.IsValid() {
		return ""
	}

	typeParts := strings.Split(triplet.Type, "::")
	slices.Reverse(typeParts)
	reversedType := strings.ToLower(strings.Join(typeParts, "."))

	return fmt.Sprintf("formae://%s/%s/%s#/%s", reversedType, triplet.Stack, triplet.Label, f.PropertyPath())
}

// ResolvableObject represents a resolvable object found in JSON with $res: true
type ResolvableObject struct {
	Path     string
	Label    string
	Type     string
	Stack    string
	Property string
}

// FindResolvablesFromProperties traverses json properties and finds all objects with $res: true
func FindResolvablesFromProperties(jsonStr string) []ResolvableObject {
	var resolvables []ResolvableObject
	result := gjson.Parse(jsonStr)
	findResolvablesRecursive("", result, &resolvables)

	return resolvables
}

// findResolvablesRecursive recursively searches for resolvable objects
func findResolvablesRecursive(basePath string, value gjson.Result, resolvables *[]ResolvableObject) {
	if value.IsObject() {
		// Check if this object is a resolvable ($res: true)
		if IsResolvableObject(value) {
			resolvable := ResolvableObject{
				Path:     basePath,
				Label:    value.Get("$label").String(),
				Type:     value.Get("$type").String(),
				Stack:    value.Get("$stack").String(),
				Property: value.Get("$property").String(),
			}
			*resolvables = append(*resolvables, resolvable)
			return
		}

		// Recurse into object properties
		value.ForEach(func(key, val gjson.Result) bool {
			var newPath string
			if basePath == "" {
				newPath = key.String()
			} else {
				newPath = fmt.Sprintf("%s.%s", basePath, key.String())
			}
			findResolvablesRecursive(newPath, val, resolvables)
			return true
		})
	} else if value.IsArray() {
		// Recurse into array elements
		value.ForEach(func(key, val gjson.Result) bool {
			var newPath string
			if basePath == "" {
				newPath = key.String()
			} else {
				newPath = fmt.Sprintf("%s.%s", basePath, key.String())
			}
			findResolvablesRecursive(newPath, val, resolvables)
			return true
		})
	}
}

// IsResolvableObject checks if a JSON object is a resolvable ($res: true)
func IsResolvableObject(value gjson.Result) bool {
	if !value.IsObject() {
		return false
	}

	resField := value.Get("$res")
	return resField.Exists() && resField.Bool()
}

// ToTripletKey converts a resolvable object to a TripletKey
func (r ResolvableObject) ToTripletKey() TripletKey {
	return TripletKey{
		Stack: r.Stack,
		Label: r.Label,
		Type:  r.Type,
	}
}

// ToFormaeURI converts a resolvable object to FormaeURI format using the provided KSUID
func (r ResolvableObject) ToFormaeURI(ksuid string) FormaeURI {
	if r.Property == "" {
		return NewFormaeURI(ksuid, "")
	}
	return NewFormaeURI(ksuid, r.Property)
}
