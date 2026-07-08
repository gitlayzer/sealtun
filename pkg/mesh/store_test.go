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
