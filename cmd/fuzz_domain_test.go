package cmd

import (
	"strings"
	"testing"
)

func FuzzValidateCustomDomain(f *testing.F) {
	seeds := []string{
		"app.example.com", "a.b.c.d.example.com", "foo.sealosgzg.site",
		"1.2.3.4", "localhost", "example", "-bad.example.com",
		"bad-.example.com", "app_example.com", "https://app.example.com",
		"app.example.com/", ".app.example.com", "*.example.com",
		"app.example.com.", "", "  ", "app..example.com",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got, err := validateCustomDomain(input)
		if err != nil {
			return
		}
		// Invariants for accepted values:
		if got != strings.TrimSpace(got) {
			t.Fatalf("accepted domain with surrounding whitespace: %q", got)
		}
		if strings.ContainsAny(got, " \t\n\r\x00/:@") {
			t.Fatalf("accepted domain containing forbidden characters: %q (from %q)", got, input)
		}
		if len(got) > 253 {
			t.Fatalf("accepted over-length domain: %d chars", len(got))
		}
		for _, label := range strings.Split(got, ".") {
			if label == "" && got != "" {
				t.Fatalf("accepted domain with empty label: %q", got)
			}
		}
	})
}

func FuzzValidateApplyPortLiterals(f *testing.F) {
	f.Add("version: v1\ntunnels:\n  - name: web\n    localPort: 03000\n")
	f.Add("version: v1\ntunnels:\n  - name: web\n    localPort: 3000\n")
	f.Fuzz(func(t *testing.T, input string) {
		// Must never panic regardless of input
		_ = validateApplyPortLiterals("fuzz.yaml", []byte(input))
	})
}
