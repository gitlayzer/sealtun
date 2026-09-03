package cmd

import (
	"strings"
	"testing"
)

func TestLoadApplyDataRejectsLeadingZeroPorts(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
	}{
		{"localPort", "version: v1\ntunnels:\n  - name: web\n    localPort: 03000\n"},
		{"port alias", "version: v1\ntunnels:\n  - name: web\n    port: 03000\n"},
		{"short octal", "version: v1\ntunnels:\n  - name: web\n    localPort: 010\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadApplyData("test.yaml", []byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), "octal") {
				t.Fatalf("expected leading-zero port to be rejected with octal explanation, got %v", err)
			}
			if !strings.Contains(err.Error(), "3000") && !strings.Contains(err.Error(), "10") {
				t.Fatalf("error should suggest the intended port, got %v", err)
			}
		})
	}
}

func TestLoadApplyDataAcceptsNonOctalPortForms(t *testing.T) {
	for _, port := range []string{"3000", "0xBB8", "22", "0", `"03000"`} {
		yamlDoc := "version: v1\ntunnels:\n  - name: web\n    localPort: " + port + "\n"
		// "0" and quoted "03000" are rejected later by port validation, but must
		// not trip the octal literal check.
		_, err := loadApplyData("test.yaml", []byte(yamlDoc))
		if err != nil && strings.Contains(err.Error(), "octal") {
			t.Fatalf("port form %s should not be flagged as octal: %v", port, err)
		}
	}
}

func TestValidateApplyTunnelNamesRejectsDuplicateLinkNames(t *testing.T) {
	items := []applyTunnel{{
		Name: "web",
		AccessPolicy: &applyAccessPolicy{
			TemporaryLinks: []applyTemporaryLink{
				{Name: "review", Token: "token-one-x"},
				{Name: "review", Token: "token-two-y"},
			},
		},
	}}
	if err := validateApplyTunnelNames(items); err == nil || !strings.Contains(err.Error(), "duplicate temporary link name") {
		t.Fatalf("expected duplicate link name rejection, got %v", err)
	}
}
