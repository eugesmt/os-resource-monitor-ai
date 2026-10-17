package storage

import (
	"sync"
	"testing"
	"time"

	"system-monitor-ai/internal/collector"
)

func snapshot(ts int64) collector.SystemSnapshot {
	return collector.SystemSnapshot{Timestamp: time.Unix(ts, 0), CPUPercent: float64(ts)}
}

func TestRingBufferOverflowAndOrder(t *testing.T) {
	r := NewRingBuffer(3)
	r.Add(snapshot(1))
	r.Add(snapshot(2))
	r.Add(snapshot(3))
	r.Add(snapshot(4))

	got := r.GetAll()
	if len(got) != 3 || got[0].CPUPercent != 2 || got[1].CPUPercent != 3 || got[2].CPUPercent != 4 {
		t.Fatalf("unexpected order: %+v", got)
	}
}

func TestRingBufferConcurrentAccess(t *testing.T) {
	r := NewRingBuffer(50)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Add(snapshot(int64(offset*1000 + j)))
				_ = r.GetLast(10)
			}
		}(i)
	}
	wg.Wait()
}

func TestRingBufferGetLastDoesNotAliasSnapshotSlices(t *testing.T) {
	r := NewRingBuffer(1)
	r.Add(collector.SystemSnapshot{CPUPerCore: []float64{10}, GPU: []collector.GPUInfo{{Name: "gpu"}}, MissingData: []string{"gpu"}})
	got := r.GetLast(1)
	got[0].CPUPerCore[0] = 99
	got[0].GPU[0].Name = "changed"
	got[0].MissingData[0] = "changed"
	want := r.GetLast(1)[0]
	if want.CPUPerCore[0] != 10 || want.GPU[0].Name != "gpu" || want.MissingData[0] != "gpu" {
		t.Fatalf("ring buffer snapshot aliases internal slices: %#v", want)
	}
}
