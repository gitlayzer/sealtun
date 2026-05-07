package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/labring/sealtun/pkg/auth"
)

func TestCollectRegionListItems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authJSON, err := json.Marshal(auth.AuthData{Region: "https://hzh.sealos.run"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), authJSON, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	items, err := collectRegionListItems()
	if err != nil {
		t.Fatalf("collectRegionListItems: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected known regions")
	}

	var foundDefault, foundCurrent bool
	for _, item := range items {
		if item.Default && item.URL == auth.DefaultRegion {
			foundDefault = true
		}
		if item.Current && item.URL == "https://hzh.sealos.run" {
			foundCurrent = true
		}
		if item.Name == "hzh" && item.SealosDomain != "sealoshzh.site" {
			t.Fatalf("expected hzh sealos domain, got %s", item.SealosDomain)
		}
	}
	if !foundDefault {
		t.Fatal("expected default region to be marked")
	}
	if !foundCurrent {
		t.Fatal("expected current region to be marked")
	}
}

func TestCollectRegionListItemsFailsOnCorruptAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	if _, err := collectRegionListItems(); err == nil {
		t.Fatal("expected corrupt auth to fail")
	}
}

func TestRegionCurrentCommandPrintsRegionOnlyByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authJSON, err := json.Marshal(auth.AuthData{
		Region:       "https://hzh.sealos.run",
		SealosDomain: "sealoshzh.site",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), authJSON, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	var out bytes.Buffer
	regionCurrentCmd.SetOut(&out)
	t.Cleanup(func() { regionCurrentCmd.SetOut(nil) })

	if err := regionCurrentCmd.RunE(regionCurrentCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	text := out.String()
	if text != "https://hzh.sealos.run\n" {
		t.Fatalf("unexpected output: %q", text)
	}
}

func TestRegionCurrentCommandJSONIncludesSealosDomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authJSON, err := json.Marshal(auth.AuthData{
		Region:       "https://hzh.sealos.run",
		SealosDomain: "sealoshzh.site",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), authJSON, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	regionCurrentJSON = true
	defer func() { regionCurrentJSON = false }()

	var out bytes.Buffer
	regionCurrentCmd.SetOut(&out)
	t.Cleanup(func() { regionCurrentCmd.SetOut(nil) })

	if err := regionCurrentCmd.RunE(regionCurrentCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	text := out.String()
	if !contains(text, "\"region\": \"https://hzh.sealos.run\"") {
		t.Fatalf("missing region in output: %s", text)
	}
	if !contains(text, "\"sealosDomain\": \"sealoshzh.site\"") {
		t.Fatalf("missing sealos domain in output: %s", text)
	}
}

func TestRegionCurrentCommandFailsOnCorruptAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	if err := regionCurrentCmd.RunE(regionCurrentCmd, nil); err == nil {
		t.Fatal("expected corrupt auth to fail")
	}
}

func contains(text, part string) bool {
	return bytes.Contains([]byte(text), []byte(part))
}
