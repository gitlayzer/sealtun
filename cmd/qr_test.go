package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintTerminalQRRendersBlocks(t *testing.T) {
	var buf bytes.Buffer
	printTerminalQR(&buf, "https://sealtun-web-ns-x.sealosgzg.site")
	out := buf.String()
	if out == "" {
		t.Fatal("expected QR output, got empty string")
	}
	if !strings.ContainsAny(out, "▄▀█") {
		t.Fatalf("expected half-block QR glyphs in output, got %q", out[:min(len(out), 80)])
	}
	lines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	if lines < 10 {
		t.Fatalf("expected a multi-line QR code, got %d lines", lines)
	}
}

func TestPrintTerminalQRDiffersPerContent(t *testing.T) {
	var a, b bytes.Buffer
	printTerminalQR(&a, "https://a.example.com")
	printTerminalQR(&b, "https://b.example.com")
	if a.String() == b.String() {
		t.Fatal("different contents produced identical QR output")
	}
}

func TestValidateExposeQRFlag(t *testing.T) {
	original := exposeQR
	t.Cleanup(func() { exposeQR = original })

	exposeQR = false
	if err := validateExposeQRFlag("ssh"); err != nil {
		t.Fatalf("--qr unset should allow ssh, got %v", err)
	}

	exposeQR = true
	if err := validateExposeQRFlag("https"); err != nil {
		t.Fatalf("--qr should allow https, got %v", err)
	}
	for _, p := range []string{"ssh", "tcp"} {
		err := validateExposeQRFlag(p)
		if err == nil || !strings.Contains(err.Error(), "--qr is only supported for https tunnels") {
			t.Fatalf("expected --qr rejection for %s, got %v", p, err)
		}
	}
}
