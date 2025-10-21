// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package random

import (
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"strconv"

	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/extension"
	"github.com/platform-engineering-labs/formae/pkg/plugin/schema/lib/registry"
)

var Random = random{}

type random struct{}

var _ extension.Library = random{}

func init() {
	registry.Register("random", func() extension.Library {
		return Random
	})
}

func (random) Invoke(uri *url.URL) *extension.Result {
	call, args := extension.NameArgsFrom(uri)

	switch call {
	case "id":
		return id(args)
	case "password":
		return password(args)
	default:
		return &extension.Result{
			Error: fmt.Sprintf("unknown function name: %s", call),
		}
	}
}

// id: get a fixed length random number
func id(args url.Values) *extension.Result {
	length, err := strconv.Atoi(extension.ValueOrDefault(args.Get("length"), "2"))
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to convert length to integer: %v", err),
		}
	}

	if length <= 0 {
		return &extension.Result{
			Error: fmt.Sprintf("length must be greater than: %v", 0),
		}
	}

	mx := int64(math.Pow10(length)) - 1
	mn := int64(math.Pow10(length - 1))

	id := rand.Int64N(mx - mn)

	if id < mn {
		id = mn + (id % (mx - mn + 1))
	}

	body, err := json.Marshal(map[string]any{
		"id": id,
	})
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to serialize result: %d", id),
		}
	}

	return &extension.Result{
		Body: body,
	}
}

// password: get a fixed length random password
func password(args url.Values) *extension.Result {
	length, err := strconv.Atoi(extension.ValueOrDefault(args.Get("length"), "12"))
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to convert length to integer: %v", err),
		}
	}

	useSpecial, err := strconv.ParseBool(extension.ValueOrDefault(args.Get("useSpecial"), "true"))
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to convert useSpecial to boolean: %v", err),
		}
	}

	if length <= 7 {
		return &extension.Result{
			Error: fmt.Sprintf("length must be greater than: %v", 7),
		}
	}

	const standard = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const special = "!@#$%^*()_+-"
	bytes := make([]byte, length)

	if _, err := crand.Read(bytes); err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("could not read random bytes: %v", err),
		}
	}

	chars := standard
	if useSpecial {
		chars += special
	}

	for i, b := range bytes {
		bytes[i] = chars[b%byte(len(chars))]
	}

	body, err := json.Marshal(map[string]any{
		"password": string(bytes),
	})
	if err != nil {
		return &extension.Result{
			Error: fmt.Sprintf("failed to serialize result: %s", string(bytes)),
		}
	}

	return &extension.Result{
		Body: body,
	}
}
