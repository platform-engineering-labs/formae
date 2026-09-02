// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration

package workflow_tests_local

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const authoredKeyPairForma = "internal/schema/pkl/testdata/forma/generator_keypair_test.pkl"

// A key-pair generator authored in PKL applies end to end: one draw, two
// destinations, each receiving the half its binding names. The halves must be
// the two ends of the SAME pair — one string fanned to both, or two
// independent draws, would both apply cleanly here and fail only at the
// consumer, which is exactly the silent outcome this asserts against.
func TestApplyForma_PklAuthoredKeyPair_EachHalfReachesItsDestination(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		forma, err := pkl.PKL{}.Evaluate(
			authoredKeyPairForma,
			pkgmodel.CommandApply,
			pkgmodel.FormaApplyModeReconcile,
			nil,
		)
		require.NoError(t, err, "the authored forma must evaluate")
		require.Len(t, forma.Resources, 2)
		require.Len(t, forma.Generators, 1)
		for _, r := range forma.Resources {
			require.True(t, gjson.GetBytes(r.Properties, "SecretString.$gen").Bool(),
				"precondition: eval must render each binding as a $gen envelope")
		}

		capture := &pklCreateCapture{}
		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Synchronization.Enabled = false
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, capture.overrides(), cfg)
		defer def()
		require.NoError(t, err)

		_, err = m.ApplyForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, "test-client-id", "", "")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			incomplete, loadErr := m.Datastore.LoadIncompleteFormaCommands()
			return loadErr == nil && len(incomplete) == 0
		}, 30*time.Second, 100*time.Millisecond, "the apply must reach a terminal state")

		cmds, err := m.Datastore.LoadFormaCommands()
		require.NoError(t, err)
		require.Len(t, cmds, 1)
		require.Equal(t, forma_command.CommandStateSuccess, cmds[0].State,
			"a PKL-authored key-pair binding must apply")

		privatePEM, ok := capture.valueFor("id-key-private")
		require.True(t, ok, "the provider must have been called for the private half")
		publicPEM, ok := capture.valueFor("id-key-public")
		require.True(t, ok, "the provider must have been called for the public half")

		privateBlock, _ := pem.Decode([]byte(privatePEM))
		require.NotNil(t, privateBlock, "the private half must reach the provider as PEM")
		parsedPrivate, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
		require.NoError(t, err, "the private half must be PKCS#8")
		privateKey := parsedPrivate.(*rsa.PrivateKey)

		publicBlock, _ := pem.Decode([]byte(publicPEM))
		require.NotNil(t, publicBlock, "the public half must reach the provider as PEM")
		parsedPublic, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
		require.NoError(t, err, "the public half must be PKIX")
		publicKey := parsedPublic.(*rsa.PublicKey)

		assert.Equal(t, 0, privateKey.PublicKey.N.Cmp(publicKey.N),
			"the two destinations must hold halves of one pair")
		assert.Equal(t, 2048, publicKey.N.BitLen(), "the default key size applies")
	})
}
