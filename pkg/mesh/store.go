package mesh

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/labring/sealtun/pkg/auth"
)

const (
	storeDirName  = "mesh"
	storeFileName = "mesh.json"
)

type Store struct {
	root string
}

func DefaultStore() (*Store, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, storeDirName)
	if _, err := auth.EnsurePrivateDir(dir, "mesh directory"); err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Path() string {
	return filepath.Join(s.root, storeFileName)
}

func (s *Store) Load() (*Config, error) {
	data, err := readRegularFile(s.Path(), "mesh config")
	if err != nil {
		return nil, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if err := config.Normalize(); err != nil {
		return nil, err
	}
	return &config, nil
}

func (s *Store) Save(config Config) error {
	if _, err := auth.EnsurePrivateDir(s.root, "mesh directory"); err != nil {
		return err
	}
	if err := config.Normalize(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(s.Path(), data, 0o600)
}

func readRegularFile(path, label string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", label, path)
	}
	return os.ReadFile(path) // #nosec G304 -- path is under the user-owned Sealtun mesh directory.
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(path), os.Getpid(), time.Now().UnixNano()))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G304 -- temp file is created next to a fixed Sealtun mesh config.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
