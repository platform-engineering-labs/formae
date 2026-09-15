// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type metadataMetastructure struct {
	apitest.FakeMetastructure
	message string
}

func (m *metadataMetastructure) ApplyForma(_ *pkgmodel.Forma, cfg *config.FormaCommandConfig, _, _, _ string) (*apimodel.SubmitCommandResponse, error) {
	m.message = cfg.Message
	return &apimodel.SubmitCommandResponse{CommandID: "command", Simulation: apimodel.Simulation{ChangesRequired: true}}, nil
}

func TestSubmissionPassesCommandMessage(t *testing.T) {
	meta := &metadataMetastructure{}
	server := NewServer(t.Context(), meta, nil, nil, nil, nil)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"command": "apply", "mode": "reconcile", "message": "Keep incident capacity"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(`{"Resources":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, CommandsRoute, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Client-ID", "client")
	if err = server.SubmitFormaCommand(server.echo.NewContext(req, httptest.NewRecorder())); err != nil {
		t.Fatal(err)
	}
	if meta.message != "Keep incident capacity" {
		t.Fatalf("message=%q", meta.message)
	}
}
