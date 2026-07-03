package protocol

import (
	"fmt"
	"strings"
)

const (
	HTTPS = "https"
	SSH   = "ssh"
	TCP   = "tcp"
)

const (
	ServerHTTPPort   int32 = 8080
	ServerRawTCPPort int32 = 2222
)

func Normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ValidateExpose(value string) error {
	value = Normalize(value)
	switch value {
	case HTTPS, SSH, TCP:
		return nil
	case "":
		return fmt.Errorf("protocol is required")
	default:
		return fmt.Errorf("unsupported protocol %q: supported protocols are https, ssh, and tcp", value)
	}
}

func ValidateServer(value string) error {
	value = Normalize(value)
	switch value {
	case HTTPS, SSH, TCP:
		return nil
	case "":
		return fmt.Errorf("protocol is required")
	default:
		return fmt.Errorf("unsupported server protocol %q: supported protocols are https, ssh, and tcp", value)
	}
}

func UsesRawTCP(value string) bool {
	switch Normalize(value) {
	case SSH, TCP:
		return true
	default:
		return false
	}
}

func IsHTTP(value string) bool {
	return Normalize(value) == HTTPS
}
