package collector

import "time"

type SystemSnapshot struct {
	Timestamp  time.Time     `json:"timestamp"`
	CPUPercent float64       `json:"cpu_percent"`
	MemUsed    uint64        `json:"mem_used"`
	MemTotal   uint64        `json:"mem_total"`
	MemPercent float64       `json:"mem_percent"`
	Disks      []DiskInfo    `json:"disks"`
	DiskIO     DiskIOInfo    `json:"disk_io"`
	Network    NetworkInfo   `json:"network"`
	Processes  []ProcessInfo `json:"processes"`
}

type ProcessInfo struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemPercent float32 `json:"mem_percent"`
}

type DiskInfo struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Free       uint64 `json:"free"`
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
