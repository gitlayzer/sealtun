package cmd

import (
	"strings"
	"testing"
)

func TestControlCharFilterWriterStripsEscapeSequences(t *testing.T) {
	var buf strings.Builder
	w := newControlCharFilterWriter(&buf)
	payload := "normal line\n\x1b[2J\x1b[Hfake prompt\x1b]52;c;malicious\x07more text\ttab\n"
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("escape character leaked: %q", got)
	}
	if strings.Contains(got, "\x07") {
		t.Fatalf("bell character leaked: %q", got)
	}
	if !strings.Contains(got, "normal line\n") || !strings.Contains(got, "more text\ttab\n") {
		t.Fatalf("legitimate content was modified: %q", got)
	}
	if !strings.Contains(got, "[2J[Hfake prompt") {
		t.Fatalf("expected escape brackets stripped but text kept, got %q", got)
	}
}
