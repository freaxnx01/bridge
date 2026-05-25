package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSchemaMissingIsZero(t *testing.T) {
	dir := t.TempDir()
	v, err := ReadSchemaVersionFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("got %d", v)
	}
}

func TestWriteAndReadSchema(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSchemaVersionTo(dir, 3); err != nil {
		t.Fatal(err)
	}
	v, err := ReadSchemaVersionFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v != 3 {
		t.Errorf("got %d", v)
	}
}

func TestBackupBeforeMigrate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "presence.json")
	_ = os.WriteFile(src, []byte("old"), 0o644)
	if err := BackupForMigrate(src, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "presence.json.bak-2")); err != nil {
		t.Errorf("expected backup file: %v", err)
	}
}
