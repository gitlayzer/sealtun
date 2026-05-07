package cmd

import (
	"testing"

	"github.com/labring/sealtun/pkg/auth"
)

func TestVerificationURLFallsBackToVerificationURI(t *testing.T) {
	got, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURI: "https://auth.example.com/device",
	})
	if err != nil {
		t.Fatalf("verificationURL returned error: %v", err)
	}
	if got != "https://auth.example.com/device" {
		t.Fatalf("unexpected verification URL: %s", got)
	}
}

func TestVerificationURLSkipsUnsafeCompleteURL(t *testing.T) {
	got, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURIComplete: "file:///tmp/not-safe",
		VerificationURI:         "https://auth.example.com/device",
	})
	if err != nil {
		t.Fatalf("verificationURL returned error: %v", err)
	}
	if got != "https://auth.example.com/device" {
		t.Fatalf("unexpected verification URL: %s", got)
	}
}

func TestVerificationURLRejectsUnsafeURLs(t *testing.T) {
	if _, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURIComplete: "file:///tmp/not-safe",
		VerificationURI:         "javascript:alert(1)",
	}); err == nil {
		t.Fatal("expected unsafe verification URLs to be rejected")
	}
}
