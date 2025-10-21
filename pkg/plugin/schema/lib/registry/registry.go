// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package registry

import (
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
)

var registry = make(map[string]func() extension.Library)

func Register(namespace string, creator func() extension.Library) {
	registry[namespace] = creator
}

func Get(namespace string) extension.Library {
	return registry[namespace]()
}

func HasLibrary(namespace string) bool {
	_, exists := registry[namespace]
	return exists
}
