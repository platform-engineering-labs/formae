// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package login

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/app"
	"github.com/platform-engineering-labs/formae/internal/cli/profile/store"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// testEndpoint is the hosted endpoint a generated profile addresses. It is a
// canonical https origin, the only form the config loader accepts.
const testEndpoint = testOrigin

// cliAuth returns a gate-validated block with the given clientId and scopes,
// which are the only two fields the renderer may default.
func cliAuth(clientID, scopes string) cliAuthBlock {
	return cliAuthBlock{
		Type:     oidcAuthType,
		Role:     cliAuthRole,
		Issuer:   testIssuer,
		ClientID: clientID,
		Scopes:   scopes,
	}
}

// writeRendered renders a profile and writes it into its own directory,
// returning the path. The bytes are written as a plain file because these
// tests are about what the renderer produces, not about how it is published.
func writeRendered(t *testing.T, endpoint, installation string, auth cliAuthBlock) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "generated.pkl")
	require.NoError(t, os.WriteFile(path, renderProfile(endpoint, installation, auth), 0o600))
	return path
}

// loadHosted loads path the way every formae command loads a config, and
// returns the hosted connection it resolves to.
func loadHosted(t *testing.T, path string) *pkgmodel.HostedConnection {
	t.Helper()
	a := &app.App{}
	require.NoError(t, a.LoadConfig(path, ""))
	conn, ok := a.Config.Cli.Connection.(*pkgmodel.HostedConnection)
	require.True(t, ok, "expected a hosted connection, got %T", a.Config.Cli.Connection)
	return conn
}

// loadAuth loads the profile at path and decodes its auth block into the
// generic shape it has on the wire, so a test can see the keys and their JSON
// types rather than the fields a Go struct would impose on them.
func loadAuth(t *testing.T, path string) map[string]any {
	t.Helper()
	var fields map[string]any
	require.NoError(t, json.Unmarshal(loadHosted(t, path).Auth, &fields))
	return fields
}

// keysOf returns the keys of fields, sorted, so a test can compare the whole
// key set at once.
func keysOf(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	return keys
}

// writeTempFile writes content into dir under a publication temp name and
// returns its path.
func writeTempFile(t *testing.T, dir string, content []byte) string {
	t.Helper()
	name, err := newTempName()
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

// refuseLinks points the link seam at a filesystem that refuses hard links
// with err, and restores it when the test ends.
func refuseLinks(t *testing.T, err error) {
	t.Helper()
	original := linkFile
	t.Cleanup(func() { linkFile = original })
	linkFile = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: err}
	}
}

// interruptWrites points the write seam at a descriptor that writes half of
// what it was given and then fails, which is what a publication interrupted
// partway through the fallback's write leaves behind. between runs after the
// destination exists and before the failure is reported, so a test can change
// what is at that name in exactly the window a cleanup has to survive.
func interruptWrites(t *testing.T, between func()) {
	t.Helper()
	original := writeFile
	t.Cleanup(func() { writeFile = original })
	writeFile = func(f *os.File, content []byte) (int, error) {
		n, err := original(f, content[:len(content)/2])
		if err != nil {
			return n, err
		}
		if between != nil {
			between()
		}
		return n, io.ErrShortWrite
	}
}

func TestRenderedProfileResolvesToTheIntendedHostedConnection(t *testing.T) {
	path := writeRendered(t, testEndpoint, testUUIDA, cliAuth("", ""))

	hosted := loadHosted(t, path)

	assert.Equal(t, testEndpoint, hosted.Endpoint)
	assert.Equal(t, testUUIDA, hosted.Installation)
}

func TestRenderedAuthBlockCarriesExactlyTheFiveKeys(t *testing.T) {
	path := writeRendered(t, testEndpoint, testUUIDA, cliAuth("", ""))

	fields := loadAuth(t, path)

	want := []string{"type", "role", "issuer", "clientId", "scopes"}
	assert.ElementsMatch(t, want, keysOf(fields))
	// The warning about dropped keys is measured against renderedAuthKeys,
	// which the template does not derive from: this is what keeps the list
	// the warning quotes and the keys the profile carries in step.
	assert.ElementsMatch(t, want, renderedAuthKeys)
	assert.Equal(t, oidcAuthType, fields["type"])
	assert.Equal(t, "cli", fields["role"])
	assert.Equal(t, testIssuer, fields["issuer"])

	// The plugin's wire struct types scopes as a string, so a Listing — which
	// would arrive here as a JSON array — never authenticates.
	scopes, ok := fields["scopes"].(string)
	require.True(t, ok, "scopes must be a string, got %T", fields["scopes"])
	assert.Equal(t, strings.Fields(scopes), strings.Split(scopes, " "), "scopes must be a single space-separated string")
}

