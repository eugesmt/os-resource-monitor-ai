package taskmanagerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSnapshotAndHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/snapshot":
			fmt.Fprint(w, `{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","cpu_percent":42.5}`)
		case "/api/history":
			if r.URL.Query().Get("limit") != "2" {
				t.Errorf("limit = %q, want 2", r.URL.Query().Get("limit"))
			}
			fmt.Fprint(w, `{"items":[{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","cpu_percent":42.5}]}`)
		case "/api/metrics/summary":
			if r.URL.Query().Get("from") != "2026-09-24T10:00:00Z" || r.URL.Query().Get("to") != "2026-09-24T11:00:00Z" {
				t.Errorf("summary query = %s, want absolute range", r.URL.RawQuery)
			}
			fmt.Fprint(w, `{"requested_from":"2026-09-24T10:00:00Z","requested_to":"2026-09-24T11:00:00Z","actual_coverage":{"from":"2026-09-24T10:00:00Z","to":"2026-09-24T11:00:00Z","samples":1,"rollups":1},"missing_data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithHTTPClient(server.URL, server.Client(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SnapshotID != "snap-1" || snapshot.CPUPercent != 42.5 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	items, err := client.History(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SnapshotID != "snap-1" {
		t.Fatalf("history = %#v", items)
	}
	summary, err := client.MetricsSummary(context.Background(), time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Coverage.Samples != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	for _, baseURL := range []string{"", "file:///tmp/taskmanager", "http://", "http://user@example.com", "http://example.com?token=secret"} {
		if _, err := NewClient(baseURL); err == nil {
			t.Errorf("NewClient(%q) succeeded", baseURL)
		}
	}
	if _, err := NewClientWithHTTPClient("http://example.com", nil, 1); err == nil {
		t.Error("nil HTTP client accepted")
	}
	if _, err := NewClientWithHTTPClient("http://example.com", http.DefaultClient, 0); err == nil {
		t.Error("zero response limit accepted")
	}
}

func TestClientMetricsSummaryRejectsInvalidRange(t *testing.T) {
	client, err := NewClientWithHTTPClient("http://example.com", http.DefaultClient, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tests := []struct {
		from time.Time
		to   time.Time
	}{
		{from: now, to: now.Add(-time.Minute)},
		{from: now.Add(-2 * time.Minute), to: now},
		{from: now.Add(-8 * 24 * time.Hour), to: now},
		{from: now.Add(-time.Hour), to: now.Add(time.Minute)},
	}
	for _, test := range tests {
		if _, err := client.MetricsSummary(context.Background(), test.from, test.to); err == nil {
			t.Fatalf("range %s..%s was accepted", test.from, test.to)
		}
	}
}

func TestClientRejectsBadStatusAndJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{name: "status", body: `{"error":"upstream"}`, code: http.StatusServiceUnavailable},
		{name: "json", body: `{not-json`, code: http.StatusOK},
		{name: "empty", body: "", code: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClientWithHTTPClient(server.URL, server.Client(), 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Snapshot(context.Background())
			if err == nil {
				t.Fatal("Snapshot succeeded")
			}
			if strings.Contains(err.Error(), "upstream") {
				t.Error("upstream response body leaked into error")
			}
		})
	}
}

func TestClientBoundsResponseBodyAndHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 33)))
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client(), 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
	if _, err := client.History(context.Background(), 0); err == nil {
		t.Error("zero history samples accepted")
	}
	if _, err := client.History(context.Background(), 121); err == nil {
		t.Error("too many history samples accepted")
	}
}

func TestClientPropagatesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Snapshot(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
