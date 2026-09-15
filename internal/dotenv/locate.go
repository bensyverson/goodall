package dotenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvFileVar names the environment variable that overrides which .env the
// live tests and the fixture recorder read.
//
// It exists for the places where the repository root has no .env of its own —
// a git worktree, where the file is gitignored and so absent, or a CI checkout
// that mounts its secrets elsewhere — so the live tests can be run there
// without a key ever being copied into the tree. It holds a path, never a key.
const EnvFileVar = "GOODALL_ENV_FILE"

// RepoEnvFile is the path of the .env the live tests and the recorder read:
// whatever [EnvFileVar] names, or the .env beside the module's go.mod.
//
// The module root is found by walking up from the working directory rather
// than assumed to be one level up, so a test in any package directory reaches
// the same file. The path is returned whether or not a file is there: where it
// is absent, this is the path a skip message tells the reader to create. A
// working directory under no module at all is an error, since there is no
// sensible path to name.
//
// Nothing here reads the file, so nothing here can leak a key.
func RepoEnvFile() (string, error) {
	if override := os.Getenv(EnvFileVar); override != "" {
		return override, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, ".env"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s, so there is no repository .env to read; set %s to a path", start, EnvFileVar)
		}
		dir = parent
	}
}
