// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/auth"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

// testSubject and testSubjectName are the fixture identity used across every
// seeded row below, standing in for a verified caller the auth middleware
// would have placed on the request context.
const (
	testSubject     = "11111111-1111-4111-8111-111111111111"
	testSubjectName = "dpanders"
)

// subjectContextShim is an echo.MiddlewareFunc that seeds the context keys
// the real auth middleware (auth.NewAuthMiddleware) sets on an allowed
// request, without wiring up a live auth plugin. Tests use it to reproduce
// exactly what a handler sees after authentication, then invoke the handler
// directly.
func subjectContextShim(subject, subjectName string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(auth.ContextKeySubject, subject)
			c.Set(auth.ContextKeySubjectName, subjectName)
			return next(c)
		}
	}
}

// newFormaCommandRequest builds a multipart /commands request carrying the
// given form fields, matching the shape SubmitFormaCommand expects. It
// optionally attaches an empty Forma file, mirroring the pattern already
// used throughout server_test.go.
func newFormaCommandRequest(t *testing.T, form map[string]string, includeFile bool) *http.Request {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range form {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("failed to write form field %q: %v", key, err)
		}
	}

	if includeFile {
		part, err := writer.CreateFormFile("file", "forma.json")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		jsonData, err := json.Marshal(&pkgmodel.Forma{})
		if err != nil {
			t.Fatalf("failed to marshal JSON: %v", err)
		}
		if _, err := part.Write(jsonData); err != nil {
			t.Fatalf("failed to write JSON data to form file: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// subjectSeamCase exercises one command-creating endpoint end to end through
// the handler, seeding (or withholding) the auth context values, then
// asserting what the metastructure call actually received.
type subjectSeamCase struct {
	name        string
	subject     string
	subjectName string
	seedContext bool // false reproduces classic mode: no auth plugin, no middleware, no context values.

	setup      func(meta *apitest.FakeMetastructure)
	newRequest func(t *testing.T) *http.Request
	setParams  func(c echo.Context)
	invoke     func(server *Server, c echo.Context) error
	recorded   func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject
}

func TestServer_CommandEndpoints_ThreadAuthenticatedSubject(t *testing.T) {
	cases := []subjectSeamCase{
		{
			name:        "apply threads the authenticated subject to ApplyForma",
			subject:     testSubject,
			subjectName: testSubjectName,
			seedContext: true,
			setup: func(meta *apitest.FakeMetastructure) {
				meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "cmd-apply"}, nil}}
			},
			newRequest: func(t *testing.T) *http.Request {
				return newFormaCommandRequest(t, map[string]string{"command": "apply", "mode": "patch", "simulate": "false"}, true)
			},
			invoke:   func(server *Server, c echo.Context) error { return server.SubmitFormaCommand(c) },
			recorded: func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject { return meta.RecordedApplySubjects },
		},
		{
			name:        "destroy threads the authenticated subject to DestroyForma",
			subject:     testSubject,
			subjectName: testSubjectName,
			seedContext: true,
			setup: func(meta *apitest.FakeMetastructure) {
				meta.DestroyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "cmd-destroy"}, nil}}
			},
			newRequest: func(t *testing.T) *http.Request {
				return newFormaCommandRequest(t, map[string]string{"command": "destroy", "simulate": "false"}, true)
			},
			invoke:   func(server *Server, c echo.Context) error { return server.SubmitFormaCommand(c) },
			recorded: func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject { return meta.RecordedDestroySubjects },
		},
		{
			name:        "destroy-by-query threads the authenticated subject to DestroyByQuery",
			subject:     testSubject,
			subjectName: testSubjectName,
			seedContext: true,
			setup: func(meta *apitest.FakeMetastructure) {
				meta.DestroyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "cmd-destroy-query"}, nil}}
			},
			newRequest: func(t *testing.T) *http.Request {
				return newFormaCommandRequest(t, map[string]string{"command": "destroy", "simulate": "false", "query": "stack:test"}, false)
			},
			invoke: func(server *Server, c echo.Context) error { return server.SubmitFormaCommand(c) },
			recorded: func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject {
				return meta.RecordedDestroyByQuerySubjects
			},
		},
		{
			name:        "force-reconcile threads the authenticated subject to ForceAutoReconcile",
			subject:     testSubject,
			subjectName: testSubjectName,
			seedContext: true,
			setup: func(meta *apitest.FakeMetastructure) {
				meta.ReconcileResponses = []apitest.WrappedReconcileResponse{{Response: &apimodel.ForceReconcileResponse{CommandID: "cmd-reconcile"}, Error: nil}}
			},
			newRequest: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/stacks/production/reconcile", nil)
			},
			setParams: func(c echo.Context) {
				c.SetParamNames("stack")
				c.SetParamValues("production")
			},
			invoke:   func(server *Server, c echo.Context) error { return server.ForceReconcile(c) },
			recorded: func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject { return meta.RecordedReconcileSubjects },
		},
		{
			// Classic mode: no auth plugin configured, so the middleware never
			// runs and neither context key is ever set. The handler must not
			// panic or error on the missing values, and must pass empty strings
			// through rather than fabricating an identity.
			name:        "no subject on the context passes empty strings through (classic mode)",
			subject:     "",
			subjectName: "",
			seedContext: false,
			setup: func(meta *apitest.FakeMetastructure) {
				meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "cmd-classic"}, nil}}
			},
			newRequest: func(t *testing.T) *http.Request {
				return newFormaCommandRequest(t, map[string]string{"command": "apply", "mode": "patch", "simulate": "false"}, true)
			},
			invoke:   func(server *Server, c echo.Context) error { return server.SubmitFormaCommand(c) },
			recorded: func(meta *apitest.FakeMetastructure) []apitest.RecordedSubject { return meta.RecordedApplySubjects },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := &apitest.FakeMetastructure{}
			tc.setup(meta)

			server := NewServer(t.Context(), meta, nil, nil, nil, nil)

			req := tc.newRequest(t)
			rec := httptest.NewRecorder()
			c := server.echo.NewContext(req, rec)
			if tc.setParams != nil {
				tc.setParams(c)
			}

			handler := echo.HandlerFunc(func(c echo.Context) error { return tc.invoke(server, c) })
			if tc.seedContext {
				handler = subjectContextShim(tc.subject, tc.subjectName)(handler)
			}

			assert.NoError(t, handler(c))

			recorded := tc.recorded(meta)
			if assert.Len(t, recorded, 1, "expected exactly one call to have reached the metastructure") {
				assert.Equal(t, apitest.RecordedSubject{Subject: tc.subject, SubjectName: tc.subjectName}, recorded[0])
			}
		})
	}
}
