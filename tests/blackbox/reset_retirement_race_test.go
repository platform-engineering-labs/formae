// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build integration || property

package blackbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/api"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestResetRefreshesStackScopeAfterRetirementRace(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired-before-plan", true: "retired-before-admission"}[stale], func(t *testing.T) {
			var lists, submits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/stacks" {
					stacks := []*pkgmodel.Stack{{Label: "survivor"}}
					if lists.Add(1) == 1 {
						stacks = append(stacks, &pkgmodel.Stack{Label: "retiring"})
					}
					_ = json.NewEncoder(w).Encode(stacks)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/commands" {
					http.Error(w, "unexpected request", 500)
					return
				}
				file, _, err := r.FormFile("file")
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				defer file.Close()
				var forma pkgmodel.Forma
				if err = json.NewDecoder(file).Decode(&forma); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				if submits.Add(1) == 1 {
					if len(forma.Stacks) != 2 {
						http.Error(w, "initial scope incomplete", 500)
						return
					}
					if stale {
						w.WriteHeader(http.StatusConflict)
						_ = json.NewEncoder(w).Encode(apimodel.ErrorResponse[apimodel.DriftResolutionError]{ErrorType: apimodel.DriftResolutionRejected, Data: apimodel.DriftResolutionError{Code: "stale-review"}})
					} else {
						w.WriteHeader(http.StatusUnprocessableEntity)
						_ = json.NewEncoder(w).Encode(apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]{ErrorType: apimodel.EmptyStackRejected, Data: apimodel.FormaEmptyStackRejectedError{EmptyStacks: []string{"retiring"}}})
					}
					return
				}
				if len(forma.Stacks) != 1 || forma.Stacks[0].Label != "survivor" || len(forma.Resources) != 0 {
					http.Error(w, "reset reused stale scope", 500)
					return
				}
				_ = json.NewEncoder(w).Encode(apimodel.SubmitCommandResponse{})
			}))
			defer server.Close()
			h := &TestHarness{client: api.NewClient(&pkgmodel.HostedConnection{Endpoint: server.URL}, nil, server.Client())}
			h.clearDesiredStateBeforeDestroy(t)
			require.Equal(t, int32(2), lists.Load())
			require.Equal(t, int32(2), submits.Load())
		})
	}
}

func TestResetRefreshesDesiredExtractionAfterStackRetires(t *testing.T) {
	var lists, extracts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/stats":
			_ = json.NewEncoder(w).Encode(apimodel.Stats{Capabilities: []string{"desired-stack-extraction"}})
		case "/api/v1/stacks":
			stacks := []*pkgmodel.Stack{{Label: "survivor"}}
			if lists.Add(1) == 1 {
				stacks = append(stacks, &pkgmodel.Stack{Label: "retiring"})
			}
			_ = json.NewEncoder(w).Encode(stacks)
		case "/api/v1/resources":
			if extracts.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(apimodel.ErrorResponse[apimodel.InvalidQueryError]{ErrorType: apimodel.InvalidQuery, Data: apimodel.InvalidQueryError{Reason: `managed stack "retiring" does not exist`}})
				return
			}
			if r.URL.Query().Get("query") != `stack:"survivor"` {
				http.Error(w, "stale extraction scope", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(pkgmodel.Forma{Extraction: &pkgmodel.ExtractionContext{CompleteStacks: []pkgmodel.Stack{{Label: "survivor"}}}})
		default:
			http.Error(w, "unexpected request", 500)
		}
	}))
	defer server.Close()
	h := &TestHarness{client: api.NewClient(&pkgmodel.HostedConnection{Endpoint: server.URL}, nil, server.Client())}
	result := h.extractRemainingDesiredState(t)
	require.NotNil(t, result)
	require.Empty(t, result.Resources)
	require.Equal(t, int32(2), lists.Load())
	require.Equal(t, int32(2), extracts.Load())
}

func TestResetScopeRetriesAreBoundedAndRejectOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		extract  bool
		status   int
		response any
		attempts int32
	}{
		{"stale-exhausted", false, http.StatusConflict, apimodel.ErrorResponse[apimodel.DriftResolutionError]{ErrorType: apimodel.DriftResolutionRejected, Data: apimodel.DriftResolutionError{Code: "stale-review"}}, 3},
		{"retirement-exhausted", false, http.StatusUnprocessableEntity, apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]{ErrorType: apimodel.EmptyStackRejected, Data: apimodel.FormaEmptyStackRejectedError{EmptyStacks: []string{"selected"}}}, 3},
		{"unrelated-empty-stack", false, http.StatusUnprocessableEntity, apimodel.ErrorResponse[apimodel.FormaEmptyStackRejectedError]{ErrorType: apimodel.EmptyStackRejected, Data: apimodel.FormaEmptyStackRejectedError{EmptyStacks: []string{"unselected"}}}, 1},
		{"different-resolution-error", false, http.StatusConflict, apimodel.ErrorResponse[apimodel.DriftResolutionError]{ErrorType: apimodel.DriftResolutionRejected, Data: apimodel.DriftResolutionError{Code: "invalid-resolution"}}, 1},
		{"extraction-retirement-exhausted", true, http.StatusBadRequest, apimodel.ErrorResponse[apimodel.InvalidQueryError]{ErrorType: apimodel.InvalidQuery, Data: apimodel.InvalidQueryError{Reason: `managed stack "selected" does not exist`}}, 3},
		{"different-invalid-query", true, http.StatusBadRequest, apimodel.ErrorResponse[apimodel.InvalidQueryError]{ErrorType: apimodel.InvalidQuery, Data: apimodel.InvalidQueryError{Reason: "invalid selector"}}, 1},
		{"unselected-missing-stack", true, http.StatusBadRequest, apimodel.ErrorResponse[apimodel.InvalidQueryError]{ErrorType: apimodel.InvalidQuery, Data: apimodel.InvalidQueryError{Reason: `managed stack "unselected" does not exist`}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts, lists atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/stacks":
					lists.Add(1)
					_ = json.NewEncoder(w).Encode([]*pkgmodel.Stack{{Label: "selected"}})
				case "/api/v1/stats":
					_ = json.NewEncoder(w).Encode(apimodel.Stats{Capabilities: []string{"desired-stack-extraction"}})
				default:
					attempts.Add(1)
					w.WriteHeader(tc.status)
					_ = json.NewEncoder(w).Encode(tc.response)
				}
			}))
			defer server.Close()
			h := &TestHarness{client: api.NewClient(&pkgmodel.HostedConnection{Endpoint: server.URL}, nil, server.Client())}
			var err error
			if tc.extract {
				_, err = h.readRemainingDesiredScope()
			} else {
				_, err = h.submitEmptyDesiredScope()
			}
			require.Error(t, err, "exhausted and unrelated failures must never be reported as empty desired state")
			if tc.attempts > 1 {
				require.ErrorContains(t, err, "did not stabilize after 3 attempts")
			}
			require.Equal(t, tc.attempts, attempts.Load())
			require.Equal(t, tc.attempts, lists.Load(), "every retry must refresh the selected stack scope")
		})
	}
}
