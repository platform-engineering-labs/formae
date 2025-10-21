// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package extension

import (
	"encoding/json"
	"net/url"
	"strings"
)

type Library interface {
	Invoke(url *url.URL) *Result
}

type Result struct {
	Error string `json:",omitempty"`
	Body  json.RawMessage
}

func (r *Result) Json() ([]byte, error) {
	return json.Marshal(r)
}

func NameArgsFrom(uri *url.URL) (string, url.Values) {
	elements := strings.Split(strings.TrimPrefix(uri.Path, "/"), "/")
	invoke := elements[1]

	return invoke, uri.Query()
}

func ValueOrDefault(value string, defaultValue string) string {
	if value != "" {
		return value
	}

	return defaultValue
}
