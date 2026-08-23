// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package connect

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// `connect aws profiles` is a local, read-only listing: every profile the
// shared AWS config names, plus the account its credentials resolve to (or
// why that could not be determined). It takes no cloud credentials of its
// own beyond the profile it is reading, and no control-plane session: these
// tests never seed a formae profile, so a run that reached openControlPlane
// would fail for lack of one.

// seedProfilesConfig writes a shared config naming the given profiles and
// isolates the run from the machine's own AWS environment. body is the
// config file verbatim (`[profile x]` sections); credentials pairs a profile
// name with a distinct static access key, so a profile that should reach the
// stub STS server has something to sign with.
func seedProfilesConfig(t *testing.T, body string, credentialProfiles ...string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	var creds string
	for i, p := range credentialProfiles {
		creds += fmt.Sprintf("[%s]\naws_access_key_id = AKIAEXAMPLE%02d\naws_secret_access_key = examplesecretexamplesecretexample%02d\n\n", p, i, i)
	}
	credentialsPath := filepath.Join(dir, "credentials")
	require.NoError(t, os.WriteFile(credentialsPath, []byte(creds), 0o600))

	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsPath)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, k := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		k := k
		if old, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
		require.NoError(t, os.Unsetenv(k))
	}
}

// runAWSProfiles runs `connect aws profiles` with args appended.
func runAWSProfiles(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runConnect(t, append([]string{"aws", "profiles"}, args...)...)
}

// The document a consumer branches on: three profiles, one resolved, one
// unavailable for lacking a region, one unavailable for an expired SSO
// session (simulated through the loadAWSConfig seam, since a real expired
// token cache is not something a hermetic test can seed). Order mirrors
// listAWSProfiles' own sort.
func TestConnectAWSProfiles_MachineDocument(t *testing.T) {
	seedProfilesConfig(t, `[profile blue-admin]
region = eu-west-1

[profile no-region]

[profile sandbox]
region = eu-west-1
`, "blue-admin", "sandbox")

	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")

	restoreLoad := loadAWSConfig
	loadAWSConfig = func(ctx context.Context, profile, region string) (aws.Config, error) {
		if profile == "sandbox" {
			return aws.Config{}, &ssocreds.InvalidTokenError{}
		}
		return restoreLoad(ctx, profile, region)
	}
	t.Cleanup(func() { loadAWSConfig = restoreLoad })

	out, err := runAWSProfiles(t, machineArgs()...)
	require.NoError(t, err, "out: %s", out)

	doc := decodeDoc(t, out)
	assert.Equal(t, "awsProfiles", doc["phase"])
	assert.Equal(t, float64(2), doc["schemaVersion"])
	assert.Equal(t, []any{}, doc["warnings"])

	rows, ok := doc["profiles"].([]any)
	require.True(t, ok, "profiles is not an array: %s", out)
	require.Len(t, rows, 3)

	blueAdmin := rows[0].(map[string]any)
	assert.Equal(t, "blue-admin", blueAdmin["name"])
	assert.Equal(t, testAccount, blueAdmin["account"])
	_, unavailablePresent := blueAdmin["unavailable"]
	assert.False(t, unavailablePresent, "a resolved profile must not carry an unavailable key")

	noRegion := rows[1].(map[string]any)
	assert.Equal(t, "no-region", noRegion["name"])
	assert.Equal(t, "no region is configured for this profile", noRegion["unavailable"])
	_, accountPresent := noRegion["account"]
	assert.False(t, accountPresent, "an unavailable profile must not carry an account key")

	sandbox := rows[2].(map[string]any)
	assert.Equal(t, "sandbox", sandbox["name"])
	assert.Equal(t, "the SSO session has expired", sandbox["unavailable"])
}

// No profiles at all is not an empty array pretending to be a listing: the
// human path says so plainly, and the machine document still carries a
// non-null (empty) array rather than omitting the field.
func TestConnectAWSProfiles_NoProfilesAtAll(t *testing.T) {
	seedProfilesConfig(t, "")

	out, err := runAWSProfiles(t)
	require.NoError(t, err, "out: %s", out)
	assert.Equal(t, "No AWS profiles were found in the local shared config.\n", out)

	out, err = runAWSProfiles(t, machineArgs()...)
	require.NoError(t, err, "out: %s", out)
	doc := decodeDoc(t, out)
	rows, ok := doc["profiles"].([]any)
	require.True(t, ok, "profiles must be an array, not null: %s", out)
	assert.Empty(t, rows)
}

