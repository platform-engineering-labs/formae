// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"syscall"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"resty.dev/v3"
)

type Client struct {
	endpoint string
	resty    *resty.Client
}

func NewClient(cfg pkgmodel.APIConfig, auth http.Header, net *http.Client) *Client {
	client := resty.New()

	if net != nil {
		client = resty.NewWithClient(net)
	}

	if auth != nil {
		client.SetHeader("Authorization", auth.Get("Authorization"))
	}

	return &Client{
		endpoint: fmt.Sprintf("%s:%d", cfg.URL, cfg.Port),
		resty:    client,
	}
}

func (c *Client) Stats() (*apimodel.Stats, error) {
	resp, err := c.resty.R().
		Get(c.endpoint + "/api/v1/stats")
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return nil, syscall.ECONNREFUSED
		}

		return nil, err
	}

	//nolint:errcheck
	defer resp.Body.Close()

	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	var stats apimodel.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &stats, nil
}

func (c *Client) WaitOnAvailable() bool {
	for {
		resp, _ := c.resty.R().Get(c.endpoint + "/api/v1/health")
		if resp.StatusCode() == 200 {
			return true
		}

		time.Sleep(1 * time.Second)
	}
}

func (c *Client) ApplyForma(forma *pkgmodel.Forma, mode pkgmodel.FormaApplyMode, simulate bool, clientID string, force bool) (*apimodel.SubmitCommandResponse, error) {
	var status apimodel.SubmitCommandResponse

	formaJSON, err := json.Marshal(&forma)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal forma to JSON: %w", err)
	}
	formaBuffer := bytes.NewReader(formaJSON)

	const formFieldName = "file"
	const clientFileName = "forma.json"

	resp, err := c.resty.R().
		SetResult(&status).
		SetContentType("multipart/form-data").
		SetHeader("Client-ID", clientID).
		SetFormData(map[string]string{
			"command":  "apply",
			"mode":     string(mode),
			"simulate": fmt.Sprintf("%t", simulate),
			"force":    fmt.Sprintf("%t", force),
		}).
		SetFileReader(formFieldName, clientFileName, formaBuffer).
		Post(c.endpoint + "/api/v1/commands")
	if err != nil {
		return nil, fmt.Errorf("failed to submit command: %w", err)
	}

	//nolint:errcheck
	defer resp.Body.Close()

	switch resp.StatusCode() {
	case http.StatusAccepted:
		return &status, nil
	case http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity:
		return c.parseSubmitCommandErrorResponse(resp.Body)
	default:
		return nil, fmt.Errorf("unexpected response code from the forma agent: %d - %s", resp.StatusCode(), resp.String())
	}
}

func (c *Client) DestroyForma(forma *pkgmodel.Forma, simulate bool, clientID string) (*apimodel.SubmitCommandResponse, error) {
	var status apimodel.SubmitCommandResponse

	formaJSON, err := json.Marshal(&forma)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal forma to JSON: %w", err)
	}
	formaBuffer := bytes.NewReader(formaJSON)

	const formFieldName = "file"
	const clientFileName = "forma.json"

	resp, err := c.resty.R().
		SetResult(&status).
		SetContentType("multipart/form-data").
		SetHeader("Client-ID", clientID).
		SetFormData(map[string]string{
			"command":  "destroy",
			"simulate": fmt.Sprintf("%t", simulate),
		}).
		SetFileReader(formFieldName, clientFileName, formaBuffer).
		Post(c.endpoint + "/api/v1/commands")
	if err != nil {
		return nil, fmt.Errorf("failed to destroy forma: %w", err)
	}

	//nolint:errcheck
	defer resp.Body.Close()

	switch resp.StatusCode() {
	case http.StatusAccepted:
		return &status, nil
	case http.StatusBadRequest, http.StatusConflict:
		return c.parseSubmitCommandErrorResponse(resp.Body)
	default:
		return nil, fmt.Errorf("unexpected response code from the forma agent: %d - %s", resp.StatusCode(), resp.String())
	}
}

