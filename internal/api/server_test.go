// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/platform-engineering-labs/formae/internal/api/apitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestServer_ApplyFormaSuccessResponse(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{
		CommandID: "1234",
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
		},
	}, nil}}

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

func TestServer_ApplyFormaNoChangesResponse(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{
		CommandID: "1234",
		Simulation: apimodel.Simulation{
			ChangesRequired: false,
		},
	}, nil}}

	server := NewServer(t.Context(), meta, nil, nil, nil, nil)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("command", "apply")
	_ = writer.WriteField("mode", "reconcile")
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
		assert.Equal(t, http.StatusOK, rec.Code)

		responseBody := rec.Body.Bytes()
		var commandResult apimodel.SubmitCommandResponse
		err = json.Unmarshal(responseBody, &commandResult)
		assert.NoError(t, err)
		assert.False(t, commandResult.Simulation.ChangesRequired)
	}
}

func TestServer_ApplyFormaConflictingResourcesError(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	conflict := apimodel.FormaConflictingCommandsError{
		ConflictingCommands: []apimodel.Command{
			{
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
			{
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
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, conflict}}

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
	meta := &apitest.FakeMetastructure{}
	rejectedResult := apimodel.FormaPatchRejectedError{}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, rejectedResult}}

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
	meta := &apitest.FakeMetastructure{}
	cyclesDetectedResult := apimodel.FormaCyclesDetectedError{}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, cyclesDetectedResult}}

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
	meta := &apitest.FakeMetastructure{}
	resourceNotFound := apimodel.FormaReferencedResourcesNotFoundError{
		MissingResources: []*pkgmodel.Resource{
			{Label: "missing-resource-1", Stack: "stack-1", Type: "AWS::S3::Bucket"},
			{Label: "missing-resource-2", Stack: "stack-2", Type: "AWS::DynamoDB::Table"},
		},
	}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, resourceNotFound}}

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
	meta := &apitest.FakeMetastructure{}
	stackRefNotFound := apimodel.StackReferenceNotFoundError{
		StackLabel: "my-missing-stack",
	}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, stackRefNotFound}}

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
	meta := &apitest.FakeMetastructure{}
	targetRefNotFound := apimodel.TargetReferenceNotFoundError{
		TargetLabel: "my-missing-target",
	}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, targetRefNotFound}}

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
	meta := &apitest.FakeMetastructure{}
	meta.ApplyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, fmt.Errorf("unexpected error")}}

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
	meta := &apitest.FakeMetastructure{}
	meta.DestroyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{
		CommandID: "1234",
		Simulation: apimodel.Simulation{
			ChangesRequired: true,
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
		err = json.Unmarshal(body, &commandResult)
		assert.NoError(t, err)
		assert.Equal(t, "1234", commandResult.CommandID)
	}
}

