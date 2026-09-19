package server

import (
	"testing"

	"taskmanager-ai/internal/collector"
	"taskmanager-ai/internal/storage"
)

func TestStateGetSnapshotDoesNotAliasSnapshotSlices(t *testing.T) {
	state := NewRuntime(nil, storage.NewRingBuffer(1), nil)
	state.state.snapshot = collector.SystemSnapshot{CPUPerCore: []float64{10}, GPU: []collector.GPUInfo{{Name: "gpu"}}, MissingData: []string{"gpu"}}
	state.state.hasSnapshot = true

	got, err := state.GetSnapshot()
	if err != nil {
		t.Fatalf("GetSnapshot() error = %v", err)
	}
	got.CPUPerCore[0] = 99
	got.GPU[0].Name = "changed"
	got.MissingData[0] = "changed"
	want, err := state.GetSnapshot()
	if err != nil {
		t.Fatalf("second GetSnapshot() error = %v", err)
	}
	if want.CPUPerCore[0] != 10 || want.GPU[0].Name != "gpu" || want.MissingData[0] != "gpu" {
		t.Fatalf("state snapshot aliases internal slices: %#v", want)
	}
}
