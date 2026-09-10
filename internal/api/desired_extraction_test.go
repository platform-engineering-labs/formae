//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package api

import (
	"fmt"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

type desiredExtractionMetastructure struct {
	apitest.FakeMetastructure
	desiredCalls, actualCalls int
}

func (m *desiredExtractionMetastructure) ExtractDesiredStacks(string) (*pkgmodel.Forma, error) {
	m.desiredCalls++
	return &pkgmodel.Forma{Stacks: []pkgmodel.Stack{{Label: "empty", ID: "stable"}}, Extraction: &pkgmodel.ExtractionContext{}}, nil
}
func (m *desiredExtractionMetastructure) ExtractResources(string) (*pkgmodel.Forma, error) {
	m.actualCalls++
	return &pkgmodel.Forma{Resources: []pkgmodel.Resource{{Label: "actual"}}}, nil
}
func TestDesiredExtractionRouting(t *testing.T) {
	meta := &desiredExtractionMetastructure{}
	server := NewServer(t.Context(), meta, nil, nil, nil, nil)
	for _, state := range []string{"desired", "wrong", "actual", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/resources?query=stack:empty&state="+state, nil)
		require.NoError(t, server.ListResources(server.echo.NewContext(req, rec)))
		if state == "wrong" {
			require.Equal(t, http.StatusBadRequest, rec.Code)
		} else {
			require.Equal(t, http.StatusOK, rec.Code)
		}
	}
	require.Equal(t, 1, meta.desiredCalls)
	require.Equal(t, 2, meta.actualCalls)
}

func TestDesiredExtractionClientRequiresCapability(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(fmt.Sprint(supported), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == StatsRoute {
					if supported {
						fmt.Fprint(w, `{"Capabilities":["desired-stack-extraction"]}`)
					} else {
						fmt.Fprint(w, `{}`)
					}
					return
				}
				requests++
				require.Equal(t, "desired", r.URL.Query().Get("state"))
				fmt.Fprint(w, `{"Stacks":[{"Label":"empty","ID":"stable"}],"Extraction":{"CompleteStacks":[{"Label":"empty","ID":"stable"}]}}`)
			}))
			defer srv.Close()
			client := NewClient(&pkgmodel.ClassicConnection{URL: srv.URL}, nil, srv.Client())
			reader, ok := any(client).(interface {
				ExtractDesiredStacks(string) (*pkgmodel.Forma, error)
			})
			require.True(t, ok)
			forma, err := reader.ExtractDesiredStacks("stack:empty")
			if supported {
				require.NoError(t, err)
				require.Len(t, forma.Stacks, 1)
				require.Equal(t, 1, requests)
			} else {
				require.Error(t, err)
				require.Zero(t, requests, "old server might silently ignore desired state")
			}
		})
	}
}
