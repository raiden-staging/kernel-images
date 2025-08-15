package bus

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"kernel-operator-api/internal/utils"
)

type chanHub struct {
	mu sync.Mutex
	m  map[string]map[chan map[string]any]struct{}
}

func newHub() *chanHub { return &chanHub{m: map[string]map[chan map[string]any]struct{}{}} }

func (h *chanHub) sub(chName string) chan map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m[chName] == nil {
		h.m[chName] = map[chan map[string]any]struct{}{}
	}
	c := make(chan map[string]any, 8)
	h.m[chName][c] = struct{}{}
	return c
}
func (h *chanHub) pub(chName string, v map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.m[chName] {
		select { case c <- v: default: }
	}
}
func (h *chanHub) close(chName string, c chan map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m[chName] != nil {
		delete(h.m[chName], c)
		close(c)
	}
}

var hub = newHub()

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Post("/bus/publish", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Channel string         `json:"channel"`
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Channel == "" {
			body.Channel = "default"
		}
		hub.pub(body.Channel, map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339),
			"type":    body.Type,
			"payload": body.Payload,
		})
		json.NewEncoder(w).Encode(map[string]any{"delivered": true})
	})

	r.Get("/bus/subscribe", func(w http.ResponseWriter, r *http.Request) {
		ch := r.URL.Query().Get("channel")
		if ch == "" {
			http.Error(w, `{"message":"Missing channel"}`, http.StatusBadRequest)
			return
		}
		sse := utils.NewSSE(w)
		c := hub.sub(ch)
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				hub.close(ch, c)
				return
			case ev := <-c:
				_ = sse.Send(ev)
			}
		}
	})

	return r
}