//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package api

import (
	"bytes"
	"encoding/json"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/auth"
	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type resolutionMetastructure struct {
	apitest.FakeMetastructure
	options *config.FormaCommandConfig
	subject string
}

func (m *resolutionMetastructure) ApplyForma(_ *pkgmodel.Forma, c *config.FormaCommandConfig, client, subject, name string) (*apimodel.SubmitCommandResponse, error) {
	m.options = c
	m.subject = subject
	return &apimodel.SubmitCommandResponse{}, nil
}
func TestResolutionMultipartControls(t *testing.T) {
	for _, control := range []string{`{"ObservationID":"observed","Decisions":[{"ResourceID":"r","Action":"absorb"}],"ReviewID":"reviewed","IdempotencyKey":"key"}`, `{"ObservationID":"x","Typo":true}`, `null`, `{} {}`} {
		m := &resolutionMetastructure{}
		server := NewServer(t.Context(), m, nil, nil, nil, nil)
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		require.NoError(t, w.WriteField("command", "apply"))
		require.NoError(t, w.WriteField("mode", "reconcile"))
		require.NoError(t, w.WriteField("subject", "untrusted-client-subject"))
		require.NoError(t, w.WriteField("resolution", control))
		file, err := w.CreateFormFile("file", "forma.json")
		require.NoError(t, err)
		_, err = file.Write([]byte(`{}`))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		req := httptest.NewRequest(http.MethodPost, CommandsRoute, &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Client-ID", "client")
		rec := httptest.NewRecorder()
		ctx := server.echo.NewContext(req, rec)
		ctx.Set(auth.ContextKeySubject, "verified-subject")
		err = server.SubmitFormaCommand(ctx)
		if control[0:2] == `{"` && bytes.Contains([]byte(control), []byte("observed")) {
			require.NoError(t, err)
			require.NotNil(t, m.options.Resolution)
			require.Equal(t, "key", m.options.Resolution.IdempotencyKey)
			require.Equal(t, "verified-subject", m.subject)
		} else {
			require.Error(t, err)
			require.Nil(t, m.options)
		}
	}
}
func TestResolutionClientChecksCapability(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == StatsRoute {
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		calls++
	}))
	defer srv.Close()
	client := NewClient(&pkgmodel.ClassicConnection{URL: srv.URL}, nil, srv.Client())
	feature, ok := any(client).(interface {
		ApplyFormaWithResolution(*pkgmodel.Forma, pkgmodel.FormaApplyMode, bool, string, pkgmodel.DriftResolution, ...string) (*apimodel.SubmitCommandResponse, error)
	})
	require.True(t, ok, "client must expose guarded resolution transport")
	_, err := feature.ApplyFormaWithResolution(&pkgmodel.Forma{}, pkgmodel.FormaApplyModeReconcile, true, "client", pkgmodel.DriftResolution{})
	require.Error(t, err)
	require.Zero(t, calls, "an old agent must not silently ignore resolution decisions")
}

func TestResolutionTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{apimodel.DriftResolutionError{Code: "invalid-decisions", Reason: "missing choice"}, http.StatusBadRequest, "invalid-decisions"},
		{datastore.ErrStaleAdmission, http.StatusConflict, "stale-review"},
		{datastore.ErrAdmissionConflict, http.StatusConflict, "idempotency-conflict"},
		{apimodel.DriftResolutionError{Code: "desired-intent-unavailable", Reason: "recover failed creation", ResourceID: "resource", CommandID: "failed-command"}, http.StatusConflict, "desired-intent-unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			server := NewServer(t.Context(), &resolutionMetastructure{}, nil, nil, nil, nil)
			rec := httptest.NewRecorder()
			require.NoError(t, mapError(server.echo.NewContext(httptest.NewRequest(http.MethodPost, CommandsRoute, nil), rec), tc.err))
			require.Equal(t, tc.status, rec.Code)
			var response apimodel.ErrorResponse[apimodel.DriftResolutionError]
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, apimodel.DriftResolutionRejected, response.ErrorType)
			require.Contains(t, rec.Body.String(), tc.code)
		})
	}
}
