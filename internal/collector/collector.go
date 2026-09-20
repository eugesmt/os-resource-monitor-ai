package collector

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"uuid"
)

const defaultMaxProcesses = 100

var ErrCollectionFailed = errors.New("all required system probes failed")

type Probes struct {
	CPU        func() (float64, error)
	CPUPerCore func() ([]float64, error)
	Memory     func() (*mem.VirtualMemoryStat, error)
	Swap       func() (*mem.SwapMemoryStat, error)
	Load       func() (*load.AvgStat, error)
	Hostname   func() (string, error)
	CPUCores   func() (int, error)
	Disks      func() ([]DiskInfo, error)
	DiskIO     func(time.Time) (DiskIOInfo, error)
	Network    func(time.Time) (NetworkInfo, error)
	GPU        func() ([]GPUInfo, error)
	Processes  func() ([]ProcessInfo, error)
}

type Collector struct {
	maxProcesses int
	log          *slog.Logger

	mu         sync.Mutex
	collectMu  sync.Mutex
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
	snapshot, _, err := c.collectWithProbes(defaultProbes(c))
	return snapshot, err
}

func (c *Collector) CollectWithProbes(probes Probes) (SystemSnapshot, error) {
	snapshot, _, err := c.collectWithProbes(completeProbes(c, probes))
	return snapshot, err
}

func (c *Collector) CollectWithDiagnostics() (SystemSnapshot, CollectionDiagnostics, error) {
	return c.collectWithProbes(defaultProbes(c))
}

func (c *Collector) CollectWithProbesDiagnostics(probes Probes) (SystemSnapshot, CollectionDiagnostics, error) {
	return c.collectWithProbes(completeProbes(c, probes))
}

func (c *Collector) collectWithProbes(p Probes) (SystemSnapshot, CollectionDiagnostics, error) {
	c.collectMu.Lock()
	defer c.collectMu.Unlock()

	now := time.Now()
	s := SystemSnapshot{SnapshotID: "snap-" + uuid.NewV4().String(), CapturedAt: now, Timestamp: now, MissingData: make([]string, 0)}
	diagnostics := CollectionDiagnostics{Errors: make(map[string]string)}

	var (
		cpuValue        float64
		cpuErr          error
		cpuPerCoreValue []float64
		cpuPerCoreErr   error
		memoryValue     *mem.VirtualMemoryStat
		memoryErr       error
		swapValue       *mem.SwapMemoryStat
		swapErr         error
		loadValue       *load.AvgStat
		loadErr         error
		hostnameValue   string
		hostnameErr     error
		cpuCoresValue   int
		cpuCoresErr     error
		disksValue      []DiskInfo
		disksErr        error
		diskIOValue     DiskIOInfo
		diskIOErr       error
		networkValue    NetworkInfo
		networkErr      error
		gpuValue        []GPUInfo
		gpuErr          error
		processesValue  []ProcessInfo
		processesErr    error
	)

	var probes sync.WaitGroup
	probes.Add(12)
	go func() { defer probes.Done(); cpuValue, cpuErr = p.CPU() }()
	go func() { defer probes.Done(); cpuPerCoreValue, cpuPerCoreErr = p.CPUPerCore() }()
	go func() { defer probes.Done(); memoryValue, memoryErr = p.Memory() }()
	go func() { defer probes.Done(); swapValue, swapErr = p.Swap() }()
	go func() { defer probes.Done(); loadValue, loadErr = p.Load() }()
	go func() { defer probes.Done(); hostnameValue, hostnameErr = p.Hostname() }()
	go func() { defer probes.Done(); cpuCoresValue, cpuCoresErr = p.CPUCores() }()
	go func() { defer probes.Done(); disksValue, disksErr = p.Disks() }()
	go func() { defer probes.Done(); diskIOValue, diskIOErr = p.DiskIO(now) }()
	go func() { defer probes.Done(); networkValue, networkErr = p.Network(now) }()
	go func() { defer probes.Done(); gpuValue, gpuErr = p.GPU() }()
	go func() { defer probes.Done(); processesValue, processesErr = p.Processes() }()
	probes.Wait()

	failed := 0
	markMissing := func(section string, err error) {
		failed++
		s.MissingData = append(s.MissingData, section)
		if err != nil {
			diagnostics.Errors[section] = err.Error()
		}
	}
	if cpuErr == nil {
		s.CPUPercent = clampPercent(cpuValue)
	} else {
		c.log.Warn("CPU collection failed", "error", cpuErr)
		markMissing("cpu", cpuErr)
	}
	if cpuPerCoreErr == nil {
		s.CPUPerCore = clampPercents(cpuPerCoreValue)
	} else {
		c.log.Debug("per-core CPU collection failed", "error", cpuPerCoreErr)
		s.MissingData = append(s.MissingData, "cpu_per_core")
		diagnostics.Errors["cpu_per_core"] = cpuPerCoreErr.Error()
	}
	if memoryErr == nil {
		s.MemUsed = memoryValue.Used
		s.MemTotal = memoryValue.Total
		s.MemAvailable = memoryValue.Available
		s.MemPercent = clampPercent(memoryValue.UsedPercent)
	} else {
		c.log.Warn("memory collection failed", "error", memoryErr)
		markMissing("memory", memoryErr)
	}
	if swapErr == nil {
		s.SwapUsed, s.SwapTotal = swapValue.Used, swapValue.Total
	} else {
		markMissing("swap", swapErr)
	}
	if loadErr == nil {
		s.Load1, s.Load5, s.Load15 = loadValue.Load1, loadValue.Load5, loadValue.Load15
	} else {
		markMissing("load", loadErr)
	}
	if hostnameErr == nil {
		s.Hostname = hostnameValue
	} else {
		markMissing("host", hostnameErr)
	}
	if cpuCoresErr == nil {
		s.CPUCores = cpuCoresValue
	} else {
		markMissing("cpu_cores", cpuCoresErr)
	}
	if disksErr == nil {
		s.Disks = disksValue
	} else {
		c.log.Warn("disk collection failed", "error", disksErr)
		markMissing("disks", disksErr)
	}
	if diskIOErr == nil {
		s.DiskIO = diskIOValue
	} else {
		c.log.Warn("disk I/O collection failed", "error", diskIOErr)
		markMissing("disk_io", diskIOErr)
	}
	if networkErr == nil {
		s.Network = networkValue
	} else {
		c.log.Warn("network collection failed", "error", networkErr)
		markMissing("network", networkErr)
	}
	if gpuErr == nil {
		s.GPU = gpuValue
	} else {
		c.log.Debug("NVIDIA GPU collection unavailable", "error", gpuErr)
		// GPU — необязательная секция: отсутствие NVIDIA/NVML не ломает snapshot.
		s.MissingData = append(s.MissingData, "gpu")
		diagnostics.Errors["gpu"] = gpuErr.Error()
	}
	if processesErr == nil {
		s.Processes = processesValue
	} else {
		c.log.Warn("process collection failed", "error", processesErr)
		markMissing("processes", processesErr)
	}
	if failed >= 9 {
		return s, diagnostics, ErrCollectionFailed
	}
	return s, diagnostics, nil
}