func TestRenderedAuthBlockWritesTheDefaultsByValue(t *testing.T) {
	tests := []struct {
		name         string
		auth         cliAuthBlock
		wantClientID string
		wantScopes   string
	}{
		{
			name:         "neither named",
			auth:         cliAuth("", ""),
			wantClientID: "formae-cli",
			wantScopes:   "openid profile email offline_access",
		},
		{
			name:         "both named",
			auth:         cliAuth("customer-cli", "openid email"),
			wantClientID: "customer-cli",
			wantScopes:   "openid email",
		},
		{
			name:         "only clientId named",
			auth:         cliAuth("customer-cli", ""),
			wantClientID: "customer-cli",
			wantScopes:   "openid profile email offline_access",
		},
		{
			name:         "only scopes named",
			auth:         cliAuth("", "openid email"),
			wantClientID: "formae-cli",
			wantScopes:   "openid email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := loadAuth(t, writeRendered(t, testEndpoint, testUUIDA, tt.auth))

			assert.Equal(t, tt.wantClientID, fields["clientId"])
			assert.Equal(t, tt.wantScopes, fields["scopes"])
		})
	}
}

func TestRenderedProfileDropsUnknownSourceKeys(t *testing.T) {
	raw := rawAuth(t, map[string]any{
		"type":          oidcAuthType,
		"role":          cliAuthRole,
		"issuer":        testIssuer,
		"clientId":      "customer-cli",
		"scopes":        "openid email",
		"audience":      "https://api.example",
		"tokenEndpoint": "https://token.example",
	})
	auth, err := decodeCliAuthBlock(raw)
	require.NoError(t, err)

	fields := loadAuth(t, writeRendered(t, testEndpoint, testUUIDA, auth))

	assert.ElementsMatch(t, []string{"type", "role", "issuer", "clientId", "scopes"}, keysOf(fields))
}

func TestUnknownAuthKeysWarningNamesTheDroppedKeys(t *testing.T) {
	tests := []struct {
		name     string
		fields   map[string]any
		wantNone bool
		contains []string
	}{
		{
			name: "every key rendered",
			fields: map[string]any{
				"type": oidcAuthType, "role": cliAuthRole, "issuer": testIssuer,
				"clientId": "customer-cli", "scopes": "openid email",
			},
			wantNone: true,
		},
		{
			name: "unknown keys named in sorted order",
			fields: map[string]any{
				"type": oidcAuthType, "role": cliAuthRole, "issuer": testIssuer,
				"tokenEndpoint": "https://token.example", "audience": "https://api.example",
			},
			contains: []string{"audience", "tokenEndpoint"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warning := unknownAuthKeysWarning(rawAuth(t, tt.fields))

			if tt.wantNone {
				assert.Empty(t, warning)
				return
			}
			for _, key := range tt.contains {
				assert.Contains(t, warning, key)
			}
			assert.Less(t, strings.Index(warning, tt.contains[0]), strings.Index(warning, tt.contains[1]),
				"unknown keys are named in sorted order")
			// A key's value may belong to another system; only names are named.
			assert.NotContains(t, warning, "https://token.example")
			assert.NotContains(t, warning, "https://api.example")
		})
	}
}

func TestEscapableValuesSurviveRenderAndReload(t *testing.T) {
	nasty := []string{
		`a "quoted" value`,
		`a\backslash`,
		"a\nnewline",
		`an \(interpolation)`,
		"a\ttab\rand\rcarriage\rreturns",
		"a\x07bell and a \x7fdelete",
		`"; unrelated = "injected`,
	}

	for _, value := range nasty {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			auth := cliAuth(value, value)
			auth.Issuer = value

			fields := loadAuth(t, writeRendered(t, testEndpoint, testUUIDA, auth))

			assert.Equal(t, value, fields["issuer"])
			assert.Equal(t, value, fields["clientId"])
			assert.Equal(t, value, fields["scopes"])
			assert.ElementsMatch(t, []string{"type", "role", "issuer", "clientId", "scopes"}, keysOf(fields))
		})
	}
}

