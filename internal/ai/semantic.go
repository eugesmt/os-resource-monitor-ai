package ai

import (
	"fmt"
	"math"
	"strings"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
)

func ValidateDiagnosis(result domain.DiagnosisResultV1, snapshot collector.SystemSnapshot) error {
	if err := validateMissingData(result.MissingData, snapshot.MissingData); err != nil {
		return err
	}
	if result.Status == "unknown" {
		if len(snapshot.MissingData) == 0 {
			return fmt.Errorf("%w: unknown status requires missing_data", ErrSemanticValidation)
		}
	}
	if (result.Status == "warning" || result.Status == "critical") && len(result.Observations) == 0 {
		return fmt.Errorf("%w: %s status requires grounded observations", ErrSemanticValidation, result.Status)
	}
	for _, observation := range result.Observations {
		if err := validateObservation(observation, snapshot); err != nil {
			return err
		}
	}
	if result.Status == "healthy" &&
		((!metricMissing(snapshot, "cpu_percent") && snapshot.CPUPercent >= 90) ||
			(!metricMissing(snapshot, "mem_percent") && snapshot.MemPercent >= 95) ||
			(!metricMissing(snapshot, "disk_utilization_percent") && anyDiskAtLeast(snapshot, 95))) {
		return fmt.Errorf("%w: healthy status contradicts critical threshold", ErrSemanticValidation)
	}
	return nil
}

var knownMissingSections = map[string]struct{}{
	"cpu":          {},
	"cpu_per_core": {},
	"memory":       {},
	"swap":         {},
	"load":         {},
	"host":         {},
	"cpu_cores":    {},
	"disks":        {},
	"disk_io":      {},
	"network":      {},
	"gpu":          {},
	"processes":    {},
}

func validateMissingData(reported, actual []string) error {
	reportedSet, err := missingDataSet(reported, "response")
	if err != nil {
		return err
	}
	actualSet, err := missingDataSet(actual, "snapshot")
	if err != nil {
		return err
	}
	if len(reportedSet) != len(actualSet) {
		return fmt.Errorf("%w: missing_data must exactly match snapshot", ErrSemanticValidation)
	}
	for section := range actualSet {
		if _, ok := reportedSet[section]; !ok {
			return fmt.Errorf("%w: missing_data omits %q", ErrSemanticValidation, section)
		}
	}
	return nil
}

func missingDataSet(items []string, source string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		section := strings.TrimSpace(item)
		if _, ok := knownMissingSections[section]; !ok {
			return nil, fmt.Errorf("%w: %s contains unknown missing section %q", ErrSemanticValidation, source, section)
		}
		if _, duplicate := set[section]; duplicate {
			return nil, fmt.Errorf("%w: %s contains duplicate missing section %q", ErrSemanticValidation, source, section)
		}
		set[section] = struct{}{}
	}
	return set, nil
}

func validateObservation(observation domain.Observation, snapshot collector.SystemSnapshot) error {
	metric := strings.TrimSpace(observation.Metric)
	if observation.PID != nil && !strings.HasPrefix(metric, "process.") {
		return fmt.Errorf("%w: metric %q cannot carry a PID", ErrSemanticValidation, metric)
	}
	if metricMissing(snapshot, metric) {
		return fmt.Errorf("%w: metric %q is missing", ErrSemanticValidation, metric)
	}
	var expected float64
	var unit string
	switch metric {
	case "cpu_percent":
		expected, unit = snapshot.CPUPercent, "%"
	case "mem_percent":
		expected, unit = snapshot.MemPercent, "%"
	case "mem_used":
		expected, unit = float64(snapshot.MemUsed), "bytes"
	case "mem_total":
		expected, unit = float64(snapshot.MemTotal), "bytes"
	case "mem_available":
		expected, unit = float64(snapshot.MemAvailable), "bytes"
	case "swap_used":
		expected, unit = float64(snapshot.SwapUsed), "bytes"
	case "swap_total":
		expected, unit = float64(snapshot.SwapTotal), "bytes"
	case "cpu_cores":
		expected, unit = float64(snapshot.CPUCores), "cores"
	case "load_1":
		expected, unit = snapshot.Load1, "load"
	case "load_5":
		expected, unit = snapshot.Load5, "load"
	case "load_15":
		expected, unit = snapshot.Load15, "load"
	case "disk_io.read_per_sec":
		expected, unit = snapshot.DiskIO.ReadPerSec, "bytes_per_second"
	case "disk_io.write_per_sec":
		expected, unit = snapshot.DiskIO.WritePerSec, "bytes_per_second"
	case "network.bytes_sent":
		expected, unit = float64(snapshot.Network.BytesSent), "bytes"
	case "network.bytes_recv":
		expected, unit = float64(snapshot.Network.BytesRecv), "bytes"
	case "network.send_per_sec":
		expected, unit = snapshot.Network.SendPerSec, "bytes_per_second"
	case "network.recv_per_sec":
		expected, unit = snapshot.Network.RecvPerSec, "bytes_per_second"
	case "gpu.count", "gpu_count":
		expected, unit = float64(len(snapshot.GPU)), "count"
	case "gpu.memory.utilization_percent":
		expected, unit = gpuMemoryUtilization(snapshot), "%"
	default:
		if metric != "disk_utilization_percent" && !strings.HasPrefix(metric, "gpu.") && metric != "process.cpu_percent" && metric != "process.mem_percent" && metric != "process.mem_bytes" && metric != "process.threads" {
			return fmt.Errorf("%w: unsupported or ungrounded metric %q", ErrSemanticValidation, metric)
		}
		if metric == "disk_utilization_percent" {
			return validateDiskObservation(observation, snapshot)
		}
		if strings.HasPrefix(metric, "gpu.") {
			return validateGPUObservation(observation, snapshot)
		}
		if observation.PID == nil {
			return fmt.Errorf("%w: process observation requires pid", ErrSemanticValidation)
		}
		// Only processes included in BuildContext are visible to the model. Reject
		// observations for the rest instead of grounding on hidden snapshot data.
		for _, process := range selectProcesses(snapshot.Processes, 10) {
			if process.PID == *observation.PID {
				if metric == "process.cpu_percent" {
					expected, unit = process.CPUPercent, "%"
				} else if metric == "process.mem_percent" {
					expected, unit = float64(process.MemPercent), "%"
				} else if metric == "process.mem_bytes" {
					expected, unit = float64(process.MemBytes), "bytes"
				} else {
					expected, unit = float64(process.Threads), "threads"
				}
				return compareObservation(observation, expected, unit)
			}
		}
		return fmt.Errorf("%w: PID %d is not in snapshot", ErrSemanticValidation, *observation.PID)
	}
	return compareObservation(observation, expected, unit)
}

