package mesh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "mesh"))
	config := NewConfig("global", "gzg", "secret")
	if err := config.UpsertRegion(Region{Name: "gzg"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(config); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mesh config permissions = %o, want 600", info.Mode().Perm())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.HomeRegion != "gzg" || loaded.GatewayToken != "secret" {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}
}

func TestStoreSaveLoadPreservesExistingTimestamps(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "mesh"))
	config := Config{
		Version:      ConfigVersion,
		Name:         "global",
		HomeRegion:   "gzg",
		GatewayToken: "secret",
		UpdatedAt:    "2025-01-01T00:00:00Z",
		Regions: []Region{{
			Name:      "gzg",
			Profile:   "mesh-gzg",
			UpdatedAt: "2025-01-02T00:00:00Z",
		}},
		Services: []Service{{
			Name:      "api",
			Protocol:  ProtocolHTTP,
			From:      "gzg",
			Namespace: "default",
			Service:   "api",
			Port:      8080,
			CreatedAt: "2025-01-03T00:00:00Z",
			UpdatedAt: "2025-01-04T00:00:00Z",
		}},
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UpdatedAt != config.UpdatedAt || loaded.Regions[0].UpdatedAt != config.Regions[0].UpdatedAt ||
		loaded.Services[0].CreatedAt != config.Services[0].CreatedAt || loaded.Services[0].UpdatedAt != config.Services[0].UpdatedAt {
		t.Fatalf("save/load rewrote timestamps: %#v", loaded)
	}
}
