// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awsFiles points the enumeration at temp config/credentials files with the
// given contents; an empty string leaves the file absent.
func awsFiles(t *testing.T, config, credentials string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	credentialsPath := filepath.Join(dir, "credentials")
	if config != "" {
		require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	}
	if credentials != "" {
		require.NoError(t, os.WriteFile(credentialsPath, []byte(credentials), 0o600))
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
}

func TestListAWSProfiles_ReadsTheConfigFileConventions(t *testing.T) {
	awsFiles(t, `[default]
region = eu-west-1

[profile staging]
region = us-east-1

[profile prod]
sso_session = corp
`, "")

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Equal(t, []string{"default", "prod", "staging"}, profiles)
}

func TestListAWSProfiles_ReadsTheCredentialsFileConventions(t *testing.T) {
	awsFiles(t, "", `[dev]
aws_access_key_id = AKIA...

[default]
aws_access_key_id = AKIA...
`)

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Equal(t, []string{"default", "dev"}, profiles)
}

// An sso-session (or services) section is configuration a profile refers to,
// not a connectable identity; the profile that uses it is listed, the section
// is not.
func TestListAWSProfiles_SkipsNonProfileSections(t *testing.T) {
	awsFiles(t, `[profile corp-dev]
sso_session = corp

[sso-session corp]
sso_start_url = https://corp.awsapps.com/start

[services local]
endpoint_url = http://localhost:4566
`, "")

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Equal(t, []string{"corp-dev"}, profiles)
}

// In the config file only `[default]` and `[profile x]` are profiles: a bare
// `[x]` there is not one (that spelling belongs to the credentials file).
func TestListAWSProfiles_BareSectionsInTheConfigFileAreNotProfiles(t *testing.T) {
	awsFiles(t, `[staging]
region = us-east-1
`, "")

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Empty(t, profiles)
}

func TestListAWSProfiles_DedupesAcrossTheTwoFiles(t *testing.T) {
	awsFiles(t, `[profile dev]
region = eu-west-1

[default]
region = eu-west-1
`, `[dev]
aws_access_key_id = AKIA...

[default]
aws_access_key_id = AKIA...
`)

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Equal(t, []string{"default", "dev"}, profiles)
}

// Malformed section lines are skipped without aborting: the rest of the file
// still names profiles.
func TestListAWSProfiles_SkipsMalformedLines(t *testing.T) {
	awsFiles(t, `[profile good]
region = eu-west-1

[unclosed
not a section at all
[profile ]

[profile also-good]
`, "")

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Equal(t, []string{"also-good", "good"}, profiles)
}

func TestListAWSProfiles_AbsentFilesAreAnEmptyListNotAnError(t *testing.T) {
	awsFiles(t, "", "")

	profiles, err := listAWSProfiles()

	require.NoError(t, err)
	assert.Empty(t, profiles)
}
