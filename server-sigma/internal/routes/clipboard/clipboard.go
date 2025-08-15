package clipboard

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"kernel-operator-api/internal/utils/sse"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/clipboard", handleClipboard)
	mux.HandleFunc("/clipboard/stream", handleStream)
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func xGet() (map[string]any, error) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":20"
	}
	cmd := exec.Command("bash", "-lc", "DISPLAY="+display+" xclip -selection clipboard -o 2>/dev/null || true")
	out, _ := cmd.CombinedOutput()
	return map[string]any{"type": "text", "text": string(out)}, nil
}

func wlGet() (map[string]any, error) {
	// text
	cmd := exec.Command("bash", "-lc", "wl-paste -t text 2>/dev/null || true")
	if have("wl-paste") {
		out, _ := cmd.CombinedOutput()
		if len(out) > 0 {
			return map[string]any{"type": "text", "text": string(out)}, nil
		}
		// image
		cmd = exec.Command("bash", "-lc", "wl-paste -t image/png | base64 -w0 2>/dev/null || true")
		out, _ = cmd.CombinedOutput()
		if len(out) > 0 {
			return map[string]any{"type": "image", "image_b64": string(out), "image_mime": "image/png"}, nil
		}
	}
	return map[string]any{"type": "text", "text": ""}, nil
}

func handleClipboard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var res map[string]any
		var err error
		if have("xclip") {
			res, err = xGet()
		} else {
			res, err = wlGet()
		}
		if err != nil {
			res = map[string]any{"type": "text", "text": ""}
		}
		writeJSON(w, http.StatusOK, res)
	case http.MethodPost:
		var body struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageB64 string `json:"image_b64"`
			ImageMIM string `json:"image_mime"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if strings.ToLower(body.Type) == "image" && body.ImageB64 != "" && have("wl-copy") {
			cmd := exec.Command("bash", "-lc", "base64 -d | wl-copy -t "+shellQuote(body.ImageMIM))
			dec, _ := base64.StdEncoding.DecodeString(body.ImageB64)
			stdin, _ := cmd.StdinPipe()
			_ = cmd.Start()
			_, _ = stdin.Write(dec)
			_ = stdin.Close()
			_ = cmd.Wait()
		} else {
			if have("xclip") {
				display := os.Getenv("DISPLAY")
				if display == "" {
					display = ":20"
				}
				_ = exec.Command("bash", "-lc", "printf %s "+shellQuote(body.Text)+" | DISPLAY="+display+" xclip -selection clipboard").Run()
			} else if have("wl-copy") {
				_ = exec.Command("bash", "-lc", "printf %s "+shellQuote(body.Text)+" | wl-copy").Run()
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleStream(w http.ResponseWriter, r *http.Request) {
	sse.Headers(w)
	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	last := ""
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			cur := ""
			if have("xclip") {
				m, _ := xGet()
				cur, _ = m["text"].(string)
			} else {
				m, _ := wlGet()
				if m["type"] == "text" {
					cur, _ = m["text"].(string)
				} else {
					if s, _ := m["image_b64"].(string); len(s) > 16 {
						cur = s[:16]
					}
				}
			}
			if cur != last {
				last = cur
				_ = sse.WriteJSON(w, map[string]any{
					"ts":     time.Now().UTC().Format(time.RFC3339),
					"type":   "text",
					"preview": preview(cur, 100),
				})
			}
		}
	}
}

func preview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}