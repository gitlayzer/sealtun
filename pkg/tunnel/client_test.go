package tunnel

import (
	"strings"
	"testing"
)

func TestUnavailableResponse(t *testing.T) {
	t.Parallel()

	response := unavailableResponse("3000")

	if !strings.HasPrefix(response, "HTTP/1.1 502 Bad Gateway\r\n") {
		t.Fatalf("unexpected status line: %q", response)
	}
	if !strings.Contains(response, "Content-Type: text/html; charset=utf-8\r\n") {
		t.Fatal("missing content type header")
	}
	if !strings.Contains(response, "Sealtun Tunnel Status") {
		t.Fatal("missing sealtun status shell")
	}
	if !strings.Contains(response, "localhost:3000") {
		t.Fatal("response should show expected local target")
	}
	if !strings.Contains(response, "Refresh this page after the local service is ready.") {
		t.Fatal("response should explain recovery step")
	}
	if !strings.Contains(response, "<strong>3000</strong>") {
		t.Fatal("response should mention the local port")
	}
}
