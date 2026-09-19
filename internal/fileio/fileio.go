// Package fileio holds the file conventions the state and config files share:
// a format chosen by extension (.json is JSON, anything else is YAML) and an
// atomic write through a temp file and rename.
package fileio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// IsJSON reports whether path selects the JSON format.
func IsJSON(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".json")
}

// Marshal encodes v in the format path selects.
func Marshal(path string, v any) ([]byte, error) {
	if IsJSON(path) {
		return json.MarshalIndent(v, "", "  ")
	}
	return yaml.Marshal(v)
}

// Unmarshal decodes data into v in the format path selects.
func Unmarshal(path string, data []byte, v any) error {
	if IsJSON(path) {
		return json.Unmarshal(data, v)
	}
	return yaml.Unmarshal(data, v)
}

// WriteAtomic writes data to path through a sibling temp file and a rename,
// so a reader never sees a partially written file.
func WriteAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
