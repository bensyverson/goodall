package goodall

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryPathFitsAModuleZip guards `go get`: the module proxy zips every
// file in the repository, testdata included, and refuses the whole module if
// one path holds a character Go reserves. A Maildir fixture named with the
// usual ":2,S" flags suffix once made the module unfetchable.
func TestEveryPathFitsAModuleZip(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "." && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if i := strings.IndexAny(d.Name(), "\"'*<>?`|:\\"); i >= 0 {
			t.Errorf("%s: %q can't be in a module path", path, d.Name()[i])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
