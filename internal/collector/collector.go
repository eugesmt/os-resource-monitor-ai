package collector

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

const defaultMaxProcesses = 100

type Collector struct {
	maxProcesses int
	log          *slog.Logger

	mu         sync.Mutex
	prevNet    NetworkInfo
	prevNetAt  time.Time
	prevDisk   DiskIOInfo
	prevDiskAt time.Time
	wg         sync.WaitGroup
}

func New(maxProcesses int, logger *slog.Logger) *Collector {
	if maxProcesses <= 0 {
		maxProcesses = defaultMaxProcesses
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{maxProcesses: maxProcesses, log: logger}
}

func (c *Collector) Collect() (SystemSnapshot, error) {
	now := time.Now()
	s := SystemSnapshot{Timestamp: now}

	if values, err := cpu.Percent(0, false); err == nil && len(values) > 0 {
		s.CPUPercent = clampPercent(values[0])
	} else if err != nil {
		c.log.Warn("CPU collection failed", "error", err)
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemUsed = vm.Used
		s.MemTotal = vm.Total
		s.MemPercent = clampPercent(vm.UsedPercent)
	} else {
		c.log.Warn("memory collection failed", "error", err)
	}

	if disks, err := collectDisks(); err == nil {
		s.Disks = disks
	} else {
		c.log.Warn("disk collection failed", "error", err)
	}
	if dio, err := c.collectDiskIO(now); err == nil {
		s.DiskIO = dio
	} else {
		c.log.Warn("disk I/O collection failed", "error", err)
	}

	if ni, err := c.collectNetwork(now); err == nil {
		s.Network = ni
	} else {
		c.log.Warn("network collection failed", "error", err)
	}

	if procs, err := c.collectProcesses(); err == nil {
		s.Processes = procs
	} else {
		c.log.Warn("process collection failed", "error", err)
	}

	return s, nil
}

func (c *Collector) Start(ctx context.Context, interval time.Duration, callback func(SystemSnapshot)) {
	if interval <= 0 {
		interval = 2 * time.Second
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		collectAndPublish := func() {
			snapshot, err := c.Collect()
			if err != nil {
				c.log.Error("collection cycle failed", "error", err)
				return
			}
			if callback != nil {
				callback(snapshot)
			}
		}

		collectAndPublish()
		for {
			select {
			case <-ticker.C:
				collectAndPublish()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func collectDisks() ([]DiskInfo, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, err
	}

	result := make([]DiskInfo, 0, len(partitions))
	for _, p := range partitions {
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		result = append(result, DiskInfo{
			Device:     p.Device,
			Mountpoint: p.Mountpoint,
			Total:      usage.Total,
			Used:       usage.Used,
			Free:       usage.Free,
		})
	}
	return result, nil
}

func (c *Collector) collectNetwork(now time.Time) (NetworkInfo, error) {
	counters, err := net.IOCounters(false)
	if err != nil {
		return NetworkInfo{}, err
	}
	if len(counters) == 0 {
		return NetworkInfo{}, nil
	}

	current := NetworkInfo{
		BytesSent: counters[0].BytesSent,
		BytesRecv: counters[0].BytesRecv,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.prevNetAt.IsZero() {
		seconds := now.Sub(c.prevNetAt).Seconds()
		if seconds > 0 && current.BytesSent >= c.prevNet.BytesSent && current.BytesRecv >= c.prevNet.BytesRecv {
			current.SendPerSec = float64(current.BytesSent-c.prevNet.BytesSent) / seconds
			current.RecvPerSec = float64(current.BytesRecv-c.prevNet.BytesRecv) / seconds
		}
	}
	c.prevNet = current
	c.prevNetAt = now
	return current, nil
}

func (c *Collector) collectProcesses() ([]ProcessInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}

	result := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		name, err := p.Name()
		if err != nil {
			continue
		}
		cpuPct, err := p.CPUPercent()
		if err != nil {
			continue
		}
		memPct, err := p.MemoryPercent()
		if err != nil {
			continue
		}

		result = append(result, ProcessInfo{
			PID:        p.Pid,
			Name:       name,
			CPUPercent: clampPercent(cpuPct),
			MemPercent: float32(clampPercent(float64(memPct))),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].CPUPercent == result[j].CPUPercent {
			return result[i].MemPercent > result[j].MemPercent
		}
		return result[i].CPUPercent > result[j].CPUPercent
	})

	if len(result) > c.maxProcesses {
		result = result[:c.maxProcesses]
	}
	return result, nil
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (c *Collector) Wait() {
	c.wg.Wait()
}

func (c *Collector) collectDiskIO(now time.Time) (DiskIOInfo, error) {
	counters, err := disk.IOCounters()
	if err != nil {
		return DiskIOInfo{}, err
	}
	var current DiskIOInfo
	for _, v := range counters {
		current.ReadBytes += v.ReadBytes
		current.WriteBytes += v.WriteBytes
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.prevDiskAt.IsZero() {
		seconds := now.Sub(c.prevDiskAt).Seconds()
		if seconds > 0 && current.ReadBytes >= c.prevDisk.ReadBytes && current.WriteBytes >= c.prevDisk.WriteBytes {
			current.ReadPerSec = float64(current.ReadBytes-c.prevDisk.ReadBytes) / seconds
			current.WritePerSec = float64(current.WriteBytes-c.prevDisk.WriteBytes) / seconds
		}
	}
	c.prevDisk = current
	c.prevDiskAt = now
	return current, nil
}