func (c *Client) DestroyByQuery(query string, simulate bool, clientID string) (*apimodel.SubmitCommandResponse, error) {
	var status apimodel.SubmitCommandResponse
	resp, err := c.resty.R().
		SetResult(&status).
		SetContentType("multipart/form-data").
		SetHeader("Client-ID", clientID).
		SetFormData(map[string]string{
			"command":  "destroy",
			"query":    query,
			"simulate": fmt.Sprintf("%t", simulate),
		}).
		Post(c.endpoint + "/api/v1/commands")
	if err != nil {
		return nil, fmt.Errorf("failed to destroy forma: %w", err)
	}

	//nolint:errcheck
	defer resp.Body.Close()

	switch resp.StatusCode() {
	case http.StatusAccepted:
		return &status, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("no resources found to destroy")
	case http.StatusBadRequest, http.StatusConflict:
		return c.parseSubmitCommandErrorResponse(resp.Body)
	default:
		return nil, fmt.Errorf("unexpected response code from the forma agent: %d - %s", resp.StatusCode(), resp.String())
	}
}

func (c *Client) CancelCommands(query string, clientID string) (*apimodel.CancelCommandResponse, error) {
	var result apimodel.CancelCommandResponse

	req := c.resty.R().
		SetResult(&result).
		SetHeader("Client-ID", clientID)

	if query != "" {
		req.SetQueryParam("query", query)
	}

	resp, err := req.Post(c.endpoint + "/api/v1/commands/cancel")
	if err != nil {
		return nil, fmt.Errorf("failed to cancel commands: %w", err)
	}

	//nolint:errcheck
	defer resp.Body.Close()

	switch resp.StatusCode() {
	case http.StatusAccepted:
		return &result, nil
	case http.StatusNotFound:
		return nil, nil
	case http.StatusBadRequest:
		return c.parseCancelCommandsErrorResponse(resp.Body)
	default:
		return nil, fmt.Errorf("unexpected response code from the forma agent: %d - %s", resp.StatusCode(), resp.String())
	}
}

// parseSubmitCommandErrorResponse parses the ErrorResponse[T] from the API and returns an appropriate error
func (c *Client) parseSubmitCommandErrorResponse(body io.ReadCloser) (*apimodel.SubmitCommandResponse, error) {
	bodyBytes, readErr := io.ReadAll(body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read error response body: %w", readErr)
	}

	var baseError struct {
		Error apimodel.APIError `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &baseError); err != nil {
		return nil, fmt.Errorf("failed to parse error type: %w", err)
	}

	switch baseError.Error {
	case apimodel.ConflictingCommands:
		var errResp apimodel.ErrorResponse[apimodel.FormaConflictingCommandsError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse ConflictingCommands error: %w", err)
		}
		return nil, &errResp

	case apimodel.ReconcileRejected:
		var errResp apimodel.ErrorResponse[apimodel.FormaReconcileRejectedError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse ReconcileRejected error: %w", err)
		}
		return nil, &errResp

	case apimodel.PatchRejected:
		var errResp apimodel.ErrorResponse[apimodel.FormaPatchRejectedError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse PatchRejected error: %w", err)
		}
		return nil, &errResp

	case apimodel.CyclesDetected:
		var errResp apimodel.ErrorResponse[apimodel.FormaCyclesDetectedError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse CyclesDetected error: %w", err)
		}
		return nil, &errResp

	case apimodel.ReferencedResourcesNotFound:
		var errResp apimodel.ErrorResponse[apimodel.FormaReferencedResourcesNotFoundError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse ReferencedResourcesNotFound error: %w", err)
		}
		return nil, &errResp

	case apimodel.TargetAlreadyExists:
		var errResp apimodel.ErrorResponse[apimodel.TargetAlreadyExistsError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse TargetAlreadyExists error: %w", err)
		}
		return nil, &errResp

	case apimodel.RequiredFieldMissingOnCreate:
		var errResp apimodel.ErrorResponse[apimodel.RequiredFieldMissingOnCreateError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse RequiredFieldMissingOnCreate error: %w", err)
		}
		return nil, &errResp

	case apimodel.InvalidQuery:
		var errResp apimodel.ErrorResponse[apimodel.InvalidQueryError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse InvalidQueryError error: %w", err)
		}
		return nil, &errResp

	case apimodel.StackReferenceNotFound:
		var errResp apimodel.ErrorResponse[apimodel.StackReferenceNotFoundError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse StackReferenceNotFound error: %w", err)
		}
		return nil, &errResp

	case apimodel.TargetReferenceNotFound:
		var errResp apimodel.ErrorResponse[apimodel.TargetReferenceNotFoundError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse TargetReferenceNotFound error: %w", err)
		}
		return nil, &errResp

	default:
		return nil, fmt.Errorf("unknown error type: %s", baseError.Error)
	}
}

// parseListCommandStatusErrorResponse parses the ErrorResponse[T] from the API and returns an appropriate error
func (c *Client) parseListCommandStatusErrorResponse(body io.ReadCloser) (*apimodel.ListCommandStatusResponse, error) {
	bodyBytes, readErr := io.ReadAll(body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read error response body: %w", readErr)
	}

	var baseError struct {
		Error apimodel.APIError `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &baseError); err != nil {
		return nil, fmt.Errorf("failed to parse error type: %w", err)
	}

	switch baseError.Error {
	case apimodel.InvalidQuery:
		var errResp apimodel.ErrorResponse[apimodel.InvalidQueryError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse InvalidQueryError error: %w", err)
		}
		return nil, &errResp

	default:
		return nil, fmt.Errorf("unknown error type: %s", baseError.Error)
	}
}

