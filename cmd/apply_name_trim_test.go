package cmd

import "testing"

func TestNormalizeApplyTunnelTrimsNameForDisplay(t *testing.T) {
	normalized, err := normalizeApplyTunnel(applyTunnel{Name: " web", LocalPort: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "web" || normalized.TunnelID != "web" {
		t.Fatalf("expected trimmed name for both display and id, got name=%q tunnelId=%q", normalized.Name, normalized.TunnelID)
	}
}
