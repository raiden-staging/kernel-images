package scripts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"kernel-operator-api/internal/utils/env"
	"kernel-operator-api/internal/utils/ids"
)

type runItem struct {
	Cmd *exec.Cmd
}

var runs = map[string]*runItem{}

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/scripts/upload", upload)
	mux.HandleFunc("/scripts/run", run)
	mux.HandleFunc("/scripts/run/", runLogs) // placeholder
	mux.HandleFunc("/scripts/list", list)
	mux.HandleFunc("/scripts/delete", del)
}

func upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Bad Request"})
		return
	}
	dest := r.FormValue("path")
	file, _, err := r.FormFile("file")
	if err != nil || dest == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Bad Request"})
		return
	}
	defer file.Close()
	abs := dest
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(env.ScriptsDir, dest)
	}
	_ = os.MkdirAll(filepath.Dir(abs), 0o755)
	f, err := os.Create(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	defer f.Close()
	n, _ := io.Copy(f, file)
	if r.FormValue("executable") == "true" || r.FormValue("executable") == "" {
		_ = os.Chmod(abs, 0o755)
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": abs, "size_bytes": n})
}

func run(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path   string            `json:"path"`
		Args   []string          `json:"args"`
		Cwd    string            `json:"cwd"`
		Env    map[string]string `json:"env"`
		Mode   string            `json:"mode"`
		Stream bool              `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	cmd := exec.Command(body.Path, body.Args...)
	if body.Cwd != "" {
		cmd.Dir = body.Cwd
	} else {
		cmd.Dir = filepath.Dir(body.Path)
	}
	if len(body.Env) > 0 {
		for k, v := range body.Env {
			cmd.Env = append(os.Environ(), k+"="+v)
		}
	}
	if body.Mode == "async" {
		id := ids.New()
		runs[id] = &runItem{Cmd: cmd}
		_ = cmd.Start()
		writeJSON(w, http.StatusOK, map[string]any{"run_id": id})
		return
	}
	var out, err bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &err
	_ = cmd.Run()
	writeJSON(w, http.StatusOK, map[string]any{
		"exit_code":   cmd.ProcessState.ExitCode(),
		"stdout_b64":  base64.StdEncoding.EncodeToString(out.Bytes()),
		"stderr_b64":  base64.StdEncoding.EncodeToString(err.Bytes()),
		"duration_ms": 0,
	})
}

func runLogs(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func list(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
		UpdatedAt string `json:"updated_at"`
	}
	var out []item
	filepath.WalkDir(env.ScriptsDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			info, _ := d.Info()
			out = append(out, item{
				Path:      path,
				SizeBytes: info.Size(),
				UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func del(w http.ResponseWriter, r *http.Request) {
	var body struct{ Path string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" || !fileExists(body.Path) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	_ = os.Remove(body.Path)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}