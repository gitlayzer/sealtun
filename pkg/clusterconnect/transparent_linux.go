//go:build linux

package clusterconnect

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	hostsPath       = "/etc/hosts"
	iptablesBinName = "iptables"
)

type OriginalDestination struct {
	IP   net.IP
	Port int
}

func requireTransparentPrivileges() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("transparent connect requires root on Linux to update iptables and /etc/hosts; rerun with sudo")
	}
	if _, err := exec.LookPath(iptablesBinName); err != nil {
		return fmt.Errorf("iptables is required for transparent connect: %w", err)
	}
	if err := ensureRootOwnedPrivateFile(hostsPath); err != nil {
		return fmt.Errorf("validate %s: %w", hostsPath, err)
	}
	return nil
}

func applyTransparentPlan(plan *TransparentPlan) error {
	if plan == nil {
		return fmt.Errorf("transparent plan is required")
	}
	if err := writeHostsBlock(hostsPath, plan.Hosts); err != nil {
		return err
	}
	_, redirectPort, err := splitListen(plan.Listen)
	if err != nil {
		_ = clearHostsBlock(hostsPath)
		return err
	}
	for _, rule := range plan.Rules {
		args := iptablesRuleArgs("-C", rule, redirectPort)
		if err := runIptables(args...); err == nil {
			continue
		}
		args = iptablesRuleArgs("-A", rule, redirectPort)
		if err := runIptables(args...); err != nil {
			_ = cleanupTransparentPlan(plan)
			return err
		}
	}
	return nil
}

func cleanupTransparentPlan(plan *TransparentPlan) error {
	var errs []string
	if plan != nil {
		_, redirectPort, err := splitListen(plan.Listen)
		if err == nil {
			for _, rule := range plan.Rules {
				for {
					err := runIptables(iptablesRuleArgs("-D", rule, redirectPort)...)
					if err != nil {
						break
					}
				}
			}
		} else {
			errs = append(errs, err.Error())
		}
	}
	if err := clearHostsBlock(hostsPath); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func iptablesRuleArgs(action string, rule RedirectRule, redirectPort int) []string {
	args := []string{"-t", "nat", action, "OUTPUT", "-p", "tcp", "-d", rule.Destination}
	if rule.Port > 0 {
		args = append(args, "--dport", strconv.Itoa(int(rule.Port)))
	}
	return append(args, "-j", "REDIRECT", "--to-ports", strconv.Itoa(redirectPort))
}

func originalDestination(conn net.Conn) (*OriginalDestination, error) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, fmt.Errorf("connection is not TCP")
	}
	raw, err := tcpConn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var dst *OriginalDestination
	var sockErr error
	controlErr := raw.Control(func(fd uintptr) {
		dst, sockErr = getsockoptOriginalDst(int(fd))
	})
	if controlErr != nil {
		return nil, controlErr
	}
	if sockErr != nil {
		return nil, sockErr
	}
	return dst, nil
}

func getsockoptOriginalDst(fd int) (*OriginalDestination, error) {
	var addr unix.RawSockaddrInet4
	size := uint32(unix.SizeofSockaddrInet4)
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), uintptr(unix.SOL_IP), uintptr(unix.SO_ORIGINAL_DST), uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return nil, errno
	}
	port := int(sockaddrPort(addr.Port))
	ip := net.IPv4(addr.Addr[0], addr.Addr[1], addr.Addr[2], addr.Addr[3])
	return &OriginalDestination{IP: ip, Port: port}, nil
}

func sockaddrPort(port uint16) uint16 {
	return binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&port))[:])
}

func runIptables(args ...string) error {
	cmd := exec.CommandContext(context.Background(), iptablesBinName, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func writeHostsBlock(path string, entries []HostEntry) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is fixed to /etc/hosts on Linux.
	if err != nil {
		return err
	}
	base := removeHostsBlock(string(data))
	var block strings.Builder
	block.WriteString(hostsBegin + "\n")
	for _, entry := range entries {
		if entry.IP == "" || entry.Host == "" {
			continue
		}
		names := append([]string{entry.Host}, entry.Also...)
		block.WriteString(entry.IP)
		block.WriteByte('\t')
		block.WriteString(strings.Join(names, " "))
		block.WriteByte('\n')
	}
	block.WriteString(hostsEnd + "\n")
	next := strings.TrimRight(base, "\n") + "\n\n" + block.String()
	return writeFileAtomic(path, []byte(next))
}

func clearHostsBlock(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is fixed to /etc/hosts on Linux.
	if err != nil {
		return err
	}
	next := strings.TrimRight(removeHostsBlock(string(data)), "\n") + "\n"
	return writeFileAtomic(path, []byte(next))
}

func writeFileAtomic(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
