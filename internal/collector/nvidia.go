//go:build linux

package collector

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// collectNVIDIAGPU получает базовые метрики NVIDIA через NVML.
// Если драйвер или NVML недоступны, секция считается необязательной и
// возвращает ошибку, которая попадёт в missing_data, не ломая snapshot.
func collectNVIDIAGPU() ([]GPUInfo, error) {
	library := nvml.New()
	if ret := library.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("NVML initialization failed: %s", library.ErrorString(ret))
	}
	defer library.Shutdown()

	count, ret := library.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("NVIDIA device count failed: %s", library.ErrorString(ret))
	}
	result := make([]GPUInfo, 0, count)
	for index := 0; index < count; index++ {
		device, ret := library.DeviceGetHandleByIndex(index)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("NVIDIA device %d handle failed: %s", index, library.ErrorString(ret))
		}
		name, ret := library.DeviceGetName(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("NVIDIA device %d name failed: %s", index, library.ErrorString(ret))
		}
		utilization, ret := library.DeviceGetUtilizationRates(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("NVIDIA device %d utilization failed: %s", index, library.ErrorString(ret))
		}
		memory, ret := library.DeviceGetMemoryInfo(device)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("NVIDIA device %d memory failed: %s", index, library.ErrorString(ret))
		}

		info := GPUInfo{
			Index:              index,
			Name:               name,
			UtilizationPercent: utilization.Gpu,
			MemoryUsed:         memory.Used,
			MemoryTotal:        memory.Total,
		}
		if temperature, temperatureRet := library.DeviceGetTemperature(device, nvml.TEMPERATURE_GPU); temperatureRet == nvml.SUCCESS {
			info.TemperatureCelsius = temperature
		}
		result = append(result, info)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("NVIDIA GPU not found")
	}
	return result, nil
}
