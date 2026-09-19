//go:build windows

package collector

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	nvmlSuccess        = 0
	nvmlTemperatureGPU = 0
	nvmlNameBufferSize = 256
)

type nvidiaWindowsLibrary struct {
	dll                  *syscall.LazyDLL
	init                 *syscall.LazyProc
	shutdown             *syscall.LazyProc
	deviceGetCount       *syscall.LazyProc
	deviceGetHandle      *syscall.LazyProc
	deviceGetName        *syscall.LazyProc
	deviceGetUtilization *syscall.LazyProc
	deviceGetMemory      *syscall.LazyProc
	deviceGetTemperature *syscall.LazyProc
}

func collectNVIDIAGPU() ([]GPUInfo, error) {
	library := &nvidiaWindowsLibrary{dll: syscall.NewLazyDLL("nvml.dll")}
	library.init = library.dll.NewProc("nvmlInit_v2")
	library.shutdown = library.dll.NewProc("nvmlShutdown")
	library.deviceGetCount = library.dll.NewProc("nvmlDeviceGetCount_v2")
	library.deviceGetHandle = library.dll.NewProc("nvmlDeviceGetHandleByIndex_v2")
	library.deviceGetName = library.dll.NewProc("nvmlDeviceGetName")
	library.deviceGetUtilization = library.dll.NewProc("nvmlDeviceGetUtilizationRates")
	library.deviceGetMemory = library.dll.NewProc("nvmlDeviceGetMemoryInfo")
	library.deviceGetTemperature = library.dll.NewProc("nvmlDeviceGetTemperature")

	if ret, _, err := library.init.Call(); ret != nvmlSuccess {
		return nil, fmt.Errorf("NVML initialization failed: %d (%v)", ret, err)
	}
	defer library.shutdown.Call()

	var count uint32
	if ret, _, err := library.deviceGetCount.Call(uintptr(unsafe.Pointer(&count))); ret != nvmlSuccess {
		return nil, fmt.Errorf("NVIDIA device count failed: %d (%v)", ret, err)
	}
	result := make([]GPUInfo, 0, count)
	for index := uint32(0); index < count; index++ {
		var device uintptr
		if ret, _, err := library.deviceGetHandle.Call(uintptr(index), uintptr(unsafe.Pointer(&device))); ret != nvmlSuccess {
			return nil, fmt.Errorf("NVIDIA device %d handle failed: %d (%v)", index, ret, err)
		}

		var name [nvmlNameBufferSize]byte
		if ret, _, err := library.deviceGetName.Call(device, uintptr(unsafe.Pointer(&name[0])), nvmlNameBufferSize); ret != nvmlSuccess {
			return nil, fmt.Errorf("NVIDIA device %d name failed: %d (%v)", index, ret, err)
		}
		var utilization struct{ GPU, Memory uint32 }
		if ret, _, err := library.deviceGetUtilization.Call(device, uintptr(unsafe.Pointer(&utilization))); ret != nvmlSuccess {
			return nil, fmt.Errorf("NVIDIA device %d utilization failed: %d (%v)", index, ret, err)
		}
		var memory struct{ Total, Free, Used uint64 }
		if ret, _, err := library.deviceGetMemory.Call(device, uintptr(unsafe.Pointer(&memory))); ret != nvmlSuccess {
			return nil, fmt.Errorf("NVIDIA device %d memory failed: %d (%v)", index, ret, err)
		}

		nameBytes := name[:]
		if end := bytes.IndexByte(nameBytes, 0); end >= 0 {
			nameBytes = nameBytes[:end]
		}
		info := GPUInfo{Index: int(index), Name: string(nameBytes), UtilizationPercent: utilization.GPU, MemoryUsed: memory.Used, MemoryTotal: memory.Total}
		var temperature uint32
		if ret, _, _ := library.deviceGetTemperature.Call(device, nvmlTemperatureGPU, uintptr(unsafe.Pointer(&temperature))); ret == nvmlSuccess {
			info.TemperatureCelsius = temperature
		}
		result = append(result, info)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("NVIDIA GPU not found")
	}
	return result, nil
}
