//go:build !linux && !windows

package collector

import "errors"

// collectNVIDIAGPU сообщает об отсутствии поддержанного NVML backend.
// Это необязательная секция и не должна блокировать системный snapshot.
func collectNVIDIAGPU() ([]GPUInfo, error) {
	return nil, errors.New("NVIDIA GPU probe is supported only on Linux/WSL in this build")
}