// parseListResourcesErrorResponse parses the ErrorResponse[T] from the API and returns an appropriate error
func (c *Client) parseListResourcesErrorResponse(body io.ReadCloser) (*pkgmodel.Forma, error) {
	bodyBytes, readErr := io.ReadAll(body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read error response body: %w", readErr)
	}

	var baseError struct {
		Error apimodel.APIError `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &baseError); err != nil {
		return nil, fmt.Errorf("failed to parse error type: %w", err)
	}

	switch baseError.Error {
	case apimodel.InvalidQuery:
		var errResp apimodel.ErrorResponse[apimodel.InvalidQueryError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse InvalidQueryError error: %w", err)
		}
		return nil, &errResp

	default:
		return nil, fmt.Errorf("unknown error type: %s", baseError.Error)
	}
}

// parseCancelCommandsErrorResponse parses the ErrorResponse[T] from the API and returns an appropriate error
func (c *Client) parseCancelCommandsErrorResponse(body io.ReadCloser) (*apimodel.CancelCommandResponse, error) {
	bodyBytes, readErr := io.ReadAll(body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read error response body: %w", readErr)
	}

	var baseError struct {
		Error apimodel.APIError `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &baseError); err != nil {
		return nil, fmt.Errorf("failed to parse error type: %w", err)
	}

	switch baseError.Error {
	case apimodel.InvalidQuery:
		var errResp apimodel.ErrorResponse[apimodel.InvalidQueryError]
		if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
			return nil, fmt.Errorf("failed to parse InvalidQueryError error: %w", err)
		}
		return nil, &errResp

	default:
		return nil, fmt.Errorf("unknown error type: %s", baseError.Error)
	}
}

func (c *Client) GetFormaCommandsStatus(query string, clientID string, n int) (*apimodel.ListCommandStatusResponse, error) {
	var status apimodel.ListCommandStatusResponse
	resp, err := c.resty.R().
		SetResult(&status).
		SetHeader("Client-ID", clientID).
		SetQueryParam("query", query).
		SetQueryParam("max_results", fmt.Sprintf("%d", n)).
		Get(c.endpoint + "/api/v1/commands/status")
	if err != nil {
		return nil, err
	}

	//nolint:errcheck
	defer resp.Body.Close()
	switch resp.StatusCode() {
	case http.StatusOK:
		return &status, nil
	case http.StatusBadRequest:
		return c.parseListCommandStatusErrorResponse(resp.Body)
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("error getting status: %v", resp.Status())
	}
}

func (c *Client) ExtractResources(query string) (*pkgmodel.Forma, error) {
	resp, err := c.resty.R().
		SetQueryParam("query", query).
		Get(c.endpoint + "/api/v1/resources")
	if err != nil {
		return nil, fmt.Errorf("failed to extract resources: %w", err)
	}
	//nolint:errcheck
	defer resp.Body.Close()
	switch resp.StatusCode() {
	case http.StatusOK:
		var resources pkgmodel.Forma
		if err := json.NewDecoder(resp.Body).Decode(&resources); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		return &resources, nil
	case http.StatusNotFound:
		return nil, nil
	case http.StatusBadRequest:
		return c.parseListResourcesErrorResponse(resp.Body)

	default:
		return nil, fmt.Errorf("unexpected response code from the forma agent: %d - %s", resp.StatusCode(), resp.String())
	}
}

func (c *Client) ForceSync() error {
	_, err := c.resty.R().
		Post(c.endpoint + "/api/v1/admin/synchronize")
	if err != nil {
		return fmt.Errorf("failed to force synchronization: %w", err)
	}
	return nil
}

func (c *Client) ForceDiscover() error {
	_, err := c.resty.R().
		Post(c.endpoint + "/api/v1/admin/discover")
	if err != nil {
		return fmt.Errorf("failed to force discovery: %w", err)
	}
	return nil
}
