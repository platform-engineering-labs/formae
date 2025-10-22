// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit
// +build unit

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

type WrappedCommandResponse struct {
	SubmitCommandResponse *apimodel.SubmitCommandResponse
	Error                 error
}

type WrappedExtractResponse struct {
	Forma *pkgmodel.Forma
	Error error
}

type WrappedListResponse struct {
	ListCommandStatusResponse *apimodel.ListCommandStatusResponse
	Error                     error
}

type FakeMetastructure struct {
	applyResponses   []WrappedCommandResponse
	destroyResponses []WrappedCommandResponse
	extractResponses []WrappedExtractResponse
	listResponses    []WrappedListResponse
}

func (m *FakeMetastructure) ApplyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.applyResponses[0]
	m.applyResponses = m.applyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) DestroyForma(forma *pkgmodel.Forma, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.destroyResponses[0]
	m.destroyResponses = m.destroyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) DestroyByQuery(query string, config *config.FormaCommandConfig, clientID string) (*apimodel.SubmitCommandResponse, error) {
	nextResponse := m.destroyResponses[0]
	m.destroyResponses = m.destroyResponses[1:]

	return nextResponse.SubmitCommandResponse, nextResponse.Error
}

func (m *FakeMetastructure) ListFormaCommandStatus(commandID string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error) {
	nextResponse := m.listResponses[0]
	m.listResponses = m.listResponses[1:]

	return nextResponse.ListCommandStatusResponse, nextResponse.Error
}

func (m *FakeMetastructure) ExtractResources(query string) (*pkgmodel.Forma, error) {
	nextResponse := m.extractResponses[0]
	m.extractResponses = m.extractResponses[1:]

	return nextResponse.Forma, nextResponse.Error
}

func (m *FakeMetastructure) ForceSync() error {
	return nil
}

func (m *FakeMetastructure) ForceDiscovery() error {
	return nil
}

func (m *FakeMetastructure) Stats() (*apimodel.Stats, error) {
	return &apimodel.Stats{
		Version: "1.0.0",
		AgentID: "test-agent",
	}, nil
}

func TestServer_ApplyFormaSuccessResponse(t *testing.T) {
	meta := &FakeMetastructure{}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "1234"}, nil}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		responseBody := rec.Body.Bytes()
		var commandResult apimodel.SubmitCommandResponse
		err = json.Unmarshal(responseBody, &commandResult)
		assert.NoError(t, err)
		assert.Equal(t, "1234", commandResult.CommandID)
	}
}

func TestServer_ApplyFormaConflictingResourcesError(t *testing.T) {
	meta := &FakeMetastructure{}
	conflict := apimodel.FormaConflictingCommandsError{
		ConflictingCommands: []apimodel.Command{
			apimodel.Command{
				CommandID: "forma_cmd1",
				Command:   "apply",
				State:     "InProgress",
				// Duration:  5,
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "bucket-1",
						ResourceType:  "AWS::S3::Bucket",
						StackName:     "stack-1",
					},
				},
			},
			apimodel.Command{
				CommandID: "forma_cmd2",
				Command:   "apply",
				State:     "InProgress",
				// Duration:  3,
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "bucket-2",
						ResourceType:  "AWS::S3::Bucket",
						StackName:     "stack-2",
					},
				},
			},
		},
	}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, conflict}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusConflict, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.ConflictingCommands, errorResponse.ErrorType)
		assert.Equal(t, 2, len(errorResponse.Data.ConflictingCommands))
	}
}

func TestServer_ApplyFormaPatchRejectedErrorError(t *testing.T) {
	meta := &FakeMetastructure{}
	rejectedResult := apimodel.FormaPatchRejectedError{}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, rejectedResult}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.FormaPatchRejectedError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.PatchRejected, errorResponse.ErrorType)
	}
}

func TestServer_ApplyFormaCyclesDetectedError(t *testing.T) {
	meta := &FakeMetastructure{}
	cyclesDetectedResult := apimodel.FormaCyclesDetectedError{}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, cyclesDetectedResult}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.FormaCyclesDetectedError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.CyclesDetected, errorResponse.ErrorType)
	}
}

func TestServer_ApplyFormaResourceNotFoundError(t *testing.T) {
	meta := &FakeMetastructure{}
	resourceNotFound := apimodel.FormaReferencedResourcesNotFoundError{
		MissingResources: []*pkgmodel.Resource{
			{Label: "missing-resource-1", Stack: "stack-1", Type: "AWS::S3::Bucket"},
			{Label: "missing-resource-2", Stack: "stack-2", Type: "AWS::DynamoDB::Table"},
		},
	}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, resourceNotFound}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.FormaReferencedResourcesNotFoundError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.ReferencedResourcesNotFound, errorResponse.ErrorType)
		assert.Equal(t, 2, len(errorResponse.Data.MissingResources))
	}
}

func TestServer_ApplyFormaStackReferenceNotFoundError(t *testing.T) {
	meta := &FakeMetastructure{}
	stackRefNotFound := apimodel.StackReferenceNotFoundError{
		StackLabel: "my-missing-stack",
	}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, stackRefNotFound}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.StackReferenceNotFoundError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.StackReferenceNotFound, errorResponse.ErrorType)
		assert.Equal(t, "my-missing-stack", errorResponse.Data.StackLabel)
	}
}

