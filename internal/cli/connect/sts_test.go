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

	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
)

// fakeSTS serves GetCallerIdentity for the given caller.
func fakeSTS(t *testing.T, account, arn string) *httptest.Server {
	t.Helper()
	body := fmt.Sprintf(`<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>%s</Arn>
    <UserId>AIDAEXAMPLE</UserId>
    <Account>%s</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>request-id</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`, arn, account)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	restore := stsEndpoint
	stsEndpoint = srv.URL
	t.Cleanup(func() { stsEndpoint = restore })
	return srv
}

// seedAWSConfig writes a shared config with the given profile body and static
// credentials, and isolates the run from the machine's own AWS environment.
func seedAWSConfig(t *testing.T, profileBody string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte(profileBody), 0o600))
	credentialsPath := filepath.Join(dir, "credentials")
	require.NoError(t, os.WriteFile(credentialsPath, []byte(`[test]
aws_access_key_id = AKIAEXAMPLEEXAMPLEXX
aws_secret_access_key = examplesecretexamplesecretexamplesecret
`), 0o600))
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

func TestVerifyCaller_AcceptsAMatchingCommercialCaller(t *testing.T) {
	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")

	caller, err := verifyCaller(context.Background(), "test", "", testAccount)

	require.NoError(t, err)
	assert.Equal(t, testAccount, caller.Account)
	assert.Equal(t, "arn:aws:iam::"+testAccount+":user/dev", caller.Arn)
	assert.Equal(t, "eu-west-1", caller.Cfg.Region, "the profile's region loads when no flag is passed")
}

func TestVerifyCaller_TheRegionFlagBeatsTheProfileRegion(t *testing.T) {
	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")

	caller, err := verifyCaller(context.Background(), "test", "us-east-1", testAccount)

	require.NoError(t, err)
	assert.Equal(t, "us-east-1", caller.Cfg.Region)
}

// Credentials with no region configured anywhere is an ordinary AWS setup, and
// nothing this path touches is regional: it asks STS who the credentials belong
// to and then creates an IAM role, both global. A region is defaulted so such a
// profile still resolves, rather than being refused over a preference no call
// downstream ever reads.
func TestVerifyCaller_NoRegionAnywhereDefaultsRatherThanRefusing(t *testing.T) {
	seedAWSConfig(t, "[profile test]\n")
	fakeSTS(t, testAccount, "arn:aws:iam::"+testAccount+":user/dev")

	caller, err := verifyCaller(context.Background(), "test", "", testAccount)

	require.NoError(t, err)
	assert.Equal(t, testAccount, caller.Account)
	assert.Equal(t, defaultRegion, caller.Cfg.Region)
}

func TestVerifyCaller_AMismatchNamesStatedAndActual(t *testing.T) {
	seedAWSConfig(t, "[profile test]\nregion = eu-west-1\n")
	fakeSTS(t, "999999999999", "arn:aws:iam::999999999999:user/dev")

	_, err := verifyCaller(context.Background(), "test", "", testAccount)

	failureCode(t, err, printer.CodeAccountMismatch)
	assert.Contains(t, err.Error(), testAccount)
	assert.Contains(t, err.Error(), "999999999999")
}

func TestVerifyCaller_ANonCommercialCallerIsRefused(t *testing.T) {
	seedAWSConfig(t, "[profile test]\nregion = us-gov-west-1\n")
	fakeSTS(t, testAccount, "arn:aws-us-gov:iam::"+testAccount+":user/dev")

	_, err := verifyCaller(context.Background(), "test", "", testAccount)

	failureCode(t, err, printer.CodeUnsupportedPartition)
}

// An expired SSO session is the one failure whose remedy is a command the
// user can paste; everything else passes through untouched.
func TestClassifySSO(t *testing.T) {
	err := classifySSO(fmt.Errorf("load config: %w", &ssocreds.InvalidTokenError{}), "corp-dev")

	failureCode(t, err, printer.CodeSSOLoginRequired)
	var f *printer.Failure
	require.ErrorAs(t, err, &f)
	assert.Equal(t, "aws sso login --profile corp-dev", f.Details["command"])

	plain := fmt.Errorf("something else")
	assert.Equal(t, plain, classifySSO(plain, "corp-dev"))
}
