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
		Processes: []collector.ProcessInfo{{PID: 42, Name: "testproc", CPUPercent: 33.3, MemPercent: 4.2}},
	}
	previous := current
	previous.CPUPercent = 40
	previous.MemPercent = 48

	text := BuildContext(current, []collector.SystemSnapshot{previous, current})
	for _, want := range []string{"SYSTEM STATE", "Top 5 processes", "testproc", "Change vs previous", "History over last 2 snapshots"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context missing %q: %s", want, text)
		}
	}
}
