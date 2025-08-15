package recording

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"kernel-operator-api/internal/utils/env"
)

type recState struct {
	Cmd        *exec.Cmd
	File       string
	StartedAt  time.Time
	FinishedAt *time.Time
}

var (
	mu    sync.Mutex
	state = map[string]*recState{}
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/recording/start", start)
	mux.HandleFunc("/recording/stop", stop)
	mux.HandleFunc("/recording/download", download)
	mux.HandleFunc("/recording/list", list)
	mux.HandleFunc("/recording/delete", del)
}

func start(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID                   string `json:"id"`
		Framerate            int    `json:"framerate"`
		MaxDurationInSeconds int    `json:"maxDurationInSeconds"`
		MaxFileSizeInMB      int    `json:"maxFileSizeInMB"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	id := body.ID
	if id == "" {
		id = "default"
	}
	mu.Lock()
	if st, ok := state[id]; ok && st.Cmd != nil && st.Cmd.Process != nil {
		mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"message": "Already recording"})
		return
	}
	mu.Unlock()

	ff := firstNonEmpty(os.Getenv("FFMPEG_BIN"), "ffmpeg")
	display := firstNonEmpty(os.Getenv("DISPLAY"), ":0") + ".0"
	args := []string{"-nostdin", "-hide_banner", "-f", "x11grab", "-i", display}
	if body.Framerate > 0 {
		args = append(args, "-r", strconv.Itoa(body.Framerate))
	} else {
		args = append(args, "-r", "20")
	}
	if body.MaxDurationInSeconds > 0 {
		args = append(args, "-t", strconv.Itoa(body.MaxDurationInSeconds))
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-movflags", "+faststart")

	ofile := filepath.Join(env.RecordingsDir, id+"-"+strconv.FormatInt(time.Now().UnixMilli(), 10)+".mp4")
	args = append(args, ofile)

	cmd := exec.Command(ff, args...)
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	mu.Lock()
	state[id] = &recState{Cmd: cmd, File: ofile, StartedAt: time.Now()}
	mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": id, "started_at": time.Now().UTC().Format(time.RFC3339)})
}

func stop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		ForceStop bool   `json:"forceStop"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ID == "" {
		body.ID = "default"
	}
	mu.Lock()
	st := state[body.ID]
	mu.Unlock()
	if st == nil || st.Cmd == nil || st.Cmd.Process == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Not recording"})
		return
	}
	if body.ForceStop {
		_ = st.Cmd.Process.Kill()
	} else {
		_ = st.Cmd.Process.Signal(os.Interrupt)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func download(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		id = "default"
	}
	mu.Lock()
	st := state[id]
	mu.Unlock()
	if st != nil && st.Cmd != nil && st.Cmd.Process != nil {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if st == nil || st.File == "" || !fileExists(st.File) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not found"})
		return
	}
	info, _ := os.Stat(st.File)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Recording-Started-At", st.StartedAt.UTC().Format(time.RFC3339))
	w.Header().Set("X-Recording-Finished-At", info.ModTime().UTC().Format(time.RFC3339))
	http.ServeFile(w, r, st.File)
}

func list(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID          string  `json:"id"`
		IsRecording bool    `json:"isRecording"`
		StartedAt   *string `json:"started_at"`
		FinishedAt  *string `json:"finished_at"`
	}
	mu.Lock()
	defer mu.Unlock()
	var out []item
	for id, st := range state {
		var started, finished *string
		if !st.StartedAt.IsZero() {
			s := st.StartedAt.UTC().Format(time.RFC3339)
			started = &s
		}
		if st.FinishedAt != nil {
			s := st.FinishedAt.UTC().Format(time.RFC3339)
			finished = &s
		}
		out = append(out, item{
			ID:          id,
			IsRecording: st.Cmd != nil && st.Cmd.Process != nil,
			StartedAt:   started,
			FinishedAt:  finished,
		})
	}
	if len(out) == 0 {
		out = append(out, item{ID: "default", IsRecording: false})
	}
	writeJSON(w, http.StatusOK, out)
}

func del(w http.ResponseWriter, r *http.Request) {
	var body struct{ ID string }
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ID == "" {
		body.ID = "default"
	}
	mu.Lock()
	st := state[body.ID]
	mu.Unlock()
	if st == nil || st.File == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not found"})
		return
	}
	_ = os.Remove(st.File)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}