package collector

import (
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

func testProbes() Probes {
	return Probes{
		CPU:        func() (float64, error) { return 0, nil },
		CPUPerCore: func() ([]float64, error) { return []float64{0, 0, 0, 0}, nil },
		Memory: func() (*mem.VirtualMemoryStat, error) {
			return &mem.VirtualMemoryStat{Used: 1, Total: 2, Available: 1, UsedPercent: 0}, nil
		},
		Swap:     func() (*mem.SwapMemoryStat, error) { return &mem.SwapMemoryStat{Used: 0, Total: 1}, nil },
		Load:     func() (*load.AvgStat, error) { return &load.AvgStat{}, nil },
		Hostname: func() (string, error) { return "test-host", nil },
		CPUCores: func() (int, error) { return 4, nil },
		Disks:    func() ([]DiskInfo, error) { return []DiskInfo{{Total: 100, Used: 10, UtilizationPercent: 10}}, nil },
		DiskIO:   func(time.Time) (DiskIOInfo, error) { return DiskIOInfo{}, nil },
		Network:  func(time.Time) (NetworkInfo, error) { return NetworkInfo{}, nil },
		GPU: func() ([]GPUInfo, error) {
			return []GPUInfo{{Index: 0, Name: "test-gpu", UtilizationPercent: 25, MemoryUsed: 100, MemoryTotal: 200, TemperatureCelsius: 55}}, nil
		},
		Processes: func() ([]ProcessInfo, error) { return []ProcessInfo{}, nil },
	}
}

func TestCollectWithProbesPreservesZeroAndIdentity(t *testing.T) {
	snapshot, err := New(10, nil).CollectWithProbes(testProbes())
	if err != nil {
		t.Fatalf("CollectWithProbes() error = %v", err)
	}
	if snapshot.SnapshotID == "" || snapshot.CapturedAt.IsZero() || snapshot.CPUPercent != 0 || len(snapshot.CPUPerCore) != 4 || has(snapshot.MissingData, "cpu") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestCollectWithProbesGeneratesUniqueSnapshotUUIDv4(t *testing.T) {
	first, err := New(10, nil).CollectWithProbes(testProbes())
	if err != nil {
		t.Fatalf("first CollectWithProbes() error = %v", err)
	}
	second, err := New(10, nil).CollectWithProbes(testProbes())
	if err != nil {
		t.Fatalf("second CollectWithProbes() error = %v", err)
	}
	if first.SnapshotID == second.SnapshotID {
		t.Fatalf("snapshot IDs are equal: %q", first.SnapshotID)
	}
	for _, snapshot := range []SystemSnapshot{first, second} {
		raw := strings.TrimPrefix(snapshot.SnapshotID, "snap-")
		if raw == snapshot.SnapshotID {
			t.Fatalf("snapshot ID %q has no snap- prefix", snapshot.SnapshotID)
		}
		parsed, err := uuid.Parse(raw)
		if err != nil || parsed[6]>>4 != 4 || parsed[8]>>6 != 2 {
			t.Fatalf("snapshot ID %q is not RFC 9562 UUIDv4: bytes=%x err=%v", snapshot.SnapshotID, parsed, err)
		}
		if snapshot.CapturedAt.IsZero() || !snapshot.CapturedAt.Equal(snapshot.Timestamp) {
			t.Fatalf("snapshot timestamps changed semantics: %#v", snapshot)
		}
	}
}

func TestCollectWithProbesMarksFailedSection(t *testing.T) {
	probes := testProbes()
	probes.CPU = func() (float64, error) { return 0, errors.New("cpu unavailable") }
	snapshot, diagnostics, err := New(10, nil).CollectWithProbesDiagnostics(probes)
	if err != nil || !has(snapshot.MissingData, "cpu") || diagnostics.Errors["cpu"] == "" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestCollectWithProbesTreatsGPUAsOptional(t *testing.T) {
	probes := testProbes()
	probes.GPU = func() ([]GPUInfo, error) { return nil, errors.New("NVIDIA unavailable") }
	snapshot, diagnostics, err := New(10, nil).CollectWithProbesDiagnostics(probes)
	if err != nil || !has(snapshot.MissingData, "gpu") || diagnostics.Errors["gpu"] == "" {
		t.Fatalf("snapshot=%#v diagnostics=%#v err=%v", snapshot, diagnostics, err)
	}
}

func has(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestCollectWithProbesRejectsCompleteFailure(t *testing.T) {
	fail := errors.New("unavailable")
	probes := Probes{CPU: func() (float64, error) { return 0, fail }, Memory: func() (*mem.VirtualMemoryStat, error) { return nil, fail }, Swap: func() (*mem.SwapMemoryStat, error) { return nil, fail }, Load: func() (*load.AvgStat, error) { return nil, fail }, Hostname: func() (string, error) { return "", fail }, CPUCores: func() (int, error) { return 0, fail }, Disks: func() ([]DiskInfo, error) { return nil, fail }, DiskIO: func(time.Time) (DiskIOInfo, error) { return DiskIOInfo{}, fail }, Network: func(time.Time) (NetworkInfo, error) { return NetworkInfo{}, fail }, Processes: func() ([]ProcessInfo, error) { return nil, fail }}
	_, err := New(10, nil).CollectWithProbes(probes)
	if !errors.Is(err, ErrCollectionFailed) {
		t.Fatalf("error = %v", err)
	}
}
