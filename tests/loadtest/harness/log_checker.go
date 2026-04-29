// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var warningAllowlist = []string{
	"rate limiter",
	"token bucket",
	"throttling",
}

// LogChecker queries Loki for error and warning log entries emitted by the agent.
type LogChecker struct {
	lokiURL string
	client  *http.Client
}

type lokiQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// NewLogChecker returns a LogChecker that targets the given Loki base URL.
func NewLogChecker(lokiURL string) *LogChecker {
	return &LogChecker{lokiURL: lokiURL, client: &http.Client{Timeout: 30 * time.Second}}
}

// CheckForErrors returns all log lines at error level emitted between since and until.
func (lc *LogChecker) CheckForErrors(ctx context.Context, since, until time.Time) ([]string, error) {
	query := `{service_name=~"formae.*"} |= "level=error" or "level=ERROR"`
	return lc.queryLoki(ctx, query, since, until)
}

// CheckForWarnings returns warning log lines that are not in the allowlist.
func (lc *LogChecker) CheckForWarnings(ctx context.Context, since, until time.Time) ([]string, error) {
	query := `{service_name=~"formae.*"} |= "level=warn" or "level=WARN"`
	lines, err := lc.queryLoki(ctx, query, since, until)
	if err != nil {
		return nil, err
	}
	var unexpected []string
	for _, line := range lines {
		allowed := false
		lower := strings.ToLower(line)
		for _, pattern := range warningAllowlist {
			if strings.Contains(lower, pattern) {
				allowed = true
				break
			}
		}
		if !allowed {
			unexpected = append(unexpected, line)
		}
	}
	return unexpected, nil
}

func (lc *LogChecker) queryLoki(ctx context.Context, query string, since, until time.Time) ([]string, error) {
	params := url.Values{
		"query": {query},
		"start": {fmt.Sprintf("%d", since.UnixNano())},
		"end":   {fmt.Sprintf("%d", until.UnixNano())},
		"limit": {"1000"},
	}

	reqURL := fmt.Sprintf("%s/loki/api/v1/query_range?%s", lc.lokiURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := lc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki query failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki returned status %d", resp.StatusCode)
	}

	var result lokiQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode loki response: %w", err)
	}

	var lines []string
	for _, stream := range result.Data.Result {
		for _, entry := range stream.Values {
			if len(entry) >= 2 {
				lines = append(lines, entry[1])
			}
		}
	}
	return lines, nil
}
