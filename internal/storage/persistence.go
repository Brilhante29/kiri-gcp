// Package storage provides common persistence utilities: atomic JSON snapshots
// under $KIRI_DATA_DIR.
//
// Callers pass (service, key): the snapshot lands at
// $KIRI_DATA_DIR/{service}/{key}.json. When KIRI_DATA_DIR is unset the
// emulator is in-memory only and both Load and Save are no-ops, so services
// can call them unconditionally.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// dataDir returns the persistence root, or "" when persistence is disabled.
func dataDir() string {
	return os.Getenv("KIRI_DATA_DIR")
}

// Load reads $KIRI_DATA_DIR/{service}/{key}.json into v. It returns nil
// when persistence is disabled or the file does not exist (first run).
func Load(service, key string, v any) error {
	root := dataDir()
	if root == "" {
		return nil
	}

	path := filepath.Join(root, service, key+".json")

	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read snapshot %s: %w", path, err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal snapshot %s: %w", path, err)
	}

	return nil
}

// Save marshals v to indented JSON and writes it atomically (tmp file +
// rename) to $KIRI_DATA_DIR/{service}/{key}.json, creating directories as
// needed. It is a no-op when persistence is disabled.
func Save(service, key string, v any) error {
	root := dataDir()
	if root == "" {
		return nil
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot %s/%s: %w", service, key, err)
	}

	dir := filepath.Join(root, service)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create data dir %s: %w", dir, err)
	}

	path := filepath.Join(dir, key+".json")
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp snapshot %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename snapshot %s -> %s: %w", tmp, path, err)
	}

	return nil
}
