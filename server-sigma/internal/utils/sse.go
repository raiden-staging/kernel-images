package utils

import (
	"bufio"
	"encoding/json"
	"net/http"
	"time"
)

type SSE struct {
	w   http.ResponseWriter
	fl  http.Flusher
	enc *bufio.Writer
}

func NewSSE(w http.ResponseWriter) *SSE {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-SSE-Content-Type", "application/json")
	fl, _ := w.(http.Flusher)
	return &SSE{w: w, fl: fl, enc: bufio.NewWriter(w)}
}

func (s *SSE) Send(obj any) error {
	b, _ := json.Marshal(obj)
	if _, err := s.enc.WriteString("event: data\n"); err != nil {
		return err
	}
	if _, err := s.enc.WriteString("data: "); err != nil {
		return err
	}
	if _, err := s.enc.Write(b); err != nil {
		return err
	}
	if _, err := s.enc.WriteString("\n\n"); err != nil {
		return err
	}
	_ = s.enc.Flush()
	if s.fl != nil {
		s.fl.Flush()
	}
	return nil
}

func (s *SSE) Heartbeat() { _ = s.Send(map[string]any{"ts": time.Now().UTC().Format(time.RFC3339)}) }