func TestServer_ApplyFormaTargetReferenceNotFoundError(t *testing.T) {
	meta := &FakeMetastructure{}
	targetRefNotFound := apimodel.TargetReferenceNotFoundError{
		TargetLabel: "my-missing-target",
	}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, targetRefNotFound}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.TargetReferenceNotFoundError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.TargetReferenceNotFound, errorResponse.ErrorType)
		assert.Equal(t, "my-missing-target", errorResponse.Data.TargetLabel)
	}
}

func TestServer_ApplyFormaUnexpectedError(t *testing.T) {
	meta := &FakeMetastructure{}
	meta.applyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, fmt.Errorf("unexpected error")}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "patch")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	err = server.SubmitFormaCommand(c)

	if assert.Error(t, err) {
		httpError, ok := err.(*echo.HTTPError)
		if assert.True(t, ok, "Expected HTTP error") {
			assert.Equal(t, http.StatusInternalServerError, httpError.Code)
			assert.Equal(t, "unexpected error", httpError.Message)
		}
	}
}

func TestServer_DestroyFormaSuccessResponse(t *testing.T) {
	meta := &FakeMetastructure{}
	meta.destroyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{CommandID: "1234"}, nil}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "destroy")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		body := rec.Body.Bytes()
		var commandResult apimodel.SubmitCommandResponse
		err = json.Unmarshal(body, &commandResult)
		assert.NoError(t, err)
		assert.Equal(t, "1234", commandResult.CommandID)
	}
}

func TestServer_DestroyFormaConflictingResourcesError(t *testing.T) {
	meta := &FakeMetastructure{}
	conflict := apimodel.FormaConflictingCommandsError{
		ConflictingCommands: []apimodel.Command{
			{
				CommandID: "forma_cmd1",
				Command:   "destroy",
				State:     "Failed",
				// Duration:  12,
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "bucket-1",
						ResourceType:  "AWS::S3::Bucket",
						StackName:     "stack-1",
					},
				},
			},
		},
	}

	meta.destroyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, conflict}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "destroy")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	err = server.SubmitFormaCommand(c)
	if assert.NoError(t, err) {
		assert.Equal(t, http.StatusConflict, rec.Code)
		body := rec.Body.Bytes()

		var errorResponse apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]
		err = json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.ConflictingCommands, errorResponse.ErrorType)
		assert.Equal(t, 1, len(errorResponse.Data.ConflictingCommands))
	}
}

func TestServer_DestroyByQuerySuccessResponse(t *testing.T) {
	meta := &FakeMetastructure{}
	meta.destroyResponses = []WrappedCommandResponse{{&apimodel.SubmitCommandResponse{
		CommandID: "1234",
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
			Command: apimodel.Command{
				CommandID: "1234",
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						StackName: "test",
					},
				},
			},
		},
	}, nil}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "destroy")
	_ = writer.WriteField("simulate", "false")

	part, err := writer.CreateFormFile("file", "forma.json")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}

	jsonData, err := json.Marshal(&pkgmodel.Forma{})
	if err != nil {
		t.Fatalf("failed to marshal JSON: %v", err)
	}
	_, err = part.Write(jsonData)
	if err != nil {
		t.Fatalf("failed to write JSON data to form file: %v", err)
	}
	writer.Close()

	req := httptest.NewRequest("POST", "/commands", body)
	req.Header.Set("Client-ID", "test-client-id")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.SubmitFormaCommand(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		body := rec.Body.Bytes()
		var commandResult apimodel.SubmitCommandResponse
		err := json.Unmarshal(body, &commandResult)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(commandResult.Simulation.Command.ResourceUpdates))
		assert.Equal(t, "test", commandResult.Simulation.Command.ResourceUpdates[0].StackName)
	}
}

func TestServer_ExtractResourcesInvalidQueryError(t *testing.T) {
	meta := &FakeMetastructure{}
	invalidQuery := apimodel.InvalidQueryError{
		Reason: "wrong syntax",
	}
	meta.extractResponses = []WrappedExtractResponse{{nil, invalidQuery}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/resources?query=invalid-query", nil)
	req.Header.Set("Client-ID", "test-client-id")

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ListResources(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		body := rec.Body.Bytes()
		var errorResponse apimodel.ErrorResponse[apimodel.InvalidQueryError]
		err := json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.InvalidQuery, errorResponse.ErrorType)
		assert.Equal(t, "wrong syntax", errorResponse.Data.Reason)
	}
}

func TestServer_ListCommandStatusInvalidQueryError(t *testing.T) {
	meta := &FakeMetastructure{}
	invalidQuery := apimodel.InvalidQueryError{
		Reason: "wrong syntax",
	}
	meta.listResponses = []WrappedListResponse{{nil, invalidQuery}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest("GET", "/commands/status?commandId=invalid-query", nil)
	req.Header.Set("Client-ID", "test-client-id")

	rec := httptest.NewRecorder()
	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ListCommandStatus(c)) {
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		body := rec.Body.Bytes()
		var errorResponse apimodel.ErrorResponse[apimodel.InvalidQueryError]
		err := json.Unmarshal(body, &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.InvalidQuery, errorResponse.ErrorType)
		assert.Equal(t, "wrong syntax", errorResponse.Data.Reason)
	}
}
