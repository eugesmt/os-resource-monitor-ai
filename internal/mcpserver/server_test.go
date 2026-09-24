package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"taskmanager-ai/internal/taskmanagerapi"
)

func TestServerRegistersOnlyExpectedToolsAndSchemas(t *testing.T) {
	server, clientSession := connectedServer(t, snapshotJSON())
	_ = server
	tools := map[string]*mcp.Tool{}
	for tool, err := range clientSession.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = tool
	}
	want := []string{"get_metrics_summary", "get_resource_history", "get_system_overview", "get_top_processes"}
	got := make([]string, 0, len(tools))
	for name := range tools {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for _, name := range []string{"get_top_processes", "get_resource_history"} {
		var schema map[string]any
		data, err := json.Marshal(tools[name].InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false {
			t.Errorf("%s allows additional properties: %s", name, tools[name].InputSchema)
		}
		if _, ok := schema["required"]; !ok {
			t.Errorf("%s has no required fields", name)
		}
	}
	var topSchema map[string]any
	if err := json.Unmarshal(mustJSON(t, tools["get_top_processes"].InputSchema), &topSchema); err != nil {
		t.Fatal(err)
	}
	topProperties := topSchema["properties"].(map[string]any)
	if got := topProperties["sort_by"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []any{"cpu", "memory"}) {
		t.Errorf("sort_by enum = %#v", got)
	}
	limitSchema := topProperties["limit"].(map[string]any)
	if limitSchema["minimum"] != float64(1) || limitSchema["maximum"] != float64(20) {
		t.Errorf("limit bounds = %#v", limitSchema)
	}
	var historySchema map[string]any
	if err := json.Unmarshal(mustJSON(t, tools["get_resource_history"].InputSchema), &historySchema); err != nil {
		t.Fatal(err)
	}
	historyProperties := historySchema["properties"].(map[string]any)
	wantMetrics := []any{"cpu", "memory", "load", "network_receive", "network_send", "disk_read", "disk_write"}
	if got := historyProperties["metric"].(map[string]any)["enum"]; !reflect.DeepEqual(got, wantMetrics) {
		t.Errorf("metric enum = %#v", got)
	}
	samplesSchema := historyProperties["samples"].(map[string]any)
	if samplesSchema["minimum"] != float64(1) || samplesSchema["maximum"] != float64(120) {
		t.Errorf("samples bounds = %#v", samplesSchema)
	}
	var summarySchema map[string]any
	if err := json.Unmarshal(mustJSON(t, tools["get_metrics_summary"].InputSchema), &summarySchema); err != nil {
		t.Fatal(err)
	}
	if summarySchema["additionalProperties"] != false || len(summarySchema["oneOf"].([]any)) != 2 {
		t.Errorf("summary schema = %#v", summarySchema)
	}
	summaryProperties := summarySchema["properties"].(map[string]any)
	lastMinutes := summaryProperties["last_minutes"].(map[string]any)
	if lastMinutes["minimum"] != float64(3) || lastMinutes["maximum"] != float64(10080) {
		t.Errorf("last_minutes bounds = %#v", lastMinutes)
	}
	defer clientSession.Close()
}

func TestTopProcessesProtocolCallSortsAndLimits(t *testing.T) {
	_, session := connectedServer(t, snapshotJSON())
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_top_processes", Arguments: map[string]any{"sort_by": "cpu", "limit": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %#v", result.Content)
	}
	var output TopProcessesOutput
	decodeStructured(t, result.StructuredContent, &output)
	if len(output.Processes) != 2 || output.Processes[0].PID != 2 || output.Processes[1].PID != 3 {
		t.Fatalf("processes = %#v", output.Processes)
	}
	if output.Processes[0].Name != "worker" {
		t.Errorf("first process = %#v", output.Processes[0])
	}
}

func TestStreamableHTTPProtocolCall(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/snapshot" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, snapshotJSON())
	}))
	defer apiServer.Close()
	api, err := taskmanagerapi.NewClientWithHTTPClient(apiServer.URL, apiServer.Client(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	mcpHTTPServer := httptest.NewServer(NewStreamableHTTPHandler(api))
	defer mcpHTTPServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: mcpHTTPServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_top_processes", Arguments: map[string]any{"sort_by": "cpu", "limit": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %#v", result.Content)
	}
	var output TopProcessesOutput
	decodeStructured(t, result.StructuredContent, &output)
	if len(output.Processes) != 1 || output.Processes[0].PID != 2 {
		t.Fatalf("processes = %#v", output.Processes)
	}
}