func defaultProbes(c *Collector) Probes {
	return Probes{
		CPU: func() (float64, error) {
			values, err := cpu.Percent(0, false)
			if err != nil {
				return 0, err
			}
			if len(values) == 0 {
				return 0, errors.New("CPU probe returned no values")
			}
			return values[0], nil
		},
		CPUPerCore: func() ([]float64, error) {
			return cpu.Percent(0, true)
		},
		Memory:    mem.VirtualMemory,
		Swap:      mem.SwapMemory,
		Load:      load.Avg,
		Hostname:  os.Hostname,
		CPUCores:  func() (int, error) { return cpu.Counts(true) },
		Disks:     collectDisks,
		DiskIO:    c.collectDiskIO,
		Network:   c.collectNetwork,
		GPU:       collectNVIDIAGPU,
		Processes: c.collectProcesses,
	}
}

func completeProbes(c *Collector, p Probes) Probes {
	d := defaultProbes(c)
	if p.CPU != nil {
		d.CPU = p.CPU
	}
	if p.CPUPerCore != nil {
		d.CPUPerCore = p.CPUPerCore
	}
	if p.Memory != nil {
		d.Memory = p.Memory
	}
	if p.Swap != nil {
		d.Swap = p.Swap
	}
	if p.Load != nil {
		d.Load = p.Load
	}
	if p.Hostname != nil {
		d.Hostname = p.Hostname
	}
	if p.CPUCores != nil {
		d.CPUCores = p.CPUCores
	}
	if p.Disks != nil {
		d.Disks = p.Disks
	}
	if p.DiskIO != nil {
		d.DiskIO = p.DiskIO
	}
	if p.Network != nil {
		d.Network = p.Network
	}
	if p.GPU != nil {
		d.GPU = p.GPU
	}
	if p.Processes != nil {
		d.Processes = p.Processes
	}
	return d
}

func (c *Collector) Start(ctx context.Context, interval time.Duration, callback func(SystemSnapshot)) {
	if interval <= 0 {
		interval = 2 * time.Second
	}

	c.wg.Go(func() {
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
	})
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
			Device:             p.Device,
			Mountpoint:         p.Mountpoint,
			Total:              usage.Total,
			Used:               usage.Used,
			Free:               usage.Free,
			UtilizationPercent: clampPercent(float64(usage.UsedPercent)),
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

		var memBytes uint64
		if memory, err := p.MemoryInfo(); err == nil && memory != nil {
			memBytes = memory.RSS
		}
		var status string
		if statuses, err := p.Status(); err == nil {
			status = strings.Join(statuses, ",")
		}
		var startedAt time.Time
		if createdAt, err := p.CreateTime(); err == nil && createdAt > 0 {
			startedAt = time.UnixMilli(createdAt)
		}
		var threads int32
		if count, err := p.NumThreads(); err == nil {
			threads = count
		}

		result = append(result, ProcessInfo{
			PID: p.Pid, Name: name, CPUPercent: clampPercent(cpuPct),
			MemPercent: float32(clampPercent(float64(memPct))), MemBytes: memBytes,
			Status: status, StartedAt: startedAt, Threads: threads,
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

func clampPercents(values []float64) []float64 {
	result := make([]float64, len(values))
	for i, value := range values {
		result[i] = clampPercent(value)
	}
	return result
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
