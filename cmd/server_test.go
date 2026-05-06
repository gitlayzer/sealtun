package cmd

import "testing"

func TestServerProtocolValidation(t *testing.T) {
	t.Parallel()

	valid := []string{"https", "HTTPS"}
	for _, protocol := range valid {
		if err := validateProtocol(protocol); err != nil {
			t.Fatalf("expected expose protocol %s to be valid: %v", protocol, err)
		}
	}

	invalid := []string{"http", "grpc", "grpcs", "tcp", "udp", "wss"}
	for _, protocol := range invalid {
		if err := validateProtocol(protocol); err == nil {
			t.Fatalf("expected expose protocol %s to be rejected", protocol)
		}
	}
}
