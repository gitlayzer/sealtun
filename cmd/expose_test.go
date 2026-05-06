package cmd

import "testing"

func TestValidateLocalPort(t *testing.T) {
	t.Parallel()

	validPorts := []string{"1", "8080", "65535"}
	for _, port := range validPorts {
		if err := validateLocalPort(port); err != nil {
			t.Fatalf("expected port %s to be valid, got error: %v", port, err)
		}
	}

	invalidPorts := []string{"0", "65536", "-1", "abc"}
	for _, port := range invalidPorts {
		if err := validateLocalPort(port); err == nil {
			t.Fatalf("expected port %s to be invalid", port)
		}
	}
}

func TestValidateProtocol(t *testing.T) {
	t.Parallel()

	validProtocols := []string{"https", "HTTPS"}
	for _, protocol := range validProtocols {
		if err := validateProtocol(protocol); err != nil {
			t.Fatalf("expected protocol %s to be valid, got error: %v", protocol, err)
		}
	}

	invalidProtocols := []string{"http", "grpc", "grpcs", "tcp", "udp", "ws", "wss", ""}
	for _, protocol := range invalidProtocols {
		if err := validateProtocol(protocol); err == nil {
			t.Fatalf("expected %s to be rejected", protocol)
		}
	}
}