func TestServer_DestroyFormaConflictingResourcesError(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
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

	meta.DestroyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{}, conflict}}

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
	meta := &apitest.FakeMetastructure{}
	meta.DestroyResponses = []apitest.WrappedCommandResponse{{&apimodel.SubmitCommandResponse{
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
	meta := &apitest.FakeMetastructure{}
	invalidQuery := apimodel.InvalidQueryError{
		Reason: "wrong syntax",
	}
	meta.ExtractResponses = []apitest.WrappedExtractResponse{{nil, invalidQuery}}

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
	meta := &apitest.FakeMetastructure{}
	invalidQuery := apimodel.InvalidQueryError{
		Reason: "wrong syntax",
	}
	meta.ListResponses = []apitest.WrappedListResponse{{nil, invalidQuery}}

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

func TestServer_CancelCommands_Success(t *testing.T) {
	fakeMetastructure := &apitest.FakeMetastructure{
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-1", "cmd-2"},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), fakeMetastructure, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/cancel", nil)
	req.Header.Set("Client-ID", "test-client")
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.CancelCommands(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		body := rec.Body.Bytes()
		var response apimodel.CancelCommandResponse
		err := json.Unmarshal(body, &response)
		assert.NoError(t, err)
		assert.Equal(t, []string{"cmd-1", "cmd-2"}, response.CommandIDs)
	}
}

func TestServer_CancelCommands_WithQuery(t *testing.T) {
	fakeMetastructure := &apitest.FakeMetastructure{
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-3"},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), fakeMetastructure, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/cancel?query=stack:test", nil)
	req.Header.Set("Client-ID", "test-client")
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.CancelCommands(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		body := rec.Body.Bytes()
		var response apimodel.CancelCommandResponse
		err := json.Unmarshal(body, &response)
		assert.NoError(t, err)
		assert.Equal(t, []string{"cmd-3"}, response.CommandIDs)
	}
}

func TestServer_CancelCommands_NoCommandsFound(t *testing.T) {
	fakeMetastructure := &apitest.FakeMetastructure{
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), fakeMetastructure, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/cancel", nil)
	req.Header.Set("Client-ID", "test-client")
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.CancelCommands(c)) {
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Empty(t, rec.Body.Bytes())
	}
}

func TestServer_CancelCommands_MissingClientID(t *testing.T) {
	fakeMetastructure := &apitest.FakeMetastructure{}

	server := NewServer(context.Background(), fakeMetastructure, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/cancel", nil)
	// No Client-ID header
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	err := server.CancelCommands(c)
	assert.Error(t, err)
	httpErr, ok := err.(*echo.HTTPError)
	assert.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, httpErr.Code)
}

func TestServer_CancelCommands_WithResourceUpdateStates(t *testing.T) {
	fakeMetastructure := &apitest.FakeMetastructure{
		CancelResponses: []apitest.WrappedCancelResponse{
			{
				CancelCommandResponse: &apimodel.CancelCommandResponse{
					CommandIDs: []string{"cmd-1"},
					ResourceUpdateStates: map[string]apimodel.CancelResourceState{
						"formae://res-1": {State: "Canceled"},
						"formae://res-2": {State: "InProgress"},
						"formae://res-3": {State: "Success"},
					},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), fakeMetastructure, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/commands/cancel", nil)
	req.Header.Set("Client-ID", "test-client")
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.CancelCommands(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		body := rec.Body.Bytes()
		var response apimodel.CancelCommandResponse
		err := json.Unmarshal(body, &response)
		assert.NoError(t, err)
		assert.Equal(t, []string{"cmd-1"}, response.CommandIDs)
		assert.Len(t, response.ResourceUpdateStates, 3)
		assert.Equal(t, "Canceled", response.ResourceUpdateStates["formae://res-1"].State)
		assert.Equal(t, "InProgress", response.ResourceUpdateStates["formae://res-2"].State)
		assert.Equal(t, "Success", response.ResourceUpdateStates["formae://res-3"].State)
	}
}

func TestServer_ListTargets_Success(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	targets := []*pkgmodel.Target{
		{
			Label:        "prod-us-east-1",
			Namespace:    "AWS",
			Discoverable: true,
			Config:       json.RawMessage(`{"Region":"us-east-1"}`),
		},
		{
			Label:        "dev-us-west-2",
			Namespace:    "AWS",
			Discoverable: false,
			Config:       json.RawMessage(`{"Region":"us-west-2"}`),
		},
	}
	meta.TargetResponses = []apitest.WrappedTargetResponse{{Targets: targets, Error: nil}}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets?query=namespace:aws", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ListTargets(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response []*pkgmodel.Target
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Len(t, response, 2)
		assert.Equal(t, "prod-us-east-1", response[0].Label)
		assert.Equal(t, "AWS", response[0].Namespace)
	}
}

func TestServer_ListTargets_NoResults(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	meta.TargetResponses = []apitest.WrappedTargetResponse{{Targets: []*pkgmodel.Target{}, Error: nil}}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ListTargets(c)) {
		assert.Equal(t, http.StatusNotFound, rec.Code)
	}
}

func TestServer_ListTargets_WithQuery(t *testing.T) {
	meta := &apitest.FakeMetastructure{}
	targets := []*pkgmodel.Target{
		{
			Label:        "tailscale-main",
			Namespace:    "TAILSCALE",
			Discoverable: true,
			Config:       json.RawMessage(`{"Tailnet":"example.com"}`),
		},
	}
	meta.TargetResponses = []apitest.WrappedTargetResponse{{Targets: targets, Error: nil}}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/targets?query=namespace:tailscale+discoverable:true", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ListTargets(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response []*pkgmodel.Target
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Len(t, response, 1)
		assert.Equal(t, "TAILSCALE", response[0].Namespace)
		assert.True(t, response[0].Discoverable)
	}
}

func TestServer_ListDrift_Success(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		DriftResponses: []apitest.WrappedDriftResponse{
			{
				Drift: apimodel.ModifiedStack{
					ModifiedResources: []apimodel.ResourceModification{
						{Stack: "production", Type: "AWS::S3::Bucket", Label: "my-bucket", Operation: "update"},
						{Stack: "production", Type: "AWS::DynamoDB::Table", Label: "my-table", Operation: "delete"},
					},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks/production/drift", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("production")

	if assert.NoError(t, server.ListDrift(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response apimodel.ModifiedStack
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Len(t, response.ModifiedResources, 2)
		assert.Equal(t, "my-bucket", response.ModifiedResources[0].Label)
	}
}

func TestServer_ListDrift_NoDrift(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		DriftResponses: []apitest.WrappedDriftResponse{
			{
				Drift: apimodel.ModifiedStack{
					ModifiedResources: []apimodel.ResourceModification{},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks/production/drift", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("production")

	if assert.NoError(t, server.ListDrift(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response apimodel.ModifiedStack
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Empty(t, response.ModifiedResources)
	}
}

func TestServer_ForceReconcile_Started(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		ReconcileResponses: []apitest.WrappedReconcileResponse{
			{
				Response: &apimodel.ForceReconcileResponse{
					CommandID: "cmd-abc123",
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/production/reconcile", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("production")

	if assert.NoError(t, server.ForceReconcile(c)) {
		assert.Equal(t, http.StatusAccepted, rec.Code)

		var response apimodel.ForceReconcileResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "cmd-abc123", response.CommandID)
	}
}

func TestServer_ForceReconcile_NoDrift(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		ReconcileResponses: []apitest.WrappedReconcileResponse{
			{
				Response: &apimodel.ForceReconcileResponse{
					Message: "no drift detected",
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/production/reconcile", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("production")

	if assert.NoError(t, server.ForceReconcile(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response apimodel.ForceReconcileResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "no drift detected", response.Message)
		assert.Empty(t, response.CommandID)
	}
}

func TestServer_ForceReconcile_Conflict(t *testing.T) {
	conflict := apimodel.FormaConflictingCommandsError{
		ConflictingCommands: []apimodel.Command{
			{
				CommandID: "forma_cmd1",
				Command:   "apply",
				State:     "InProgress",
				ResourceUpdates: []apimodel.ResourceUpdate{
					{
						ResourceLabel: "bucket-1",
						ResourceType:  "AWS::S3::Bucket",
						StackName:     "production",
					},
				},
			},
		},
	}

	meta := &apitest.FakeMetastructure{
		ReconcileResponses: []apitest.WrappedReconcileResponse{
			{
				Response: nil,
				Error:    conflict,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/production/reconcile", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("production")

	if assert.NoError(t, server.ForceReconcile(c)) {
		assert.Equal(t, http.StatusConflict, rec.Code)

		var errorResponse apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]
		err := json.Unmarshal(rec.Body.Bytes(), &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.ConflictingCommands, errorResponse.ErrorType)
		assert.Equal(t, 1, len(errorResponse.Data.ConflictingCommands))
	}
}

func TestServer_ForceReconcile_PolicyRequired(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		ReconcileResponses: []apitest.WrappedReconcileResponse{
			{
				Response: nil,
				Error: apimodel.ReconcilePolicyRequiredError{
					StackLabel: "no-policy-stack",
				},
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/no-policy-stack/reconcile", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)
	c.SetParamNames("stack")
	c.SetParamValues("no-policy-stack")

	if assert.NoError(t, server.ForceReconcile(c)) {
		assert.Equal(t, http.StatusForbidden, rec.Code)

		var errorResponse apimodel.ErrorResponse[apimodel.ReconcilePolicyRequiredError]
		err := json.Unmarshal(rec.Body.Bytes(), &errorResponse)
		assert.NoError(t, err)
		assert.Equal(t, apimodel.ReconcilePolicyRequired, errorResponse.ErrorType)
		assert.Equal(t, "no-policy-stack", errorResponse.Data.StackLabel)
	}
}

func TestServer_ForceCheckTTL_StacksExpired(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		CheckTTLResponses: []apitest.WrappedCheckTTLResponse{
			{
				Response: &apimodel.ForceCheckTTLResponse{
					ExpiredStacks: []string{"stack-a", "stack-b"},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/check-ttl", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ForceCheckTTL(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response apimodel.ForceCheckTTLResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, []string{"stack-a", "stack-b"}, response.ExpiredStacks)
	}
}

func TestServer_ForceCheckTTL_NothingExpired(t *testing.T) {
	meta := &apitest.FakeMetastructure{
		CheckTTLResponses: []apitest.WrappedCheckTTLResponse{
			{
				Response: &apimodel.ForceCheckTTLResponse{
					ExpiredStacks: []string{},
				},
				Error: nil,
			},
		},
	}

	server := NewServer(context.Background(), meta, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/check-ttl", nil)
	rec := httptest.NewRecorder()

	c := server.echo.NewContext(req, rec)

	if assert.NoError(t, server.ForceCheckTTL(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)

		var response apimodel.ForceCheckTTLResponse
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Empty(t, response.ExpiredStacks)
	}
}
