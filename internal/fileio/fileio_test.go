package fileio

import (
	"os"
	"path/filepath"
	"testing"
)

type doc struct {
	Name string `yaml:"name" json:"name"`
}

func TestRoundTripByExtension(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"x.yaml", "x.json", "x.JSON"} {
		path := filepath.Join(dir, name)
		data, err := Marshal(path, doc{Name: "a"})
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if err := WriteAtomic(path, data); err != nil {
			t.Fatalf("%s: WriteAtomic: %v", name, err)
		}
		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("%s: temp file left behind", name)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var d doc
		if err := Unmarshal(path, got, &d); err != nil {
			t.Fatalf("%s: Unmarshal: %v", name, err)
		}
		if d.Name != "a" {
			t.Errorf("%s: round trip = %+v", name, d)
		}
		if IsJSON(path) != (name != "x.yaml") {
			t.Errorf("%s: IsJSON = %v", name, IsJSON(path))
		}
	}
}
