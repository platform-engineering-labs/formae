// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ResolvableObject.Path is a gjson/sjson path built from literal JSON map keys.
// A key carrying path syntax — a Kubernetes annotation, say — must be escaped as
// the path is built, or the envelope is read from and written to a nested tree
// rather than to the key it actually sits under.

const dottedAnnotationKey = "objectset.rio.cattle.io/applied"

func resolvableEnvelope() string {
	return `{"$res":true,"$label":"secret","$type":"K8s::Core::Secret","$stack":"default","$property":"data"}`
}

func TestFindResolvablesFromProperties_DottedKeyPath(t *testing.T) {
	props := `{"metadata":{"annotations":{"` + dottedAnnotationKey + `":` + resolvableEnvelope() + `}}}`

	resolvables := FindResolvablesFromProperties(props)
	require.Len(t, resolvables, 1)

	assert.Equal(t, `metadata.annotations.objectset\.rio\.cattle\.io\/applied`, resolvables[0].Path,
		"the path must address the literal key, not a nested tree")
	assert.True(t, gjson.Get(props, resolvables[0].Path).Get("$res").Bool(),
		"the path must read back the envelope it was built from")
}

func TestFindResolvablesFromProperties_DottedKeyInsideArray(t *testing.T) {
	props := `{"items":[{"` + dottedAnnotationKey + `":` + resolvableEnvelope() + `}]}`

	resolvables := FindResolvablesFromProperties(props)
	require.Len(t, resolvables, 1)

	assert.Equal(t, `items.0.objectset\.rio\.cattle\.io\/applied`, resolvables[0].Path)
	assert.True(t, gjson.Get(props, resolvables[0].Path).Get("$res").Bool(),
		"the path must read back the envelope it was built from")
}

func TestFindResolvablesFromProperties_DottedTopLevelKey(t *testing.T) {
	props := `{"` + dottedAnnotationKey + `":` + resolvableEnvelope() + `}`

	resolvables := FindResolvablesFromProperties(props)
	require.Len(t, resolvables, 1)

	assert.Equal(t, `objectset\.rio\.cattle\.io\/applied`, resolvables[0].Path)
	assert.True(t, gjson.Get(props, resolvables[0].Path).Get("$res").Bool(),
		"the path must read back the envelope it was built from")
}

// Escaping at construction leaves plain-key paths byte-identical, so every
// existing resolvable keeps the path it has today.
func TestFindResolvablesFromProperties_PlainKeyPathsUnchanged(t *testing.T) {
	props := `{"spec":{"containers":[{"image":` + resolvableEnvelope() + `}]},"name":` + resolvableEnvelope() + `}`

	got := map[string]bool{}
	for _, r := range FindResolvablesFromProperties(props) {
		got[r.Path] = true
	}

	assert.True(t, got["spec.containers.0.image"], "got %v", got)
	assert.True(t, got["name"], "got %v", got)
}

// The escaping rule is shared with formae core, which cannot be imported here
// (this is a separately versioned module) and so carries its own
// implementation. This pins the rule itself so the two cannot drift: every
// character gjson treats as path syntax is escaped, and so is the colon sjson
// reads as its force marker.
func TestEscapePathKeyImplementsTheSharedRule(t *testing.T) {
	keys := []string{
		"name",
		"my_field-name",
		"123",
		"with space",
		"ünïcodé",
		dottedAnnotationKey,
		"app.kubernetes.io/name",
		`back\slash`,
		"star*key",
		"question?key",
		"hash#key",
		"at@key",
		"pipe|key",
		"colon:key",
		":leading",
		"bracket[0]",
	}
	for _, key := range keys {
		assert.Equal(t, escapeColons(gjson.Escape(key)), escapePathKey(key), "key %q", key)
	}
}

func escapeColons(s string) string {
	return strings.ReplaceAll(s, ":", `\:`)
}
