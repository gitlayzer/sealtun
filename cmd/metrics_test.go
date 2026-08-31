package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/session"
)

func TestMetricsHTTPClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	client := newMetricsHTTPClient()
	req, err := http.NewRequest(http.MethodGet, "https://example.com/_sealtun/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	redirectReq, err := http.NewRequest(http.MethodGet, "https://evil.example/_sealtun/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := client.CheckRedirect(redirectReq, []*http.Request{req}); err != http.ErrUseLastResponse {
		t.Fatalf("expected redirects to be blocked, got %v", err)
	}
}

func TestDecodeMetricsJSONRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	var payload map[string]interface{}
	body := `{"totalRequests":1}` + strings.Repeat(" ", metricsResponseMaxBytes)
	if err := decodeMetricsJSON(strings.NewReader(body), &payload); err == nil {
		t.Fatal("expected oversized metrics response to be rejected")
	}
}

func TestFetchServerMetricsRejectsInvalidSessionHost(t *testing.T) {
	t.Parallel()

	_, err := fetchServerMetrics(context.Background(), session.TunnelSession{
		SealosHost: "sealtun.example.com@127.0.0.1",
		Secret:     "secret",
	})
	if err == nil {
		t.Fatal("expected invalid session host to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid session metrics host") {
		t.Fatalf("unexpected error: %v", err)
	}
}
