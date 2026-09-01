package storage

import (
	"sync"

	"taskmanager-ai/internal/collector"
)

type RingBuffer struct {
	mu    sync.RWMutex
	data  []collector.SystemSnapshot
	head  int
	count int
	limit int
}

func NewRingBuffer(limit int) *RingBuffer {
	if limit <= 0 {
		limit = 1
	}
	return &RingBuffer{data: make([]collector.SystemSnapshot, limit), limit: limit}
}

func (r *RingBuffer) Add(snapshot collector.SystemSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data[r.head] = cloneSnapshot(snapshot)
	r.head = (r.head + 1) % r.limit
	if r.count < r.limit {
		r.count++
	}
}

func (r *RingBuffer) GetAll() []collector.SystemSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.getLastLocked(r.count)
}

func (r *RingBuffer) GetLast(n int) []collector.SystemSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n < 0 {
		n = 0
	}
	if n > r.count {
		n = r.count
	}
	return r.getLastLocked(n)
}

func (r *RingBuffer) getLastLocked(n int) []collector.SystemSnapshot {
	out := make([]collector.SystemSnapshot, 0, n)
	start := (r.head - n + r.limit) % r.limit
	for i := 0; i < n; i++ {
		out = append(out, cloneSnapshot(r.data[(start+i)%r.limit]))
	}
	return out
}

func cloneSnapshot(s collector.SystemSnapshot) collector.SystemSnapshot {
	copySnapshot := s
	copySnapshot.Disks = append([]collector.DiskInfo(nil), s.Disks...)
	copySnapshot.Processes = append([]collector.ProcessInfo(nil), s.Processes...)
	return copySnapshot
}
