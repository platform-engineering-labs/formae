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
	"time"
)

// MetricCollector queries Mimir (Prometheus-compatible) for agent resource utilization.
type MetricCollector struct {
	mimirURL string
	client   *http.Client
}

type promQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// NewMetricCollector returns a MetricCollector that targets the given Mimir base URL.
func NewMetricCollector(mimirURL string) *MetricCollector {
	return &MetricCollector{mimirURL: mimirURL, client: &http.Client{Timeout: 30 * time.Second}}
}

// CollectResourceUtilization returns peak and average CPU/memory for the agent over the given window.
func (mc *MetricCollector) CollectResourceUtilization(ctx context.Context, since, until time.Time) (*ReportResourceUtil, error) {
	util := &ReportResourceUtil{}

	window := until.Sub(since).String()

	peakCPU, err := mc.queryInstant(ctx, fmt.Sprintf(`max_over_time(process_cpu_usage{service_name="formae-agent"}[%s])`, window), until)
	if err == nil && peakCPU != nil {
		util.AgentPeakCPUPercent = *peakCPU * 100
	}

	avgCPU, err := mc.queryInstant(ctx, fmt.Sprintf(`avg_over_time(process_cpu_usage{service_name="formae-agent"}[%s])`, window), until)
	if err == nil && avgCPU != nil {
		util.AgentAvgCPUPercent = *avgCPU * 100
	}

	peakMem, err := mc.queryInstant(ctx, fmt.Sprintf(`max_over_time(process_resident_memory_bytes{service_name="formae-agent"}[%s])`, window), until)
	if err == nil && peakMem != nil {
		util.AgentPeakMemoryMB = *peakMem / 1024 / 1024
	}

	avgMem, err := mc.queryInstant(ctx, fmt.Sprintf(`avg_over_time(process_resident_memory_bytes{service_name="formae-agent"}[%s])`, window), until)
	if err == nil && avgMem != nil {
		util.AgentAvgMemoryMB = *avgMem / 1024 / 1024
	}

	return util, nil
}

// CheckActorLeak returns the net change in ergo process count over the soak window.
func (mc *MetricCollector) CheckActorLeak(ctx context.Context, soakStart, soakEnd time.Time) (int, error) {
	startCount, err := mc.queryInstant(ctx, `ergo_processes_total{service_name="formae-agent"}`, soakStart)
	if err != nil {
		return 0, fmt.Errorf("query start actor count: %w", err)
	}
	endCount, err := mc.queryInstant(ctx, `ergo_processes_total{service_name="formae-agent"}`, soakEnd)
	if err != nil {
		return 0, fmt.Errorf("query end actor count: %w", err)
	}
	if startCount == nil || endCount == nil {
		return 0, nil
	}
	return int(*endCount) - int(*startCount), nil
}

func (mc *MetricCollector) queryInstant(ctx context.Context, query string, at time.Time) (*float64, error) {
	params := url.Values{
		"query": {query},
		"time":  {fmt.Sprintf("%d", at.Unix())},
	}

	reqURL := fmt.Sprintf("%s/prometheus/api/v1/query?%s", mc.mimirURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := mc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mimir query failed: %w", err)
	}
	defer resp.Body.Close()

	var result promQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode mimir response: %w", err)
	}

	if len(result.Data.Result) == 0 {
		return nil, nil
	}

	valStr, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected value type")
	}

	var val float64
	fmt.Sscanf(valStr, "%f", &val)
	return &val, nil
}
