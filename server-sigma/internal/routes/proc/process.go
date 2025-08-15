package proc

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	execx "kernel-operator-api/internal/utils/exec"
	"kernel-operator-api/internal/utils/ids"
	"kernel-operator-api/internal/utils/sse"
)

type procItem struct {
	Cmd       *exec.Cmd
	StartedAt time.Time
	Stdout    ioReadCloser
	Stderr    ioReadCloser
}

type ioReadCloser interface {
	Read(p []byte) (n int, err error)
	Close() error
}

var (
	mu    sync.Mutex
	procs = map[string]*procItem{}
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/process/exec", handleExec)
	mux.HandleFunc("/process/spawn", handleSpawn)
	mux.HandleFunc("/process/", handleProcessSub) // /process/{id}/...
}

func handleExec(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Cwd     string            `json:"cwd"`
		Env     map[string]string `json:"env"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Missing command"})
		return
	}
	var envv []string
	for k, v := range body.Env {
		envv = append(envv, k+"="+v)
	}
	res, err := execx.ExecCapture(r.Context(), body.Command, body.Args, envv, body.Cwd)
	if err != nil && res == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exit_code":   res.Code,
		"stdout_b64":  base64.StdEncoding.EncodeToString(res.Stdout),
		"stderr_b64":  base64.StdEncoding.EncodeToString(res.Stderr),
		"duration_ms": res.Duration.Milliseconds(),
	})
}

func handleSpawn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Cwd     string            `json:"cwd"`
		Env     map[string]string `json:"env"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Missing command"})
		return
	}
	cmd := exec.Command(body.Command, body.Args...)
	if body.Cwd != "" {
		cmd.Dir = body.Cwd
	}
	if len(body.Env) > 0 {
		cmd.Env = append(os.Environ(), toEnv(body.Env)...)
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	id := ids.New()
	mu.Lock()
	procs[id] = &procItem{Cmd: cmd, StartedAt: time.Now(), Stdout: stdout, Stderr: stderr}
	mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"process_id": id,
		"pid":        cmd.Process.Pid,
		"started_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func handleProcessSub(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/process/"), "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	id := parts[0]
	action := strings.Join(parts[1:], "/")
	mu.Lock()
	item := procs[id]
	mu.Unlock()
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	switch action {
	case "status":
		state := "running"
		var exitCode *int
		if item.Cmd.ProcessState != nil && item.Cmd.ProcessState.Exited() {
			state = "exited"
			code := item.Cmd.ProcessState.ExitCode()
			exitCode = &code
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state, "exit_code": exitCode, "cpu_pct": 0, "mem_bytes": 0})
	case "stdout/stream":
		sse.Headers(w)
		flusher, _ := w.(http.Flusher)
		if flusher == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		go func() { _ = item.Cmd.Wait() }()
		buf := make([]byte, 4096)
		for {
			select {
			case <-r.Context().Done():
				return
			default:
				if n, _ := item.Stdout.Read(buf); n > 0 {
					_ = sse.WriteJSON(w, map[string]any{"stream": "stdout", "data_b64": base64.StdEncoding.EncodeToString(buf[:n])})
				}
				if n2, _ := item.Stderr.Read(buf); n2 > 0 {
					_ = sse.WriteJSON(w, map[string]any{"stream": "stderr", "data_b64": base64.StdEncoding.EncodeToString(buf[:n2])})
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	case "kill":
		var body struct{ Signal string `json:"signal"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		sig := syscall.SIGTERM
		switch strings.ToUpper(body.Signal) {
		case "KILL":
			sig = syscall.SIGKILL
		case "INT":
			sig = syscall.SIGINT
		case "HUP":
			sig = syscall.SIGHUP
		}
		_ = item.Cmd.Process.Signal(sig)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "stdin":
		var body struct{ DataB64 string `json:"data_b64"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, http.StatusOK, map[string]any{"written_bytes": 0})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func toEnv(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}