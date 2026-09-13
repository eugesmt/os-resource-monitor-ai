package ai

import (
	"fmt"
	"math"
	"strings"
	"time"

	"taskmanager-ai/internal/collector"
)

func BuildContext(snapshot collector.SystemSnapshot, history []collector.SystemSnapshot) string {
	var b strings.Builder

	b.WriteString("SYSTEM CONTEXT\n\n")
	b.WriteString("CURRENT SYSTEM STATE\n")
	if !snapshot.Timestamp.IsZero() {
		fmt.Fprintf(&b, "Timestamp: %s\n", snapshot.Timestamp.Format(time.RFC3339Nano))
	}
	fmt.Fprintf(&b, "Current CPU: %.1f%%\n", snapshot.CPUPercent)
	fmt.Fprintf(&b, "Memory: %.2f GB / %.2f GB (%.1f%%)\n", bytesToGB(snapshot.MemUsed), bytesToGB(snapshot.MemTotal), snapshot.MemPercent)

	b.WriteString("\nDisks:\n")
	for _, d := range snapshot.Disks {
		fmt.Fprintf(&b, "- %s (%s): %.2f GB used / %.2f GB total\n", d.Mountpoint, d.Device, bytesToGB(d.Used), bytesToGB(d.Total))
	}

	fmt.Fprintf(&b, "\nDisk I/O: read %.2f MB/s, write %.2f MB/s\n", snapshot.DiskIO.ReadPerSec/1024/1024, snapshot.DiskIO.WritePerSec/1024/1024)
	fmt.Fprintf(&b, "Network: receive %.2f KB/s, send %.2f KB/s\n", snapshot.Network.RecvPerSec/1024, snapshot.Network.SendPerSec/1024)

	b.WriteString("\nTop 5 processes by CPU:\n")
	limit := 5
	if len(snapshot.Processes) < limit {
		limit = len(snapshot.Processes)
	}
	for i := 0; i < limit; i++ {
		p := snapshot.Processes[i]
		fmt.Fprintf(&b, "%d. %s (PID %d): CPU %.1f%%, MEM %.1f%%\n", i+1, p.Name, p.PID, p.CPUPercent, p.MemPercent)
	}

	if len(history) > 1 {
		prev := history[len(history)-2]
		b.WriteString("\nPREVIOUS SNAPSHOT\n")
		if !prev.Timestamp.IsZero() {
			fmt.Fprintf(&b, "Timestamp: %s\n", prev.Timestamp.Format(time.RFC3339Nano))
		}
		fmt.Fprintf(&b, "CPU: %.1f%%\n", prev.CPUPercent)
		fmt.Fprintf(&b, "Memory: %.1f%%\n", prev.MemPercent)
		b.WriteString("\nCALCULATED CHANGES\n")
		fmt.Fprintf(&b, "CPU: %+.1f percentage points\n", snapshot.CPUPercent-prev.CPUPercent)
		fmt.Fprintf(&b, "Memory: %+.1f percentage points\n", snapshot.MemPercent-prev.MemPercent)
	}

	if len(history) > 0 {
		b.WriteString("\nSNAPSHOT HISTORY\n")
		cpuAvg, cpuMin, cpuMax := stats(history, func(s collector.SystemSnapshot) float64 { return s.CPUPercent })
		memAvg, memMin, memMax := stats(history, func(s collector.SystemSnapshot) float64 { return s.MemPercent })
		fmt.Fprintf(&b, "\nHistory over last %d snapshots:\n", len(history))
		fmt.Fprintf(&b, "CPU: avg %.1f%%, min %.1f%%, max %.1f%%\n", cpuAvg, cpuMin, cpuMax)
		fmt.Fprintf(&b, "Memory: avg %.1f%%, min %.1f%%, max %.1f%%\n", memAvg, memMin, memMax)
	}

	return b.String()
}

func stats(items []collector.SystemSnapshot, pick func(collector.SystemSnapshot) float64) (avg, min, max float64) {
	if len(items) == 0 {
		return 0, 0, 0
	}
	min = math.Inf(1)
	max = math.Inf(-1)
	var sum float64
	for _, item := range items {
		v := pick(item)
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return sum / float64(len(items)), min, max
}

func bytesToGB(v uint64) float64 { return float64(v) / 1024 / 1024 / 1024 }
