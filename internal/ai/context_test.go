package ai

import (
	"strings"
	"testing"
	"time"

	"taskmanager-ai/internal/collector"
)

func TestBuildContextIncludesKeySections(t *testing.T) {
	now := time.Now()
	current := collector.SystemSnapshot{
		Timestamp: now, CPUPercent: 60, MemUsed: 4 * 1024 * 1024 * 1024, MemTotal: 8 * 1024 * 1024 * 1024, MemPercent: 50,
		CPUPerCore: []float64{10, 20, 30, 40},
		Processes:  []collector.ProcessInfo{{PID: 42, Name: "testproc", CPUPercent: 33.3, MemPercent: 4.2}},
		GPU:        []collector.GPUInfo{{Index: 0, Name: "test-gpu", UtilizationPercent: 25}},
	}
	current.SnapshotID = "snap-current"
	text := BuildContext(current, []collector.SystemSnapshot{{SnapshotID: "old"}})
	for _, want := range []string{"SNAPSHOT_DATA", "UNTRUSTED DATA", "snap-current", "cpu_per_core", "testproc", "test-gpu", "CURRENT_SNAPSHOT_ONLY=true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "old") {
		t.Fatal("history snapshot leaked into current-only context")
	}
}

func TestProjectSnapshotSelectsTopCPUAndMemoryProcesses(t *testing.T) {
	processes := make([]collector.ProcessInfo, 25)
	for i := range processes {
		processes[i] = collector.ProcessInfo{PID: int32(i + 1), CPUPercent: float64(25 - i), MemPercent: float32(i + 1), MemBytes: uint64(i + 1)}
	}
	projected := projectSnapshot(collector.SystemSnapshot{Processes: processes})
	if projected.ProcessCount != 25 || !projected.ProcessesTruncated || len(projected.Processes) != 20 {
		t.Fatalf("projected process metadata = count:%d truncated:%t len:%d", projected.ProcessCount, projected.ProcessesTruncated, len(projected.Processes))
	}
	if projected.Processes[0].PID != 1 {
		t.Fatalf("first process = %#v, want highest CPU process", projected.Processes[0])
	}
}
