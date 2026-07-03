//go:build linux

package clusterconnect

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestSockaddrPortUsesNetworkByteOrder(t *testing.T) {
	buf := []byte{0x16, 0x2e} // 5678
	raw := *(*uint16)(unsafe.Pointer(&buf[0]))
	if got := sockaddrPort(raw); got != binary.BigEndian.Uint16(buf) {
		t.Fatalf("expected port 5678, got %d", got)
	}
}

func TestWriteHostsBlockIsAtomicAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o640); err != nil {
		t.Fatalf("write hosts fixture: %v", err)
	}

	if err := writeHostsBlock(path, []HostEntry{{IP: "10.96.0.12", Host: "web", Also: []string{"web.default.svc.cluster.local"}}}); err != nil {
		t.Fatalf("write hosts block: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read hosts fixture: %v", err)
	}
	if got := string(data); !strings.Contains(got, "10.96.0.12\tweb web.default.svc.cluster.local") {
		t.Fatalf("expected managed hosts block, got %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat hosts fixture: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected mode 0640, got %v", info.Mode().Perm())
	}
}
