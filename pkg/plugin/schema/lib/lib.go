// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package lib

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/registry"

	_ "github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/modules/bcrypt"
	_ "github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/modules/random"
)

func Call(uri *url.URL) *extension.Result {
	elements := strings.Split(strings.TrimPrefix(uri.Path, "/"), "/")
	lib := elements[0]

	if registry.HasLibrary(lib) {
		return registry.Get(lib).Invoke(uri)
	}

	return &extension.Result{
		Error: fmt.Sprintf("library '%s' is not registered", lib),
	}
}
