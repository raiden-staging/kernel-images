package fs

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kernel-operator-api/internal/utils/sse"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/fs/read_file", readFile)
	mux.HandleFunc("/fs/write_file", writeFile)
	mux.HandleFunc("/fs/list_files", listFiles)
	mux.HandleFunc("/fs/create_directory", createDir)
	mux.HandleFunc("/fs/delete_file", deleteFile)
	mux.HandleFunc("/fs/delete_directory", deleteDir)
	mux.HandleFunc("/fs/set_file_permissions", setPerms)
	mux.HandleFunc("/fs/file_info", fileInfo)
	mux.HandleFunc("/fs/move", movePath)
	mux.HandleFunc("/fs/upload", upload)
	mux.HandleFunc("/fs/download", download)
	mux.HandleFunc("/fs/tail/stream", tailStream)
}

func readFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !strings.HasPrefix(p, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, f)
}

func writeFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := q.Get("path")
	mode := q.Get("mode")
	if p == "" || !strings.HasPrefix(p, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	if mode == "" {
		mode = "0644"
	}
	m, _ := strconv.ParseInt(mode, 8, 64)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	b, _ := io.ReadAll(r.Body)
	if err := os.WriteFile(p, b, os.FileMode(m)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func listFiles(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !strings.HasPrefix(p, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if err != nil || !st.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Bad path"})
		return
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	type item struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		SizeBytes int64  `json:"size_bytes"`
		IsDir     bool   `json:"is_dir"`
		ModTime   string `json:"mod_time"`
		Mode      string `json:"mode"`
	}
	out := make([]item, 0, len(ents))
	for _, e := range ents {
		fp := filepath.Join(p, e.Name())
		st, _ := os.Stat(fp)
		mode := st.Mode() & 0o7777
		out = append(out, item{
			Name:      e.Name(),
			Path:      fp,
			SizeBytes: sizeIfFile(st),
			IsDir:     st.IsDir(),
			ModTime:   st.ModTime().UTC().Format(time.RFC3339),
			Mode:      strconv.FormatInt(int64(mode), 8),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func createDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" || !strings.HasPrefix(body.Path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	mode := int64(0o755)
	if body.Mode != "" {
		mode, _ = strconv.ParseInt(body.Mode, 8, 64)
	}
	if err := os.MkdirAll(body.Path, os.FileMode(mode)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func deleteFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" || !strings.HasPrefix(body.Path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	st, err := os.Stat(body.Path)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if st.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Is directory"})
		return
	}
	_ = os.Remove(body.Path)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func deleteDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" || !strings.HasPrefix(body.Path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	_ = os.RemoveAll(body.Path)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func setPerms(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Path == "" || !strings.HasPrefix(body.Path, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	if _, err := os.Stat(body.Path); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if body.Mode != "" {
		mode, _ := strconv.ParseInt(body.Mode, 8, 64)
		_ = os.Chmod(body.Path, os.FileMode(mode))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func fileInfo(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !strings.HasPrefix(p, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid path"})
		return
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	mode := st.Mode() & 0o777
	prefix := "-"
	if st.IsDir() {
		prefix = "d"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":       filepath.Base(p),
		"path":       p,
		"size_bytes": sizeIfFile(st),
		"is_dir":     st.IsDir(),
		"mod_time":   st.ModTime().UTC().Format(time.RFC3339),
		"mode":       prefix + strconv.FormatInt(int64(mode), 8),
	})
}

func movePath(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Src string `json:"src_path"`
		Dst string `json:"dest_path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Src == "" || body.Dst == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Missing paths"})
		return
	}
	_ = os.MkdirAll(filepath.Dir(body.Dst), 0o755)
	if err := os.Rename(body.Src, body.Dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Bad Request"})
		return
	}
	pathDest := r.FormValue("path")
	file, _, err := r.FormFile("file")
	if err != nil || pathDest == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Missing fields"})
		return
	}
	defer file.Close()
	_ = os.MkdirAll(filepath.Dir(pathDest), 0o755)
	f, err := os.Create(pathDest)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	defer f.Close()
	n, _ := io.Copy(f, file)
	writeJSON(w, http.StatusOK, map[string]any{"path": pathDest, "size_bytes": n})
}

func download(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.Copy(w, f)
}

func tailStream(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	sse.Headers(w)
	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	cmd := exec.Command("tail", "-n", "0", "-F", p)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	go cmd.Wait()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		_ = sse.WriteJSON(w, map[string]any{
			"line": line,
			"ts":   time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func sizeIfFile(st os.FileInfo) int64 {
	if st.IsDir() {
		return 0
	}
	return st.Size()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}