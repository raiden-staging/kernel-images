package stream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"kernel-operator-api/internal/utils/ids"
	"kernel-operator-api/internal/utils/sse"
)

type streamItem struct {
	Cmd *exec.Cmd
}

var (
	mu      sync.Mutex
	streams = map[string]*streamItem{}
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/stream/start", start)
	mux.HandleFunc("/stream/stop", stop)
	mux.HandleFunc("/stream/", metrics) // /stream/{id}/metrics/stream
}

func start(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region           *struct{ X, Y, Width, Height int } `json:"region"`
		Display          int                                `json:"display"`
		Fps              int                                `json:"fps"`
		VideoCodec       string                             `json:"video_codec"`
		VideoBitrateKbps int                                `json:"video_bitrate_kbps"`
		Audio            *struct{ CaptureSystem bool `json:"capture_system"` } `json:"audio"`
		RTMPSURL         string                             `json:"rtmps_url"`
		StreamKey        string                             `json:"stream_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.RTMPSURL == "" || body.StreamKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Missing RTMPS params"})
		return
	}
	ff := firstNonEmpty(os.Getenv("FFMPEG_BIN"), "ffmpeg")
	display := firstNonEmpty(os.Getenv("DISPLAY"), ":0") + ".0"
	fps := "30"
	if body.Fps > 0 {
		fps = strconv.Itoa(body.Fps)
	}
	args := []string{"-hide_banner", "-thread_queue_size", "512", "-f", "x11grab", "-r", fps, "-i", display}
	if body.Audio != nil && body.Audio.CaptureSystem {
		pulse := firstNonEmpty(os.Getenv("PULSE_SOURCE"), "default")
		args = append(args, "-f", "pulse", "-i", pulse)
	}
	if body.Region != nil {
		args = append(args, "-filter:v", "crop="+cropExpr(body.Region))
	}
	vcodec := "libx264"
	switch strings.ToLower(body.VideoCodec) {
	case "h265":
		vcodec = "libx265"
	case "av1":
		vcodec = "libsvtav1"
	}
	br := "3500k"
	if body.VideoBitrateKbps > 0 {
		br = strconv.Itoa(body.VideoBitrateKbps) + "k"
	}
	args = append(args, "-c:v", vcodec, "-b:v", br, "-c:a", "aac", "-f", "flv", body.RTMPSURL+"/"+body.StreamKey)
	cmd := exec.Command(ff, args...)
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	id := ids.New()
	mu.Lock()
	streams[id] = &streamItem{Cmd: cmd}
	mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"stream_id": id, "status": "starting", "metrics_endpoint": "/stream/" + id + "/metrics/stream"})
}

func stop(w http.ResponseWriter, r *http.Request) {
	var body struct{ StreamID string `json:"stream_id"` }
	_ = json.NewDecoder(r.Body).Decode(&body)
	mu.Lock()
	item := streams[body.StreamID]
	mu.Unlock()
	if item == nil || item.Cmd == nil || item.Cmd.Process == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	_ = item.Cmd.Process.Signal(os.Interrupt)
	writeJSON(w, http.StatusOK, map[string]any{"stream_id": body.StreamID, "status": "stopped"})
}

func metrics(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/stream/"), "/")
	if len(parts) != 3 || parts[1] != "metrics" || parts[2] != "stream" {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	id := parts[0]
	mu.Lock()
	item := streams[id]
	mu.Unlock()
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	sse.Headers(w)
	flusher, _ := w.(http.Flusher)
	if flusher == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	stderr, _ := item.Cmd.StderrPipe()
	go item.Cmd.Wait()
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		obj := parseFFmpegLine(line)
		obj["ts"] = time.Now().UTC().Format(time.RFC3339)
		_ = sse.WriteJSON(w, obj)
	}
	_ = sse.WriteJSON(w, map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "ended": true})
}

func parseFFmpegLine(s string) map[string]any {
	out := map[string]any{}
	if i := strings.Index(s, "fps="); i >= 0 {
		var v float64
		fmt.Sscanf(s[i:], "fps=%f", &v)
		out["fps"] = v
	}
	if i := strings.Index(s, "bitrate="); i >= 0 {
		var v float64
		fmt.Sscanf(s[i:], "bitrate=%fkbits/s", &v)
		out["bitrate_kbps"] = v
	}
	if i := strings.Index(s, "drop="); i >= 0 {
		var v int
		fmt.Sscanf(s[i:], "drop=%d", &v)
		out["dropped_frames"] = v
	}
	return out
}

func cropExpr(r *struct{ X, Y, Width, Height int }) string {
	return fmt.Sprintf("%d:%d:%d:%d", r.Width, r.Height, r.X, r.Y)
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