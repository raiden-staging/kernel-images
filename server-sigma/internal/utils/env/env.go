package env

import (
	"os"
	"path/filepath"
	"sort"
)

var (
	DataDir       = firstNonEmpty(os.Getenv("DATA_DIR"), "/tmp/kernel-operator-api/data")
	TmpDir        = filepath.Join(DataDir, "tmp")
	ScriptsDir    = filepath.Join(DataDir, "scripts")
	RecordingsDir = filepath.Join(DataDir, "recordings")
	ScreensDir    = filepath.Join(DataDir, "screenshots")
)

func EnsureDirs() {
	for _, p := range []string{DataDir, TmpDir, ScriptsDir, RecordingsDir, ScreensDir} {
		_ = os.MkdirAll(p, 0o755)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func SortedEnvKeys() []string {
	keys := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		k := kv
		if i := indexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexByte(s string, c byte) int {
	for i := range s {
		if s[i] == c {
			return i
		}
	}
	return -1
}