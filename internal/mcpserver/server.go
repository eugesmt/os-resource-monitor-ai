package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/metrics"
	"taskmanager-ai/internal/taskmanagerapi"
)

type EmptyInput struct{}

type TopProcessesInput struct {
	SortBy string `json:"sort_by" jsonschema:"metric used to rank processes: cpu or memory"`
	Limit  int    `json:"limit" jsonschema:"maximum number of processes to return, from 1 to 20"`
}

type ResourceHistoryInput struct {
	Metric  string `json:"metric" jsonschema:"metric to return: cpu, memory, load, network_receive, network_send, disk_read, or disk_write"`
	Samples int    `json:"samples" jsonschema:"maximum number of history samples, from 1 to 120"`
}

type MetricsSummaryInput struct {
	LastMinutes *int   `json:"last_minutes,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
}

type SystemOverviewOutput struct {
	SnapshotID      string                `json:"snapshot_id"`
	CapturedAt      time.Time             `json:"captured_at"`
	Hostname        string                `json:"hostname"`
	CPUCores        int                   `json:"cpu_cores"`
	CPUPercent      float64               `json:"cpu_percent"`
	CPUPerCore      []float64             `json:"cpu_per_core"`
	Load1           float64               `json:"load_1"`
	Load5           float64               `json:"load_5"`
	Load15          float64               `json:"load_15"`
	MemoryUsed      uint64                `json:"memory_used"`
	MemoryTotal     uint64                `json:"memory_total"`
	MemoryAvailable uint64                `json:"memory_available"`
	MemoryPercent   float64               `json:"memory_percent"`
	SwapUsed        uint64                `json:"swap_used"`
	SwapTotal       uint64                `json:"swap_total"`
	Disks           []collector.DiskInfo  `json:"disks"`
	DiskIO          collector.DiskIOInfo  `json:"disk_io"`
	Network         collector.NetworkInfo `json:"network"`
	GPU             []collector.GPUInfo   `json:"gpu"`
	MissingData     []string              `json:"missing_data"`
}

type TopProcessesOutput struct {
	SnapshotID  string                  `json:"snapshot_id"`
	CapturedAt  time.Time               `json:"captured_at"`
	SortBy      string                  `json:"sort_by"`
	Processes   []collector.ProcessInfo `json:"processes"`
	MissingData []string                `json:"missing_data"`
}

type HistoryPoint struct {
	SnapshotID string    `json:"snapshot_id"`
	CapturedAt time.Time `json:"captured_at"`
	Value      float64   `json:"value"`
}

type ResourceHistoryOutput struct {
	Metric      string         `json:"metric"`
	Unit        string         `json:"unit"`
	Points      []HistoryPoint `json:"points"`
	Minimum     *float64       `json:"minimum,omitempty"`
	Maximum     *float64       `json:"maximum,omitempty"`
	Average     *float64       `json:"average,omitempty"`
	MissingData []string       `json:"missing_data"`
}

const (
	maxHTTPMCPRequestBytes     = 1 << 20
	emptyInputSchema           = `{"type":"object","properties":{},"additionalProperties":false}`
	topProcessesInputSchema    = `{"type":"object","properties":{"sort_by":{"type":"string","enum":["cpu","memory"],"description":"Metric used to rank processes"},"limit":{"type":"integer","minimum":1,"maximum":20,"description":"Maximum number of processes to return"}},"required":["sort_by","limit"],"additionalProperties":false}`
	resourceHistoryInputSchema = `{"type":"object","properties":{"metric":{"type":"string","enum":["cpu","memory","load","network_receive","network_send","disk_read","disk_write"],"description":"System metric to return"},"samples":{"type":"integer","minimum":1,"maximum":120,"description":"Maximum number of history samples"}},"required":["metric","samples"],"additionalProperties":false}`
	metricsSummaryInputSchema  = `{"type":"object","properties":{"last_minutes":{"type":"integer","minimum":3,"maximum":10080,"description":"Relative lookback in minutes"},"from":{"type":"string","format":"date-time","description":"RFC3339 range start"},"to":{"type":"string","format":"date-time","description":"RFC3339 range end"}},"oneOf":[{"required":["last_minutes"]},{"required":["from","to"]}],"additionalProperties":false}`
	saveDiagnosticReportSchema = `{"type":"object","properties":{"content":{"type":"string","minLength":1,"maxLength":131072,"description":"Complete diagnostic report content"},"format":{"type":"string","enum":["markdown","json"],"description":"File format for the report"}},"required":["content","format"],"additionalProperties":false}`
)

// NewStreamableHTTPHandler exposes tools over stateless MCP Streamable HTTP.
// Authentication and listener policy remain the caller's responsibility; the
// production command restricts its listener to loopback.
func NewStreamableHTTPHandler(api *taskmanagerapi.Client, options ...ServerOption) http.Handler {
	server := NewServer(api, options...)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		MaxRequestBodyBytes:          maxHTTPMCPRequestBytes,
		PropagateRequestCancellation: true,
	})
	return http.NewCrossOriginProtection().Handler(handler)
}

// NewServer constructs an MCP server backed by the Task Manager API. It remains
// read-only unless WithReportDirectory is supplied with a non-empty directory.
func NewServer(api *taskmanagerapi.Client, options ...ServerOption) *mcp.Server {
	config := serverConfig{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "taskmanager-mcp", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_system_overview", Description: "Return the current compact operating-system overview", InputSchema: json.RawMessage(emptyInputSchema),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, SystemOverviewOutput, error) {
		if api == nil {
			return nil, SystemOverviewOutput{}, fmt.Errorf("Task Manager API client is unavailable")
		}
		snapshot, err := api.Snapshot(ctx)
		if err != nil {
			return nil, SystemOverviewOutput{}, err
		}
		return nil, SystemOverviewOutput{
			SnapshotID: snapshot.SnapshotID, CapturedAt: snapshot.CapturedAt, Hostname: snapshot.Hostname,
			CPUCores: snapshot.CPUCores, CPUPercent: snapshot.CPUPercent, CPUPerCore: snapshot.CPUPerCore,
			Load1: snapshot.Load1, Load5: snapshot.Load5, Load15: snapshot.Load15,
			MemoryUsed: snapshot.MemUsed, MemoryTotal: snapshot.MemTotal, MemoryAvailable: snapshot.MemAvailable,
			MemoryPercent: snapshot.MemPercent, SwapUsed: snapshot.SwapUsed, SwapTotal: snapshot.SwapTotal,
			Disks: snapshot.Disks, DiskIO: snapshot.DiskIO, Network: snapshot.Network, GPU: snapshot.GPU,
			MissingData: snapshot.MissingData,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_top_processes", Description: "Return processes using the most CPU or memory", InputSchema: json.RawMessage(topProcessesInputSchema),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input TopProcessesInput) (*mcp.CallToolResult, TopProcessesOutput, error) {
		if api == nil {
			return nil, TopProcessesOutput{}, fmt.Errorf("Task Manager API client is unavailable")
		}
		if input.SortBy != "cpu" && input.SortBy != "memory" {
			return nil, TopProcessesOutput{}, fmt.Errorf("sort_by must be cpu or memory")
		}
		if input.Limit < 1 || input.Limit > 20 {
			return nil, TopProcessesOutput{}, fmt.Errorf("limit must be between 1 and 20")
		}
		snapshot, err := api.Snapshot(ctx)
		if err != nil {
			return nil, TopProcessesOutput{}, err
		}
		if contains(snapshot.MissingData, "processes") {
			return nil, TopProcessesOutput{}, fmt.Errorf("process data unavailable: missing_data includes processes")
		}
		processes := append([]collector.ProcessInfo(nil), snapshot.Processes...)
		sort.SliceStable(processes, func(i, j int) bool {
			if input.SortBy == "memory" && processes[i].MemPercent != processes[j].MemPercent {
				return processes[i].MemPercent > processes[j].MemPercent
			}
			if input.SortBy == "cpu" && processes[i].CPUPercent != processes[j].CPUPercent {
				return processes[i].CPUPercent > processes[j].CPUPercent
			}
			return processes[i].PID < processes[j].PID
		})
		if len(processes) > input.Limit {
			processes = processes[:input.Limit]
		}
		return nil, TopProcessesOutput{SnapshotID: snapshot.SnapshotID, CapturedAt: snapshot.CapturedAt, SortBy: input.SortBy, Processes: processes, MissingData: snapshot.MissingData}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_resource_history", Description: "Return a compact time series for a selected system resource metric", InputSchema: json.RawMessage(resourceHistoryInputSchema),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input ResourceHistoryInput) (*mcp.CallToolResult, ResourceHistoryOutput, error) {
		if api == nil {
			return nil, ResourceHistoryOutput{}, fmt.Errorf("Task Manager API client is unavailable")
		}
		section, unit, valueFn, ok := metricSpec(input.Metric)
		if !ok {
			return nil, ResourceHistoryOutput{}, fmt.Errorf("unsupported metric %q", input.Metric)
		}
		if input.Samples < 1 || input.Samples > 120 {
			return nil, ResourceHistoryOutput{}, fmt.Errorf("samples must be between 1 and 120")
		}
		items, err := api.History(ctx, input.Samples)
		if err != nil {
			return nil, ResourceHistoryOutput{}, err
		}
		output := ResourceHistoryOutput{Metric: input.Metric, Unit: unit, Points: make([]HistoryPoint, 0, len(items)), MissingData: make([]string, 0)}
		values := make([]float64, 0, len(items))
		for _, item := range items {
			if contains(item.MissingData, section) {
				output.MissingData = appendUnique(output.MissingData, section)
				continue
			}
			value := valueFn(item)
			output.Points = append(output.Points, HistoryPoint{SnapshotID: item.SnapshotID, CapturedAt: item.CapturedAt, Value: value})
			values = append(values, value)
		}
		if len(values) > 0 {
			minimum, maximum, sum := values[0], values[0], 0.0
			for _, value := range values {
				if value < minimum {
					minimum = value
				}
				if value > maximum {
					maximum = value
				}
				sum += value
			}
			average := sum / float64(len(values))
			output.Minimum, output.Maximum, output.Average = &minimum, &maximum, &average
		}
		return nil, output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_metrics_summary", Description: "Return an aggregate operating-system summary for a relative or absolute time range", InputSchema: json.RawMessage(metricsSummaryInputSchema),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input MetricsSummaryInput) (*mcp.CallToolResult, metrics.Summary, error) {
		if api == nil {
			return nil, metrics.Summary{}, fmt.Errorf("Task Manager API client is unavailable")
		}
		from, to, err := resolveMetricsSummaryInput(input, time.Now().UTC())
		if err != nil {
			return nil, metrics.Summary{}, err
		}
		output, err := api.MetricsSummary(ctx, from, to)
		if err != nil {
			return nil, metrics.Summary{}, err
		}
		return nil, output, nil
	})
	if config.reportDirectory != "" {
		mcp.AddTool(server, &mcp.Tool{
			Name: "save_diagnostic_report", Description: "Save a diagnostic report in the server-configured report directory", InputSchema: json.RawMessage(saveDiagnosticReportSchema),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input SaveDiagnosticReportInput) (*mcp.CallToolResult, SaveDiagnosticReportOutput, error) {
			output, err := saveDiagnosticReport(ctx, config.reportDirectory, input)
			if err != nil {
				return nil, SaveDiagnosticReportOutput{}, err
			}
			if config.reportDownloadBaseURL != "" {
				output.DownloadURL = config.reportDownloadBaseURL + "/" + output.Path
			}
			return nil, output, nil
		})
	}
	return server
}

func resolveMetricsSummaryInput(input MetricsSummaryInput, now time.Time) (time.Time, time.Time, error) {
	fromRaw, toRaw := strings.TrimSpace(input.From), strings.TrimSpace(input.To)
	hasAbsolute := fromRaw != "" || toRaw != ""
	if input.LastMinutes != nil && hasAbsolute {
		return time.Time{}, time.Time{}, fmt.Errorf("use last_minutes or from and to, not both")
	}
	if input.LastMinutes == nil && !hasAbsolute {
		return time.Time{}, time.Time{}, fmt.Errorf("provide last_minutes or both from and to")
	}
	if input.LastMinutes != nil {
		if *input.LastMinutes < 3 || *input.LastMinutes > 10080 {
			return time.Time{}, time.Time{}, fmt.Errorf("last_minutes must be between 3 and 10080")
		}
		to := now.UTC()
		return to.Add(-time.Duration(*input.LastMinutes) * time.Minute), to, nil
	}
	if fromRaw == "" || toRaw == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("from and to must be provided together")
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be a valid RFC3339 timestamp")
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be a valid RFC3339 timestamp")
	}
	from, to = from.UTC(), to.UTC()
	if to.After(now.UTC()) {
		return time.Time{}, time.Time{}, fmt.Errorf("to cannot be in the future")
	}
	if !from.Before(to) || to.Sub(from) < 3*time.Minute || to.Sub(from) > 7*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("time range must be between 3 minutes and 7 days")
	}
	return from, to, nil
}

func metricSpec(metric string) (string, string, func(collector.SystemSnapshot) float64, bool) {
	switch metric {
	case "cpu":
		return "cpu", "percent", func(s collector.SystemSnapshot) float64 { return s.CPUPercent }, true
	case "memory":
		return "memory", "percent", func(s collector.SystemSnapshot) float64 { return s.MemPercent }, true
	case "load":
		return "load", "load_average", func(s collector.SystemSnapshot) float64 { return s.Load1 }, true
	case "network_receive":
		return "network", "bytes_per_second", func(s collector.SystemSnapshot) float64 { return s.Network.RecvPerSec }, true
	case "network_send":
		return "network", "bytes_per_second", func(s collector.SystemSnapshot) float64 { return s.Network.SendPerSec }, true
	case "disk_read":
		return "disk_io", "bytes_per_second", func(s collector.SystemSnapshot) float64 { return s.DiskIO.ReadPerSec }, true
	case "disk_write":
		return "disk_io", "bytes_per_second", func(s collector.SystemSnapshot) float64 { return s.DiskIO.WritePerSec }, true
	default:
		return "", "", nil, false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendUnique(values []string, target string) []string {
	if !contains(values, target) {
		return append(values, target)
	}
	return values
}