func gpuMemoryUtilization(snapshot collector.SystemSnapshot) float64 {
	var used, total uint64
	for _, gpu := range snapshot.GPU {
		used += gpu.MemoryUsed
		total += gpu.MemoryTotal
	}
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func validateDiskObservation(observation domain.Observation, snapshot collector.SystemSnapshot) error {
	if len(snapshot.Disks) == 0 {
		return fmt.Errorf("%w: disks unavailable", ErrSemanticValidation)
	}
	max := 0.0
	for _, disk := range snapshot.Disks {
		if disk.UtilizationPercent > max {
			max = disk.UtilizationPercent
		}
	}
	return compareObservation(observation, max, "%")
}

func validateGPUObservation(observation domain.Observation, snapshot collector.SystemSnapshot) error {
	if len(snapshot.GPU) == 0 {
		return fmt.Errorf("%w: GPU unavailable", ErrSemanticValidation)
	}
	var expected float64
	unit := "%"
	for _, gpu := range snapshot.GPU {
		switch observation.Metric {
		case "gpu.utilization_percent":
			if float64(gpu.UtilizationPercent) > expected {
				expected = float64(gpu.UtilizationPercent)
			}
		case "gpu.memory_used":
			expected += float64(gpu.MemoryUsed)
			unit = "bytes"
		case "gpu.memory_total":
			expected += float64(gpu.MemoryTotal)
			unit = "bytes"
		case "gpu.temperature_celsius":
			if float64(gpu.TemperatureCelsius) > expected {
				expected = float64(gpu.TemperatureCelsius)
			}
			unit = "celsius"
		default:
			return fmt.Errorf("%w: unsupported GPU metric %q", ErrSemanticValidation, observation.Metric)
		}
	}
	return compareObservation(observation, expected, unit)
}

func compareObservation(observation domain.Observation, expected float64, unit string) error {
	if normalizeUnit(observation.Unit) != normalizeUnit(unit) {
		return fmt.Errorf("%w: observation %q has an incompatible unit", ErrSemanticValidation, observation.Metric)
	}
	if math.Abs(observation.Value-expected) > math.Max(0.1, math.Abs(expected)*0.02) {
		return fmt.Errorf("%w: observation %q is not grounded", ErrSemanticValidation, observation.Metric)
	}
	return nil
}

func normalizeUnit(unit string) string {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "%", "percent", "percentage":
		return "%"
	case "load", "load average", "load_average", "loadavg":
		return "load"
	case "b", "byte", "bytes":
		return "bytes"
	case "b/s", "byte/s", "bytes/s", "bytes per second", "bytes_per_second":
		return "bytes_per_second"
	case "core", "cores":
		return "cores"
	case "thread", "threads":
		return "threads"
	case "count", "counts", "gpu", "gpus":
		return "count"
	default:
		return strings.ToLower(strings.TrimSpace(unit))
	}
}

func anyDiskAtLeast(snapshot collector.SystemSnapshot, threshold float64) bool {
	for _, disk := range snapshot.Disks {
		if disk.UtilizationPercent >= threshold {
			return true
		}
	}
	return false
}
func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func metricMissing(snapshot collector.SystemSnapshot, metric string) bool {
	section := metric
	switch metric {
	case "cpu_percent":
		section = "cpu"
	case "mem_percent":
		section = "memory"
	case "mem_used", "mem_total", "mem_available":
		section = "memory"
	case "swap_used", "swap_total":
		section = "swap"
	case "cpu_cores":
		section = "cpu_cores"
	case "load_1", "load_5", "load_15":
		section = "load"
	case "disk_utilization_percent":
		section = "disks"
	case "disk_io.read_per_sec", "disk_io.write_per_sec":
		section = "disk_io"
	case "network.bytes_sent", "network.bytes_recv", "network.send_per_sec", "network.recv_per_sec":
		section = "network"
	case "gpu.utilization_percent", "gpu.memory_used", "gpu.memory_total", "gpu.memory.utilization_percent", "gpu.temperature_celsius":
		section = "gpu"
	case "gpu.count", "gpu_count":
		section = "gpu"
	case "process.cpu_percent", "process.mem_percent", "process.mem_bytes", "process.threads":
		section = "processes"
	}
	return contains(snapshot.MissingData, section)
}
