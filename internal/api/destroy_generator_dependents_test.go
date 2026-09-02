// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// A destroy refused because a generator it deletes still has dependents has to
// reach the caller as the typed refusal, not as an opaque error. The client's
// destroy entry points decode a narrower set of statuses than its apply one
// does — 400 and 409 only, where apply also accepts 422 — so the status the
// server maps this refusal to has to be one of that narrower set. This runs
// the whole way through the real server and the real client to pin that.
func TestDestroyForma_GeneratorHasDependents_DecodesOnTheDestroyPath(t *testing.T) {
	refusal := apimodel.FormaGeneratorHasDependentsError{
		Dependents: []apimodel.GeneratorDependent{{
			GeneratorLabel: "db-password",
			GeneratorStack: "platform",
			ResourceLabel:  "api-secret",
			ResourceType:   "AWS::SecretsManager::Secret",
			Stack:          "web",
		}},
	}

	fake := &apitest.FakeMetastructure{
		DestroyResponses: []apitest.WrappedCommandResponse{
			{SubmitCommandResponse: &apimodel.SubmitCommandResponse{}, Error: refusal},
			{SubmitCommandResponse: &apimodel.SubmitCommandResponse{}, Error: refusal},
		},
	}

	srv := NewServer(t.Context(), fake, nil, nil, nil, nil)
	baseURL := apitest.NewTestServer(t, srv.Handler())
	client := NewClient(&pkgmodel.ClassicConnection{URL: baseURL, Port: 80}, nil, nil)

	assertDecoded := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		errResp, ok := err.(*apimodel.ErrorResponse[apimodel.FormaGeneratorHasDependentsError])
		require.True(t, ok, "the refusal must decode into its typed error, got %T: %v", err, err)
		assert.Equal(t, apimodel.GeneratorHasDependents, errResp.ErrorType)
		require.Len(t, errResp.Data.Dependents, 1)
		assert.Equal(t, "api-secret", errResp.Data.Dependents[0].ResourceLabel)
		assert.Equal(t, "web", errResp.Data.Dependents[0].Stack)
		assert.Equal(t, "db-password", errResp.Data.Dependents[0].GeneratorLabel)
	}

	_, err := client.DestroyForma(&pkgmodel.Forma{}, false, "abort", "test-client-id")
	assertDecoded(t, err)

	_, err = client.DestroyByQuery("stack:platform", false, "abort", "test-client-id")
	assertDecoded(t, err)
}