func TestFingerprintIsASha256Digest(t *testing.T) {
	// The empty digest is the published SHA-256 of no bytes at all, so this
	// pins the algorithm and the encoding rather than restating the code.
	const emptyDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	assert.Equal(t, emptyDigest, fingerprint(nil))
	assert.Equal(t, fingerprint([]byte("same")), fingerprint([]byte("same")))
	assert.NotEqual(t, fingerprint([]byte("same")), fingerprint([]byte("other")))
	assert.Regexp(t, fingerprintRE, fingerprint([]byte("same")))
}

func TestStatAndDigestReadsIdentityAndContentFromOneFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	path := filepath.Join(dir, "regular.pkl")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	info, digest, err := statAndDigest(path)

	require.NoError(t, err)
	assert.Equal(t, fingerprint(content), digest)
	assert.Equal(t, int64(len(content)), info.Size())
	onDisk, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(onDisk, info), "the FileInfo must describe the file that was hashed")
}

func TestStatAndDigestRefusesAnythingButARegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.pkl")
	require.NoError(t, os.WriteFile(target, []byte("profile bytes\n"), 0o600))
	link := filepath.Join(dir, "link.pkl")
	require.NoError(t, os.Symlink(target, link))
	subdir := filepath.Join(dir, "subdir.pkl")
	require.NoError(t, os.Mkdir(subdir, 0o755))

	tests := []struct {
		name     string
		path     string
		wantKind bool
	}{
		{name: "symlink to a regular file", path: link, wantKind: true},
		{name: "directory", path: subdir, wantKind: true},
		{name: "missing", path: filepath.Join(dir, "absent.pkl")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, digest, err := statAndDigest(tt.path)

			require.Error(t, err)
			assert.Nil(t, info)
			assert.Empty(t, digest)
			if tt.wantKind {
				assert.ErrorIs(t, err, errNotRegularFile)
			}
		})
	}
}

func TestPublishLinksTheTempFileIntoPlace(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	temp := writeTempFile(t, dir, content)
	dest := filepath.Join(dir, "generated.pkl")

	require.NoError(t, publish(temp, dest, content, generatedProfileMode))

	published, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, generatedProfileMode, published.Mode().Perm())
	onDisk, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, onDisk)

	// The temp file survives publication: it is the witness that the file at
	// the destination is the one this run wrote.
	tempInfo, err := os.Stat(temp)
	require.NoError(t, err)
	assert.True(t, os.SameFile(tempInfo, published), "the destination is a link to the temp file")
}

func TestPublishRefusesASymlinkedTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.pkl")
	theirs := []byte("a file the user wrote\n")
	require.NoError(t, os.WriteFile(target, theirs, 0o644))
	temp := filepath.Join(dir, ".tmp-0123456789abcdef.pkl")
	require.NoError(t, os.Symlink(target, temp))
	dest := filepath.Join(dir, "generated.pkl")

	err := publish(temp, dest, []byte("profile bytes\n"), generatedProfileMode)

	require.ErrorIs(t, err, errNotRegularFile)
	targetInfo, statErr := os.Stat(target)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o644), targetInfo.Mode().Perm(), "the mode never lands on a symlink's target")
	_, statErr = os.Lstat(dest)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "nothing is published")
}

func TestPublishRefusesADestinationTakenAfterTheCollisionScan(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	temp := writeTempFile(t, dir, content)
	dest := filepath.Join(dir, "generated.pkl")
	// The scan found nothing; the file appears between the scan and the link.
	theirs := []byte("a profile the user wrote\n")
	require.NoError(t, os.WriteFile(dest, theirs, 0o644))

	err := publish(temp, dest, content, generatedProfileMode)

	assert.ErrorIs(t, err, errNameTaken)
	onDisk, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	assert.Equal(t, theirs, onDisk, "the user's file is untouched")
}

func TestPublishFallsBackToAnExclusiveWriteWhenLinksAreUnavailable(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	temp := writeTempFile(t, dir, content)
	dest := filepath.Join(dir, "generated.pkl")
	refuseLinks(t, syscall.EPERM)

	require.NoError(t, publish(temp, dest, content, generatedProfileMode))

	published, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, generatedProfileMode, published.Mode().Perm())
	onDisk, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, onDisk)
	tempInfo, err := os.Stat(temp)
	require.NoError(t, err)
	assert.False(t, os.SameFile(tempInfo, published), "the fallback writes a second file, not a link")
}

