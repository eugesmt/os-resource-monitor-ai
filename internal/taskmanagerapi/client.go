package taskmanagerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/metrics"
)

const (
	defaultHTTPTimeout = 5 * time.Second
	defaultMaxBody     = 2 << 20
	maxHistorySamples  = 120
	minSummaryRange    = 3 * time.Minute
	maxSummaryRange    = 7 * 24 * time.Hour
)

// Client is a bounded, read-only client for the Task Manager HTTP API.
type Client struct {
	baseURL          *url.URL
	httpClient       *http.Client
	maxResponseBytes int64
}

// NewClient creates a client using the default timeout and response bound.
func NewClient(baseURL string) (*Client, error) {
	return NewClientWithHTTPClient(baseURL, &http.Client{Timeout: defaultHTTPTimeout}, defaultMaxBody)
}

// NewClientWithHTTPClient is intended for tests and callers that need to
// provide a custom transport or timeout.
func NewClientWithHTTPClient(baseURL string, httpClient *http.Client, maxResponseBytes int64) (*Client, error) {
	if httpClient == nil {
		return nil, errors.New("http client is nil")
	}
	if maxResponseBytes <= 0 {
		return nil, errors.New("max response size must be positive")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse API URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("API URL must use http or https and include a host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("API URL must not contain user info, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{baseURL: u, httpClient: httpClient, maxResponseBytes: maxResponseBytes}, nil
}

// Snapshot fetches the latest system snapshot.
func (c *Client) Snapshot(ctx context.Context) (collector.SystemSnapshot, error) {
	var snapshot collector.SystemSnapshot
	if err := c.getJSON(ctx, "/api/snapshot", &snapshot); err != nil {
		return collector.SystemSnapshot{}, err
	}
	return snapshot, nil
}

// History fetches at most samples snapshots from the history endpoint.
func (c *Client) History(ctx context.Context, samples int) ([]collector.SystemSnapshot, error) {
	if samples < 1 || samples > maxHistorySamples {
		return nil, fmt.Errorf("history samples must be between 1 and %d", maxHistorySamples)
	}
	path := "/api/history?limit=" + url.QueryEscape(strconv.Itoa(samples))
	var response struct {
		Items []collector.SystemSnapshot `json:"items"`
	}
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

// MetricsSummary fetches an aggregate for the inclusive requested time range.
// The server receives an absolute range so the MCP layer and direct API users
// share one validation and timezone contract.
func (c *Client) MetricsSummary(ctx context.Context, from, to time.Time) (metrics.Summary, error) {
	from, to = from.UTC(), to.UTC()
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return metrics.Summary{}, errors.New("metrics summary range must have from before to")
	}
	if to.After(time.Now().UTC()) {
		return metrics.Summary{}, errors.New("metrics summary range cannot end in the future")
	}
	duration := to.Sub(from)
	if duration < minSummaryRange || duration > maxSummaryRange {
		return metrics.Summary{}, errors.New("metrics summary range must be between 3 minutes and 7 days")
	}
	query := url.Values{}
	query.Set("from", from.Format(time.RFC3339Nano))
	query.Set("to", to.Format(time.RFC3339Nano))
	var summary metrics.Summary
	if err := c.getJSON(ctx, "/api/metrics/summary?"+query.Encode(), &summary); err != nil {
		return metrics.Summary{}, err
	}
	return summary, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, output any) error {
	u := *c.baseURL
	path, query, _ := strings.Cut(endpoint, "?")
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	u.RawQuery = query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request %s returned HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read response %s: %w", path, err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return fmt.Errorf("response %s exceeds %d-byte limit", path, c.maxResponseBytes)
	}
	if len(body) == 0 {
		return fmt.Errorf("response %s is empty", path)
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode response %s: %w", path, err)
	}
	return nil
}