func TestStreamableHTTPRejectsCrossOriginRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/mcp", http.NoBody)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	NewStreamableHTTPHandler(nil).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestTopProcessesValidationAndMissingData(t *testing.T) {
	_, session := connectedServer(t, snapshotJSON())
	defer session.Close()
	for _, args := range []map[string]any{
		{"sort_by": "disk", "limit": 2},
		{"sort_by": "cpu", "limit": 0},
		{"sort_by": "cpu", "limit": 21},
		{"sort_by": "cpu", "limit": 2, "extra": true},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_top_processes", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("args %#v returned success", args)
		}
	}
	_, missingSession := connectedServer(t, snapshotJSONWithMissing("processes"))
	defer missingSession.Close()
	result, err := missingSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_top_processes", Arguments: map[string]any{"sort_by": "cpu", "limit": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("missing process data returned success")
	}
}

func TestTopProcessesSortsMemoryAndAcceptsBoundaryLimits(t *testing.T) {
	_, session := connectedServer(t, snapshotJSON())
	defer session.Close()
	for _, test := range []struct {
		limit int
		want  []int32
	}{
		{limit: 1, want: []int32{1}},
		{limit: 20, want: []int32{1, 2, 3}},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "get_top_processes", Arguments: map[string]any{"sort_by": "memory", "limit": test.limit},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("limit %d returned tool error: %#v", test.limit, result.Content)
		}
		var output TopProcessesOutput
		decodeStructured(t, result.StructuredContent, &output)
		got := make([]int32, len(output.Processes))
		for i, process := range output.Processes {
			got[i] = process.PID
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("limit %d PIDs = %v, want %v", test.limit, got, test.want)
		}
	}
}

func TestSystemOverviewExcludesProcesses(t *testing.T) {
	_, session := connectedServer(t, snapshotJSON())
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_system_overview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool error: %#v", result.Content)
	}
	var raw map[string]any
	decodeStructured(t, result.StructuredContent, &raw)
	if _, ok := raw["processes"]; ok {
		t.Fatal("system overview contains processes")
	}
}

func TestResourceHistoryMappingAndStatistics(t *testing.T) {
	_, session := connectedServer(t, historyJSON())
	defer session.Close()
	for _, test := range []struct {
		metric string
		unit   string
		value  float64
	}{
		{"cpu", "percent", 30},
		{"memory", "percent", 70},
		{"load", "load_average", 1.5},
		{"network_receive", "bytes_per_second", 11},
		{"network_send", "bytes_per_second", 22},
		{"disk_read", "bytes_per_second", 33},
		{"disk_write", "bytes_per_second", 44},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_resource_history", Arguments: map[string]any{"metric": test.metric, "samples": 2}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("metric %s failed: %#v", test.metric, result.Content)
		}
		var output ResourceHistoryOutput
		decodeStructured(t, result.StructuredContent, &output)
		if output.Unit != test.unit || len(output.Points) != 2 || output.Points[0].Value != test.value {
			t.Fatalf("metric %s output = %#v", test.metric, output)
		}
		if output.Minimum == nil || output.Maximum == nil || output.Average == nil {
			t.Fatalf("metric %s has no statistics", test.metric)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_resource_history", Arguments: map[string]any{"metric": "unknown", "samples": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("unknown metric returned success")
	}
}

func TestResourceHistoryMissingDataIsExplicit(t *testing.T) {
	_, session := connectedServer(t, historyJSONWithMissing())
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_resource_history", Arguments: map[string]any{"metric": "network_receive", "samples": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool failed unexpectedly: %#v", result.Content)
	}
	var output ResourceHistoryOutput
	decodeStructured(t, result.StructuredContent, &output)
	if len(output.Points) != 1 || len(output.MissingData) != 1 || output.MissingData[0] != "network" {
		t.Fatalf("output = %#v", output)
	}
}

func TestResourceHistoryEmptyHistoryHasNoStatistics(t *testing.T) {
	_, session := connectedServer(t, `{"items":[]}`)
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_resource_history", Arguments: map[string]any{"metric": "cpu", "samples": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool failed: %#v", result.Content)
	}
	var output ResourceHistoryOutput
	decodeStructured(t, result.StructuredContent, &output)
	if len(output.Points) != 0 || output.Minimum != nil || output.Maximum != nil || output.Average != nil {
		t.Fatalf("empty history output = %#v", output)
	}
}

