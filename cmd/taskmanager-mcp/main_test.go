package main

import "testing"

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"} {
		if err := validateLoopbackAddress(address); err != nil {
			t.Errorf("validateLoopbackAddress(%q) = %v", address, err)
		}
	}
}

func TestValidateLoopbackAddressRejectsUnsafeOrInvalidAddress(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8090", "192.0.2.1:8090", ":8090", "127.0.0.1"} {
		if err := validateLoopbackAddress(address); err == nil {
			t.Errorf("validateLoopbackAddress(%q) succeeded", address)
		}
	}
}