func TestPublishFallbackRefusesAnOccupiedDestination(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	temp := writeTempFile(t, dir, content)
	dest := filepath.Join(dir, "generated.pkl")
	theirs := []byte("a profile the user wrote\n")
	require.NoError(t, os.WriteFile(dest, theirs, 0o644))
	refuseLinks(t, syscall.EPERM)

	err := publish(temp, dest, content, generatedProfileMode)

	assert.ErrorIs(t, err, errNameTaken)
	onDisk, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	assert.Equal(t, theirs, onDisk, "the user's file is untouched")
}

func TestPublishFallbackCleansUpOnlyTheFileItCreated(t *testing.T) {
	tests := []struct {
		name    string
		replace bool
	}{
		{name: "an interrupted write leaves nothing at the destination"},
		{name: "a file that replaced the destination is left alone", replace: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			content := []byte("profile bytes\n")
			temp := writeTempFile(t, dir, content)
			dest := filepath.Join(dir, "generated.pkl")
			theirs := []byte("a profile the user wrote\n")
			refuseLinks(t, syscall.EPERM)
			interruptWrites(t, func() {
				if !tt.replace {
					return
				}
				require.NoError(t, os.Remove(dest))
				require.NoError(t, os.WriteFile(dest, theirs, 0o644))
			})

			err := publish(temp, dest, content, generatedProfileMode)

			require.Error(t, err)
			onDisk, readErr := os.ReadFile(dest)
			if !tt.replace {
				assert.ErrorIs(t, readErr, os.ErrNotExist,
					"a half-written profile is removed rather than left wedging the name")
				return
			}
			require.NoError(t, readErr)
			assert.Equal(t, theirs, onDisk, "only the file this publication created is removed")
		})
	}
}

func TestPublishDoesNotFallBackWhenTheNameIsTaken(t *testing.T) {
	dir := t.TempDir()
	content := []byte("profile bytes\n")
	temp := writeTempFile(t, dir, content)
	dest := filepath.Join(dir, "generated.pkl")
	// EEXIST says the name is taken, not that links are unavailable, so the
	// exclusive write must never run — here there is nothing at the
	// destination for it to trip over, so a fallback would create one.
	refuseLinks(t, syscall.EEXIST)

	err := publish(temp, dest, content, generatedProfileMode)

	assert.ErrorIs(t, err, errNameTaken)
	_, statErr := os.Lstat(dest)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "no file is written when the name is taken")
}

func TestNewTempNameIsNeverAProfile(t *testing.T) {
	root := t.TempDir()
	s := store.New(root)
	require.NoError(t, os.MkdirAll(s.ProfilesDir(), 0o755))

	seen := make(map[string]bool, 64)
	for range 64 {
		name, err := newTempName()
		require.NoError(t, err)
		assert.Regexp(t, tempNameRE, name)
		assert.False(t, seen[name], "temp names are unique")
		seen[name] = true
		assert.Error(t, store.ValidateName(strings.TrimSuffix(name, ".pkl")))
		require.NoError(t, os.WriteFile(filepath.Join(s.ProfilesDir(), name), []byte("x"), 0o600))
	}

	names, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, names, "a temp file is never listed as a profile")
}

func TestProfileVerifierChecksTheConnectionItResolvesTo(t *testing.T) {
	rendered := writeRendered(t, testEndpoint, testUUIDA, cliAuth("", ""))
	classic := filepath.Join(t.TempDir(), "classic.pkl")
	require.NoError(t, os.WriteFile(classic, []byte(store.StubTemplate), 0o600))
	broken := filepath.Join(t.TempDir(), "broken.pkl")
	require.NoError(t, os.WriteFile(broken, []byte("not a config at all"), 0o600))

	tests := []struct {
		name         string
		path         string
		endpoint     string
		installation string
		wantErr      bool
	}{
		{name: "the intended connection", path: rendered, endpoint: testEndpoint, installation: testUUIDA},
		{name: "another endpoint", path: rendered, endpoint: testOtherOrigin, installation: testUUIDA, wantErr: true},
		{name: "another installation", path: rendered, endpoint: testEndpoint, installation: testUUIDB, wantErr: true},
		{name: "not a hosted connection", path: classic, endpoint: testEndpoint, installation: testUUIDA, wantErr: true},
		{name: "not loadable", path: broken, endpoint: testEndpoint, installation: testUUIDA, wantErr: true},
		{
			name: "absent", path: filepath.Join(t.TempDir(), "absent.pkl"),
			endpoint: testEndpoint, installation: testUUIDA, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newProfileVerifier().Verify(tt.path, tt.endpoint, tt.installation)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