func TestMetricsSummaryToolSupportsRelativeAndAbsoluteRanges(t *testing.T) {
	_, session := connectedServerWithHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics/summary" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("from") == "" || r.URL.Query().Get("to") == "" {
			t.Errorf("summary query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, metricsSummaryJSON())
	}))
	defer session.Close()

	lastMinutes := 60
	for _, input := range []MetricsSummaryInput{
		{LastMinutes: &lastMinutes},
		{From: "2026-09-24T10:00:00Z", To: "2026-09-24T11:00:00Z"},
	} {
		arguments := map[string]any{}
		if input.LastMinutes != nil {
			arguments["last_minutes"] = *input.LastMinutes
		} else {
			arguments["from"] = input.From
			arguments["to"] = input.To
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_metrics_summary", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("summary tool error: %#v", result.Content)
		}
		var output map[string]any
		decodeStructured(t, result.StructuredContent, &output)
		coverage, ok := output["actual_coverage"].(map[string]any)
		if !ok || coverage["samples"] != float64(2) {
			t.Fatalf("summary output = %#v", output)
		}
	}
}

func TestMetricsSummaryToolRejectsInvalidRanges(t *testing.T) {
	_, session := connectedServer(t, metricsSummaryJSON())
	defer session.Close()
	lastMinutes := 2
	for _, arguments := range []map[string]any{
		{},
		{"last_minutes": lastMinutes},
		{"last_minutes": 60, "from": "2026-09-24T10:00:00Z", "to": "2026-09-24T11:00:00Z"},
		{"from": "2026-09-24T10:00:00Z"},
		{"from": "bad", "to": "2026-09-24T11:00:00Z"},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_metrics_summary", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("arguments %#v returned success", arguments)
		}
	}
}

func TestToolsPropagateUpstreamErrors(t *testing.T) {
	_, session := connectedServerWithHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"not forwarded"}`))
	}))
	defer session.Close()
	for _, params := range []*mcp.CallToolParams{
		{Name: "get_system_overview", Arguments: map[string]any{}},
		{Name: "get_resource_history", Arguments: map[string]any{"metric": "cpu", "samples": 1}},
		{Name: "get_metrics_summary", Arguments: map[string]any{"last_minutes": 3}},
	} {
		result, err := session.CallTool(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("%s did not return tool error", params.Name)
		}
	}
}

func connectedServer(t *testing.T, response string) (*mcp.Server, *mcp.ClientSession) {
	t.Helper()
	return connectedServerWithHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/snapshot":
			fmt.Fprint(w, response)
		case "/api/history":
			fmt.Fprint(w, response)
		default:
			http.NotFound(w, r)
		}
	}))
}

func connectedServerWithHandler(t *testing.T, handler http.Handler) (*mcp.Server, *mcp.ClientSession) {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	api, err := taskmanagerapi.NewClientWithHTTPClient(httpServer.URL, httpServer.Client(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(api)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, session
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeStructured(t *testing.T, value any, output any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		t.Fatal(err)
	}
}

func snapshotJSON() string {
	return `{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","hostname":"host","cpu_cores":8,"cpu_percent":30,"mem_percent":70,"load_1":1.5,"processes":[{"pid":1,"name":"same-cpu","cpu_percent":10,"mem_percent":20},{"pid":2,"name":"worker","cpu_percent":90,"mem_percent":20},{"pid":3,"name":"other","cpu_percent":90,"mem_percent":5}],"missing_data":[]}`
}

func snapshotJSONWithMissing(section string) string {
	return fmt.Sprintf(`{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","missing_data":[%q]}`, section)
}

func historyJSON() string {
	return `{"items":[{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","cpu_percent":30,"mem_percent":70,"load_1":1.5,"network":{"recv_per_sec":11,"send_per_sec":22},"disk_io":{"read_per_sec":33,"write_per_sec":44},"missing_data":[]},{"snapshot_id":"snap-2","captured_at":"2026-09-24T12:01:00Z","cpu_percent":20,"mem_percent":60,"load_1":1,"network":{"recv_per_sec":10,"send_per_sec":20},"disk_io":{"read_per_sec":30,"write_per_sec":40},"missing_data":[]}]}`
}

func historyJSONWithMissing() string {
	return `{"items":[{"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","network":{"recv_per_sec":11},"missing_data":["network"]},{"snapshot_id":"snap-2","captured_at":"2026-09-24T12:01:00Z","network":{"recv_per_sec":10},"missing_data":[]}]}`
}

func metricsSummaryJSON() string {
	return `{"requested_from":"2026-09-24T10:00:00Z","requested_to":"2026-09-24T11:00:00Z","actual_coverage":{"from":"2026-09-24T10:00:00Z","to":"2026-09-24T11:00:00Z","samples":2,"rollups":1},"missing_data":[]}`
}
