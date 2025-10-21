// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package bcrypt

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/registry"
	gbcrypt "golang.org/x/crypto/bcrypt"
)

var BCrypt = bcrypt{}

type bcrypt struct{}

var _ extension.Library = bcrypt{}

func init() {
	registry.Register("bcrypt", func() extension.Library {
		return BCrypt
	})
}

func (bcrypt) Invoke(uri *url.URL) *extension.Result {
	call, args := extension.NameArgsFrom(uri)

	switch call {
	case "hashPassword":
		return hashPassword(args)
	default:
		return &extension.Result{
			Error: fmt.Sprintf("unknown function name: %s", call),
		}
	}
}

func hashPassword(args url.Values) *extension.Result {
	password := args.Get("password")
	if password == "" {
		return &extension.Result{
			Error: "failed to decode password",
		}
	}

	hash, err := gbcrypt.GenerateFromPassword([]byte(password), gbcrypt.DefaultCost)
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to hash password: %v", err),
		}
	}

	body, err := json.Marshal(map[string]any{
		"hash": string(hash),
	})
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to serialize result: %s", hash),
		}
	}

	return &extension.Result{
		Body: body,
	}
}
