package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"system-monitor-ai/internal/storage"
)

func TestParseMetricsSummaryRangeAcceptsRelativeAndAbsoluteModes(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	from, to, err := parseMetricsSummaryRange(url.Values{"last_minutes": {"60"}}, now)
	if err != nil {
		t.Fatalf("relative range error = %v", err)
	}
	if !from.Equal(now.Add(-time.Hour)) || !to.Equal(now) {
		t.Fatalf("relative range = %s..%s", from, to)
	}

	from, to, err = parseMetricsSummaryRange(url.Values{
		"from": {"2026-09-25T10:00:00+03:00"},
		"to":   {"2026-09-25T12:00:00+03:00"},
	}, now)
	if err != nil {
		t.Fatalf("absolute range error = %v", err)
	}
	if !from.Equal(time.Date(2026, 9, 25, 7, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("absolute range = %s..%s", from, to)
	}
}

func TestParseMetricsSummaryRangeRejectsInvalidQueries(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	tests := []url.Values{
		{},
		{"last_minutes": {"2"}},
		{"last_minutes": {"10081"}},
		{"last_minutes": {"60"}, "from": {"2026-09-25T10:00:00Z"}, "to": {"2026-09-25T11:00:00Z"}},
		{"from": {"2026-09-25T10:00:00Z"}},
		{"to": {"2026-09-25T11:00:00Z"}},
		{"from": {"2026-09-25T10:00:00Z"}, "to": {"2026-09-25T10:02:00Z"}},
		{"from": {"2026-09-18T11:59:00Z"}, "to": {"2026-09-25T12:00:00Z"}},
		{"from": {"2026-09-25T13:00:00Z"}, "to": {"2026-09-25T14:00:00Z"}},
		{"from": {"bad"}, "to": {"2026-09-25T11:00:00Z"}},
		{"from": {"2026-09-25T10:00:00Z", "2026-09-25T10:01:00Z"}, "to": {"2026-09-25T11:00:00Z"}},
		{"last_minutes": {"60"}, "unexpected": {"true"}},
	}
	for _, query := range tests {
		t.Run(query.Encode(), func(t *testing.T) {
			if _, _, err := parseMetricsSummaryRange(query, now); err == nil {
				t.Fatalf("query %s was accepted", query.Encode())
			}
		})
	}
}

func TestParseMetricsSummaryRangeErrorDoesNotEchoTimestampOrQuery(t *testing.T) {
	query := url.Values{"from": {strings.Repeat("secret", 100)}, "to": {"bad"}}
	_, _, err := parseMetricsSummaryRange(query, time.Now().UTC())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestMetricsSummaryEndpointRejectsInvalidQueryBeforeStorage(t *testing.T) {
	runtime := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	mux := http.NewServeMux()
	RegisterRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}})

	for _, target := range []string{
		"/api/metrics/summary",
		"/api/metrics/summary?last_minutes=2",
		"/api/metrics/summary?last_minutes=60&from=2026-09-24T10:00:00Z&to=2026-09-24T11:00:00Z",
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want %d; body=%s", target, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		}
	}
}
