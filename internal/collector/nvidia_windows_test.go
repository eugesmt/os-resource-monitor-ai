//go:build windows

package collector

import "testing"

func TestLoadNVIDIAWindowsLibraryMissingDLLReturnsError(t *testing.T) {
	library, err := loadNVIDIAWindowsLibrary("system-monitor-ai-missing-nvml.dll")
	if err == nil {
		t.Fatal("missing NVML DLL loaded successfully")
	}
	if library != nil {
		t.Fatalf("library = %#v, want nil", library)
	}
}

func TestLoadNVIDIAWindowsLibraryMissingSymbolReturnsError(t *testing.T) {
	library, err := loadNVIDIAWindowsLibrary("kernel32.dll")
	if err == nil {
		t.Fatal("NVML symbols unexpectedly found in kernel32.dll")
	}
	if library != nil {
		t.Fatalf("library = %#v, want nil", library)
	}
}
