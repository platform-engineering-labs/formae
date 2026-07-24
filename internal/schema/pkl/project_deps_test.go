// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package pkl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePklProjectDeps_RemoteOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PklProject")
	require.NoError(t, os.WriteFile(path, []byte(`amends "pkl:Project"

dependencies {
  ["formae"] {
    uri = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.85.0"
  }
  ["aws"] {
    uri = "package://hub.platform.engineering/plugins/aws/schema/pkl/aws/aws@0.1.5"
  }
}
`), 0644))

	deps, err := parsePklProjectDeps(path)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"aws.aws@0.1.5",
	}, deps)
}

func TestParsePklProjectDeps_LocalOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PklProject")
	require.NoError(t, os.WriteFile(path, []byte(`amends "pkl:Project"

dependencies {
  ["aws"] = import("/home/me/.pel/formae/plugins/aws/v0.1.5/schema/pkl/PklProject")
}
`), 0644))

	deps, err := parsePklProjectDeps(path)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"local:aws:/home/me/.pel/formae/plugins/aws/v0.1.5/schema/pkl/PklProject",
	}, deps)
}

func TestParsePklProjectDeps_Mixed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PklProject")
	require.NoError(t, os.WriteFile(path, []byte(`amends "pkl:Project"

dependencies {
  ["formae"] {
    uri = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.85.0"
  }
  ["aws"] = import("/path/to/aws/PklProject")
}
`), 0644))

	deps, err := parsePklProjectDeps(path)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"pkl.formae@0.85.0",
		"local:aws:/path/to/aws/PklProject",
	}, deps)
}

func TestParsePklProjectDeps_EmptyBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PklProject")
	require.NoError(t, os.WriteFile(path, []byte(`amends "pkl:Project"

dependencies {
}
`), 0644))

	deps, err := parsePklProjectDeps(path)
	require.NoError(t, err)
	assert.Empty(t, deps)
}

func TestParsePklProjectDeps_FileMissing(t *testing.T) {
	_, err := parsePklProjectDeps("/no/such/file")
	require.Error(t, err)
}

func TestBumpFormaeCoreDep(t *testing.T) {
	t.Run("bumps and reports previous version, leaves plugin deps", func(t *testing.T) {
		in := []string{"aws.aws@0.1.5", "pkl.formae@0.85.0"}
		out, current := bumpFormaeCoreDep(in, "0.88.0")
		assert.Equal(t, "0.85.0", current)
		assert.Equal(t, []string{"aws.aws@0.1.5", "pkl.formae@0.88.0"}, out)
		assert.Equal(t, "pkl.formae@0.85.0", in[1], "input slice must not be mutated")
	})

	t.Run("no formae core dep reports empty", func(t *testing.T) {
		out, current := bumpFormaeCoreDep([]string{"aws.aws@0.1.5"}, "0.88.0")
		assert.Empty(t, current)
		assert.Equal(t, []string{"aws.aws@0.1.5"}, out)
	})

	t.Run("already current still reports it", func(t *testing.T) {
		_, current := bumpFormaeCoreDep([]string{"pkl.formae@0.88.0"}, "0.88.0")
		assert.Equal(t, "0.88.0", current)
	})
}

func TestCoreSchemaVersion(t *testing.T) {
	cases := map[string]string{
		"0.88.0":         "0.88.0",
		"0.88.0-dev.7":   "0.88.0",
		"0.88.0-rc.1":    "0.88.0",
		"0.88.0+build.3": "0.88.0",
		"0.0.0":          "0.0.0",
		"0.0.0-dev.3":    "0.0.0",
	}
	for in, want := range cases {
		assert.Equal(t, want, coreSchemaVersion(in), "coreSchemaVersion(%q)", in)
	}
}

func TestIsOlderVersion(t *testing.T) {
	assert.True(t, isOlderVersion("0.85.0", "0.88.0"), "older patchline is behind")
	assert.True(t, isOlderVersion("0.87.9", "0.88.0"))
	assert.False(t, isOlderVersion("0.88.0", "0.88.0"), "equal is not older")
	assert.False(t, isOlderVersion("0.89.0", "0.88.0"), "newer must not nag")
	assert.False(t, isOlderVersion("", "0.88.0"), "no dep found → no nag")
	assert.False(t, isOlderVersion("garbage", "0.88.0"), "unparseable → no nag")
	assert.False(t, isOlderVersion("0.85.0", "nope"), "unparseable target → no nag")

	// The rule is the hardcoded 0.88.0, independent of binary version.
	assert.Equal(t, "0.88.0", requiredFormaeSchemaVersion, "the rule is 0.88.0")
	assert.True(t, isOlderVersion("0.87.9", requiredFormaeSchemaVersion), "below the rule nags")
	assert.False(t, isOlderVersion(requiredFormaeSchemaVersion, requiredFormaeSchemaVersion), "at the rule is fine")
	assert.False(t, isOlderVersion("0.99.0", requiredFormaeSchemaVersion), "above the rule is fine")
}
