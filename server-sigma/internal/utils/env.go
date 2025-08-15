package utils

import (
	"os"
	"path/filepath"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	DataDir       = getenv("DATA_DIR", "/tmp/kernel-operator-api/data")
	TmpDir        = filepath.Join(DataDir, "tmp")
	ScriptsDir    = filepath.Join(DataDir, "scripts")
	RecordingsDir = filepath.Join(DataDir, "recordings")
	ScreensDir    = filepath.Join(DataDir, "screenshots")
)

func EnsureDirs() error {
	for _, p := range []string{DataDir, TmpDir, ScriptsDir, RecordingsDir, ScreensDir} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func init() { _ = EnsureDirs() }