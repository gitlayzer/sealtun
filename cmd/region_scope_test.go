package cmd

import (
	"testing"

	"github.com/labring/sealtun/pkg/session"
)

func TestRegionMatches(t *testing.T) {
	pairs := [][2]string{
		{"https://gzg.sealos.run", "https://gzg.sealos.run/"},
		{"https://gzg.sealos.run/", "https://gzg.sealos.run"},
		{" https://gzg.sealos.run ", "https://gzg.sealos.run"},
		{"HTTPS://GZG.SEALOS.RUN/", "https://gzg.sealos.run"},
	}
	for _, pair := range pairs {
		if !regionMatches(pair[0], pair[1]) {
			t.Fatalf("expected %q to match %q", pair[0], pair[1])
		}
	}
	if regionMatches("https://gzg.sealos.run", "https://bja.sealos.run") {
		t.Fatal("different regions must not match")
	}
}

func TestSessionExpiredTreatsUnparseableAsNotExpired(t *testing.T) {
	sess := session.TunnelSession{TunnelID: "abc123", ExpiresAt: "not-a-timestamp"}
	if sessionExpired(sess, nowUTC()) {
		t.Fatal("unparseable expiresAt must not be treated as expired (daemon auto-deletes on this)")
	}
	sess.ExpiresAt = "2020-01-01T00:00:00Z"
	if !sessionExpired(sess, nowUTC()) {
		t.Fatal("past expiresAt should be expired")
	}
}
