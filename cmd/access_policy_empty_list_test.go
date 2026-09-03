package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestResolveAccessPolicyRejectsAllEmptyIPLists(t *testing.T) {
	_, err := resolveAccessPolicy(accessPolicyInput{IPAllowlist: []string{",", " "}}, time.Now(), func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "no valid entries") {
		t.Fatalf("expected all-empty allowlist to be rejected, got %v", err)
	}
	_, err = resolveAccessPolicy(accessPolicyInput{IPDenylist: []string{","}}, time.Now(), func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "no valid entries") {
		t.Fatalf("expected all-empty denylist to be rejected, got %v", err)
	}
}

func TestResolveAccessPolicyAllowsMixedEmptyEntries(t *testing.T) {
	policy, err := resolveAccessPolicy(accessPolicyInput{IPAllowlist: []string{"1.1.1.1,,2.2.2.2"}}, time.Now(), func(string) string { return "" })
	if err != nil {
		t.Fatalf("mixed valid and empty entries should be accepted, got %v", err)
	}
	if len(policy.IPAllowlist) != 2 {
		t.Fatalf("expected 2 normalized entries, got %v", policy.IPAllowlist)
	}
}

func TestResolveAccessPolicyAllowsNoIPFlags(t *testing.T) {
	if _, err := resolveAccessPolicy(accessPolicyInput{}, time.Now(), func(string) string { return "" }); err != nil {
		t.Fatalf("absent IP flags should not error, got %v", err)
	}
}
