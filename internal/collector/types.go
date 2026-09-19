package collector

import "time"

type SystemSnapshot struct {
	SnapshotID string    `json:"snapshot_id"`
	CapturedAt time.Time `json:"captured_at"`
	// Timestamp сохранён для совместимости с существующими UI- и API-клиентами.
	Timestamp    time.Time     `json:"timestamp"`
	Hostname     string        `json:"hostname"`
	CPUCores     int           `json:"cpu_cores"`
	CPUPerCore   []float64     `json:"cpu_per_core"`
	Load1        float64       `json:"load_1"`
	Load5        float64       `json:"load_5"`
	Load15       float64       `json:"load_15"`
	CPUPercent   float64       `json:"cpu_percent"`
	MemUsed      uint64        `json:"mem_used"`
	MemTotal     uint64        `json:"mem_total"`
	MemAvailable uint64        `json:"mem_available"`
	MemPercent   float64       `json:"mem_percent"`
	SwapUsed     uint64        `json:"swap_used"`
	SwapTotal    uint64        `json:"swap_total"`
	Disks        []DiskInfo    `json:"disks"`
	DiskIO       DiskIOInfo    `json:"disk_io"`
	Network      NetworkInfo   `json:"network"`
	GPU          []GPUInfo     `json:"gpu"`
	Processes    []ProcessInfo `json:"processes"`
	MissingData  []string      `json:"missing_data"`
}

type CollectionDiagnostics struct {
	Errors map[string]string `json:"errors"`
}

type GPUInfo struct {
	Index              int    `json:"index"`
	Name               string `json:"name"`
	UtilizationPercent uint32 `json:"utilization_percent"`
	MemoryUsed         uint64 `json:"memory_used"`
	MemoryTotal        uint64 `json:"memory_total"`
	TemperatureCelsius uint32 `json:"temperature_celsius"`
}

type ProcessInfo struct {
	PID        int32     `json:"pid"`
	Name       string    `json:"name"`
	CPUPercent float64   `json:"cpu_percent"`
	MemPercent float32   `json:"mem_percent"`
	MemBytes   uint64    `json:"mem_bytes"`
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	Threads    int32     `json:"threads"`
}

type DiskInfo struct {
	Device             string  `json:"device"`
	Mountpoint         string  `json:"mountpoint"`
	Total              uint64  `json:"total"`
	Used               uint64  `json:"used"`
	Free               uint64  `json:"free"`
	UtilizationPercent float64 `json:"utilization_percent"`
}

type DiskIOInfo struct {
	ReadBytes   uint64  `json:"read_bytes"`
	WriteBytes  uint64  `json:"write_bytes"`
	ReadPerSec  float64 `json:"read_per_sec"`
	WritePerSec float64 `json:"write_per_sec"`
}

type NetworkInfo struct {
	BytesSent  uint64  `json:"bytes_sent"`
	BytesRecv  uint64  `json:"bytes_recv"`
	SendPerSec float64 `json:"send_per_sec"`
	RecvPerSec float64 `json:"recv_per_sec"`
}
