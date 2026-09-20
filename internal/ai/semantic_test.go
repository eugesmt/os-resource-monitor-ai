package ai

import (
	"errors"
	"testing"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/domain"
)

func TestValidateDiagnosisGroundsMetricsAndPID(t *testing.T) {
	pid := int32(42)
	snapshot := collector.SystemSnapshot{CPUPercent: 50, Processes: []collector.ProcessInfo{{PID: pid, CPUPercent: 25}}}
	valid := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{{Metric: "process.cpu_percent", PID: &pid, Value: 25, Unit: "%"}}}
	if err := ValidateDiagnosis(valid, snapshot); err != nil {
		t.Fatalf("valid diagnosis rejected: %v", err)
	}
	pid = 99
	if err := ValidateDiagnosis(valid, snapshot); !errors.Is(err, ErrSemanticValidation) {
		t.Fatalf("unknown PID error = %v", err)
	}
}

func TestValidateDiagnosisAllowsUnknownWithMissingData(t *testing.T) {
	snapshot := collector.SystemSnapshot{MissingData: []string{"cpu"}}
	result := domain.DiagnosisResultV1{Status: "unknown", MissingData: []string{"cpu"}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("unknown diagnosis rejected: %v", err)
	}
}

func TestValidateDiagnosisUnknownStillGroundsObservations(t *testing.T) {
	pid := int32(42)
	snapshot := collector.SystemSnapshot{CPUPercent: 50, Processes: []collector.ProcessInfo{{PID: pid, CPUPercent: 25}}, MissingData: []string{"memory"}}
	result := domain.DiagnosisResultV1{
		Status:      "unknown",
		MissingData: []string{"memory"},
		Observations: []domain.Observation{{
			Metric: "process.cpu_percent", PID: &pid, Value: 999, Unit: "%", Interpretation: "fabricated",
		}},
	}
	if err := ValidateDiagnosis(result, snapshot); !errors.Is(err, ErrSemanticValidation) {
		t.Fatalf("fabricated unknown observation error = %v", err)
	}
}

func TestValidateDiagnosisRejectsFabricatedOrDuplicateMissingData(t *testing.T) {
	snapshot := collector.SystemSnapshot{MissingData: []string{"memory"}}
	for name, missing := range map[string][]string{
		"fabricated": {"memory", "gpu"},
		"duplicate":  {"memory", "memory"},
		"omitted":    {},
	} {
		t.Run(name, func(t *testing.T) {
			result := domain.DiagnosisResultV1{Status: "unknown", MissingData: missing}
			if err := ValidateDiagnosis(result, snapshot); !errors.Is(err, ErrSemanticValidation) {
				t.Fatalf("missing_data=%v error = %v", missing, err)
			}
		})
	}
}

func TestValidateDiagnosisRejectsExplanatoryMissingDataText(t *testing.T) {
	result := domain.DiagnosisResultV1{
		Status: "healthy",
		MissingData: []string{
			"gpu.memory.utilization_percent не указан отдельно, но вычисляется из memory_used/memory_total как ~8.6%",
		},
	}
	if err := ValidateDiagnosis(result, collector.SystemSnapshot{GPU: []collector.GPUInfo{{MemoryUsed: 86, MemoryTotal: 1000}}}); !errors.Is(err, ErrSemanticValidation) {
		t.Fatalf("explanatory missing_data error = %v, want ErrSemanticValidation", err)
	}
}

func TestValidateDiagnosisAcceptsDerivedGPUMemoryUtilizationWhenMemoryIsPresent(t *testing.T) {
	snapshot := collector.SystemSnapshot{GPU: []collector.GPUInfo{{MemoryUsed: 86, MemoryTotal: 1000}}}
	result := domain.DiagnosisResultV1{
		Status:       "warning",
		Observations: []domain.Observation{{Metric: "gpu.memory.utilization_percent", Value: 8.6, Unit: "%"}},
	}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("derived GPU memory utilization rejected: %v", err)
	}
}

func TestValidateDiagnosisRejectsObservationForHiddenProcess(t *testing.T) {
	processes := make([]collector.ProcessInfo, 21)
	for i := range processes {
		processes[i] = collector.ProcessInfo{PID: int32(i + 1), CPUPercent: float64(100 - i)}
	}
	pid := processes[len(processes)-1].PID
	result := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{{Metric: "process.cpu_percent", PID: &pid, Value: processes[len(processes)-1].CPUPercent, Unit: "%", Interpretation: "hidden"}}}
	if err := ValidateDiagnosis(result, collector.SystemSnapshot{Processes: processes}); !errors.Is(err, ErrSemanticValidation) {
		t.Fatalf("hidden process error = %v", err)
	}
}

func TestValidateDiagnosisSupportsSnapshotMemoryAndSwapMetrics(t *testing.T) {
	snapshot := collector.SystemSnapshot{MemUsed: 8, MemTotal: 16, SwapUsed: 4, SwapTotal: 8}
	result := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{
		{Metric: "swap_used", Value: 4, Unit: "bytes", Interpretation: "swap usage"},
		{Metric: "mem_used", Value: 8, Unit: "bytes", Interpretation: "memory usage"},
	}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("snapshot metrics rejected: %v", err)
	}
}

func TestValidateDiagnosisAcceptsEquivalentLoadUnit(t *testing.T) {
	snapshot := collector.SystemSnapshot{Load1: 1.25}
	result := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{{Metric: "load_1", Value: 1.25, Unit: "load average", Interpretation: "load average is elevated"}}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("equivalent load unit rejected: %v", err)
	}
}

func TestValidateDiagnosisAcceptsGroundedGPUObservation(t *testing.T) {
	snapshot := collector.SystemSnapshot{GPU: []collector.GPUInfo{{Index: 0, UtilizationPercent: 75}}}
	result := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{{Metric: "gpu.utilization_percent", Value: 75, Unit: "%"}}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("GPU observation rejected: %v", err)
	}
}

func TestValidateDiagnosisAcceptsGroundedGPUCount(t *testing.T) {
	snapshot := collector.SystemSnapshot{GPU: []collector.GPUInfo{{Index: 0}, {Index: 1}}}
	result := domain.DiagnosisResultV1{Status: "healthy", Observations: []domain.Observation{{Metric: "gpu.count", Value: 2, Unit: "count"}}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("GPU count observation rejected: %v", err)
	}
}

func TestValidateDiagnosisAcceptsGroundedGPUCountAlias(t *testing.T) {
	snapshot := collector.SystemSnapshot{GPU: []collector.GPUInfo{{Index: 0}}}
	result := domain.DiagnosisResultV1{Status: "healthy", Observations: []domain.Observation{{Metric: "gpu_count", Value: 1, Unit: "count"}}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("GPU count alias observation rejected: %v", err)
	}
}

func TestValidateDiagnosisAcceptsGroundedGPUMemoryUtilization(t *testing.T) {
	snapshot := collector.SystemSnapshot{GPU: []collector.GPUInfo{{MemoryUsed: 75, MemoryTotal: 100}}}
	result := domain.DiagnosisResultV1{Status: "warning", Observations: []domain.Observation{{Metric: "gpu.memory.utilization_percent", Value: 75, Unit: "%"}}}
	if err := ValidateDiagnosis(result, snapshot); err != nil {
		t.Fatalf("GPU memory utilization observation rejected: %v", err)
	}
}
