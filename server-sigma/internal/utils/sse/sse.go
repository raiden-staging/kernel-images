package sse

import (
	"bufio"
	"encoding/json"
	"net/http"
	"time"
)

func Headers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-SSE-Content-Type", "application/json")
}

func WriteJSON(w http.ResponseWriter, v any) error {
	bw := bufio.NewWriter(w)
	wf, _ := w.(http.Flusher)
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := bw.WriteString("event: data\n"); err != nil {
		return err
	}
	if _, err := bw.WriteString("data: "); err != nil {
		return err
	}
	if _, err := bw.Write(b); err != nil {
		return err
	}
	if _, err := bw.WriteString("\n\n"); err != nil {
		return err
	}
	_ = bw.Flush()
	if wf != nil {
		wf.Flush()
	}
	return nil
}

func Heartbeat(w http.ResponseWriter) {
	_ = WriteJSON(w, map[string]any{"ts": time.Now().Format(time.RFC3339), "event": "heartbeat"})
}