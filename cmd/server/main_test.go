package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"system-monitor-ai/internal/collector"
	"system-monitor-ai/internal/server"
	"system-monitor-ai/internal/storage"
)

func TestRegisterRoutesIncludesEmbeddedMCP(t *testing.T) {
	runtime := server.NewRuntime(collector.New(1, nil), storage.NewRingBuffer(2), nil)
	mux := http.NewServeMux()
	registerRoutes(mux, runtime, fstest.MapFS{"index.html": {Data: []byte("ok")}}, "127.0.0.1:8080", "")
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "embedded-test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools := 0
	for tool, err := range session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name != "" {
			tools++
		}
	}
	if tools < 4 {
		t.Fatalf("embedded MCP tools = %d, want at least 4", tools)
	}
}

func TestSystemMonitorMCPReportDirectoryUsesCanonicalEnvOnly(t *testing.T) {
	t.Setenv("SYSTEM_MONITOR_MCP_REPORT_DIR", "/tmp/system-monitor-reports")
	if got := systemMonitorMCPReportDirectory(); got != "/tmp/system-monitor-reports" {
		t.Fatalf("systemMonitorMCPReportDirectory() = %q, want canonical directory", got)
	}

	t.Setenv("SYSTEM_MONITOR_MCP_REPORT_DIR", "")
	if got := systemMonitorMCPReportDirectory(); got != "" {
		t.Fatalf("systemMonitorMCPReportDirectory() = %q with empty canonical env, want empty", got)
	}
}

func TestReportDownloadBaseURLRejectsAmbiguousAddresses(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{address: "127.0.0.1:8080", want: "http://127.0.0.1:8080/reports"},
		{address: "[::1]:8080", want: "http://[::1]:8080/reports"},
		{address: ":8080"},
		{address: "0.0.0.0:8080"},
		{address: "[::]:8080"},
	}
	for _, test := range tests {
		got, ok := reportDownloadBaseURL(test.address)
		if test.want == "" {
			if ok || got != "" {
				t.Errorf("reportDownloadBaseURL(%q) = (%q, %t), want empty", test.address, got, ok)
			}
			continue
		}
		if !ok || got != test.want {
			t.Errorf("reportDownloadBaseURL(%q) = (%q, %t), want %q", test.address, got, ok, test.want)
		}
	}
}
