package mesh

import (
	"net/http"
	"testing"
)

func TestCloneTargetHeadersStripsMeshToken(t *testing.T) {
	header := http.Header{}
	header.Set(gatewayTokenHeader, "secret")
	header.Set("Authorization", "Bearer secret")

	out := cloneTargetHeaders(header, "secret")
	if out.Get(gatewayTokenHeader) != "" {
		t.Fatalf("gateway token header leaked to target: %#v", out)
	}
	if out.Get("Authorization") != "" {
		t.Fatalf("gateway authorization leaked to target: %#v", out)
	}
}

func TestCloneTargetHeadersPreservesApplicationAuthorization(t *testing.T) {
	header := http.Header{}
	header.Set(gatewayTokenHeader, "secret")
	header.Set("Authorization", "Bearer app-token")

	out := cloneTargetHeaders(header, "secret")
	if out.Get(gatewayTokenHeader) != "" {
		t.Fatalf("gateway token header leaked to target: %#v", out)
	}
	if out.Get("Authorization") != "Bearer app-token" {
		t.Fatalf("application authorization was not preserved: %#v", out)
	}
}
