package store

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// CurrentSchema is the schema version this binary writes.
const CurrentSchema = 1

// ReadSchemaVersion reads the schema-version file from the default cache dir.
func ReadSchemaVersion() (int, error) {
	d, err := Dir()
	if err != nil {
		return 0, err
	}
	return ReadSchemaVersionFrom(d)
}

// ReadSchemaVersionFrom reads schema-version from dir. Missing == 0.
func ReadSchemaVersionFrom(dir string) (int, error) {
	b, err := ReadFile(filepath.Join(dir, "schema-version"))
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// WriteSchemaVersion writes v to the default cache dir.
func WriteSchemaVersion(v int) error {
	d, err := Dir()
	if err != nil {
		return err
	}
	return WriteSchemaVersionTo(d, v)
}

// WriteSchemaVersionTo writes v to dir.
func WriteSchemaVersionTo(dir string, v int) error {
	return AtomicWrite(filepath.Join(dir, "schema-version"), []byte(strconv.Itoa(v)))
}

// BackupForMigrate copies src to src.bak-<fromVersion> before in-place migration.
func BackupForMigrate(src string, fromVersion int) error {
	b, err := ReadFile(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return AtomicWrite(fmt.Sprintf("%s.bak-%d", src, fromVersion), b)
}
