package input

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/input/mouse/move", mouseMove)
	mux.HandleFunc("/input/mouse/click", mouseClick)
}

func runXdotool(args ...string) error {
	xd := firstNonEmpty(os.Getenv("XDOTOOL_BIN"), "xdotool")
	display := firstNonEmpty(os.Getenv("DISPLAY"), ":0")
	cmd := exec.Command(xd, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	return cmd.Run()
}

func mouseMove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.X == 0 && body.Y == 0 && r.ContentLength == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Invalid coords"})
		return
	}
	if err := runXdotool("mousemove", strconv.Itoa(body.X), strconv.Itoa(body.Y)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func mouseClick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Button string `json:"button"`
		Count  int    `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Count == 0 {
		body.Count = 1
	}
	btn := buttonNum(body.Button)
	if err := runXdotool("click", "--repeat", strconv.Itoa(body.Count), btn); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func buttonNum(name string) string {
	name = strings.ToLower(name)
	switch name {
	case "left":
		return "1"
	case "middle":
		return "2"
	case "right":
		return "3"
	case "back":
		return "8"
	case "forward":
		return "9"
	default:
		if name == "" {
			return "1"
		}
		return name
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}