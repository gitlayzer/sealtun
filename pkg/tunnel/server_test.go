package tunnel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerUnavailablePageWhenClientDisconnected(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 status, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sealtun Tunnel Status") {
		t.Fatal("missing fallback page shell")
	}
	if !strings.Contains(body, "localhost:3000") {
		t.Fatal("fallback page should include expected local port")
	}
}

func TestServerHealthzReflectsDisconnectedClient(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 status, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"clientConnected":false`) {
		t.Fatalf("unexpected health body: %s", body)
	}
	if !strings.Contains(body, `"protocol":"https"`) {
		t.Fatalf("health body should include protocol: %s", body)
	}
}