// Human output names each profile with its account, and marks an unavailable
// one clearly with its reason, rather than a bare name or a raw AWS error.
func TestConnectAWSProfiles_HumanOutput(t *testing.T) {
	seedProfilesConfig(t, `[profile blue-admin]
region = eu-west-1

[profile sandbox]
region = eu-west-1
`, "blue-admin")

	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")

	restoreLoad := loadAWSConfig
	loadAWSConfig = func(ctx context.Context, profile, region string) (aws.Config, error) {
		if profile == "sandbox" {
			return aws.Config{}, &ssocreds.InvalidTokenError{}
		}
		return restoreLoad(ctx, profile, region)
	}
	t.Cleanup(func() { loadAWSConfig = restoreLoad })

	out, err := runAWSProfiles(t)
	require.NoError(t, err, "out: %s", out)

	assert.Contains(t, out, "blue-admin  "+testAccount)
	assert.Contains(t, out, "sandbox  unavailable: the SSO session has expired")
	assert.NotContains(t, out, "schemaVersion")
}

// A single dead profile must not hang the whole read: its resolution gets
// its own bounded timeout, so the run finishes and reports that one profile
// as unavailable rather than blocking on it.
func TestConnectAWSProfiles_APerProfileTimeoutBoundsAHungResolution(t *testing.T) {
	seedProfilesConfig(t, `[profile slow]
region = eu-west-1
`, "slow")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult><Arn>arn:aws:iam::` + testAccount + `:user/dev</Arn><UserId>x</UserId><Account>` + testAccount + `</Account></GetCallerIdentityResult>
  <ResponseMetadata><RequestId>r</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`))
	}))
	t.Cleanup(srv.Close)
	restoreEndpoint := stsEndpoint
	stsEndpoint = srv.URL
	t.Cleanup(func() { stsEndpoint = restoreEndpoint })

	restoreTimeout := profileResolveTimeout
	profileResolveTimeout = 20 * time.Millisecond
	t.Cleanup(func() { profileResolveTimeout = restoreTimeout })

	start := time.Now()
	out, err := runAWSProfiles(t, machineArgs()...)
	elapsed := time.Since(start)
	require.NoError(t, err, "out: %s", out)

	assert.Less(t, elapsed, 250*time.Millisecond, "the hung profile must not make the whole read wait for it")

	doc := decodeDoc(t, out)
	rows := doc["profiles"].([]any)
	require.Len(t, rows, 1)
	row := rows[0].(map[string]any)
	assert.Equal(t, "slow", row["name"])
	assert.Equal(t, "could not resolve this profile's credentials", row["unavailable"])
}

// The reason text a consumer sees is derived from the failure's kind, never
// the raw AWS SDK error: an undeclared error gets the same generic reason as
// any other unclassified failure, not its own message echoed onto the wire.
func TestUnavailableReason(t *testing.T) {
	assert.Equal(t, "the SSO session has expired",
		unavailableReason(printer.Fail(printer.CodeSSOLoginRequired, "the AWS SSO session for this profile has expired", nil)))
	assert.Equal(t, "no region is configured for this profile",
		unavailableReason(printer.Fail(printer.CodeProvisionFailed, "no region: pass --region or set one on the AWS profile", nil)))
	assert.Equal(t, "the credentials belong to a non-commercial AWS partition",
		unavailableReason(printer.Fail(printer.CodeUnsupportedPartition, "the credentials belong to a non-commercial AWS partition, which connect does not support", nil)))
	assert.Equal(t, "could not resolve this profile's credentials",
		unavailableReason(fmt.Errorf("operation error STS: GetCallerIdentity, https response error StatusCode: 403, RequestID: abc, api error InvalidClientTokenId: secret-looking-value")))
}

// The command is nested under `aws`, since profiles are AWS-specific while
// connect itself is not, and it carries only the shared output flags: no
// account/quick-create/role-arn/profile-aws/region/no-input, which belong to
// provisioning, not a local read.
func TestStructure_ProfilesIsRegisteredUnderAWSAndCarriesNoProvisioningFlags(t *testing.T) {
	parent := ConnectCmd()
	aws := findSub(t, parent, "aws")
	profiles := findSub(t, aws, "profiles")

	assert.NotNil(t, profiles.Flags().Lookup("output-consumer"), "profiles must own the output-consumer flag")
	assert.NotNil(t, profiles.Flags().Lookup("output-schema"), "profiles must own the output-schema flag")

	for _, flag := range []string{"account", "quick-create", "provider-exists", "role-arn", "profile-aws", "region", "no-input"} {
		assert.Nil(t, profiles.Flags().Lookup(flag), "flag %q must not exist on connect aws profiles", flag)
	}
}

// A positional argument is rejected: profiles takes none.
func TestStructure_ProfilesTakesNoPositionalArguments(t *testing.T) {
	out, err := runAWSProfiles(t, "unexpected-arg")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected-arg")
	assert.NotContains(t, out, "schemaVersion")
}
