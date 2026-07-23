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

func TestRewriteFormaeCoreVersion(t *testing.T) {
	const project = `amends "pkl:Project"

dependencies {
  ["formae"] {
    uri = "package://hub.platform.engineering/plugins/pkl/schema/pkl/formae/formae@0.85.0"
  }
  ["aws"] {
    uri = "package://hub.platform.engineering/plugins/aws/schema/pkl/aws/aws@0.1.5"
  }
}
`
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "PklProject")
		require.NoError(t, os.WriteFile(path, []byte(body), 0644))
		return path
	}

	t.Run("bumps mismatched version and leaves plugin deps untouched", func(t *testing.T) {
		path := write(t, project)
		changed, err := rewriteFormaeCoreVersion(path, "0.88.0")
		require.NoError(t, err)
		assert.True(t, changed)

		deps, err := parsePklProjectDeps(path)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"pkl.formae@0.88.0", "aws.aws@0.1.5"}, deps)
	})

	t.Run("no-op when already at target version", func(t *testing.T) {
		path := write(t, project)
		changed, err := rewriteFormaeCoreVersion(path, "0.85.0")
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("no-op when no formae core dep present", func(t *testing.T) {
		path := write(t, `amends "pkl:Project"

dependencies {
  ["aws"] {
    uri = "package://hub.platform.engineering/plugins/aws/schema/pkl/aws/aws@0.1.5"
  }
}
`)
		changed, err := rewriteFormaeCoreVersion(path, "0.88.0")
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("errors on missing file", func(t *testing.T) {
		_, err := rewriteFormaeCoreVersion("/no/such/file", "0.88.0")
		require.Error(t, err)
	})
}
