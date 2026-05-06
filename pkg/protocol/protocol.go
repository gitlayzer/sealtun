package protocol

import (
	"fmt"
	"strings"
)

const (
	HTTPS = "https"
)

func Normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ValidateExpose(value string) error {
	value = Normalize(value)
	switch value {
	case HTTPS:
		return nil
	case "":
		return fmt.Errorf("protocol is required")
	default:
		return fmt.Errorf("unsupported protocol %q: only https is currently supported", value)
	}
}

func ValidateServer(value string) error {
	value = Normalize(value)
	switch value {
	case HTTPS:
		return nil
	case "":
		return fmt.Errorf("protocol is required")
	default:
		return fmt.Errorf("unsupported server protocol %q: only https is currently supported", value)
	}
}
