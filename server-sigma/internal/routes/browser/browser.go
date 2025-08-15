package browser

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"kernel-operator-api/internal/utils"
)

type hub struct {
	mu sync.Mutex
	ch map[chan map[string]any]struct{}
}

func newHub() *hub {
	return &hub{ch: make(map[chan map[string]any]struct{})}
}
func (h *hub) sub() chan map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := make(chan map[string]any, 8)
	h.ch[c] = struct{}{}
	return c
}
func (h *hub) pub(v map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.ch {
		select { case c <- v: default: }
	}
}
func (h *hub) close(c chan map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ch, c)
	close(c)
}

var (
	sessionsMu sync.Mutex
	sessions   = map[string]*hub{} // id -> hub
)

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Post("/browser/har/start", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := utils.UID()
		sessionsMu.Lock()
		sessions[id] = newHub()
		sessionsMu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"har_session_id": id})
	})

	r.Get("/browser/har/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		sessionsMu.Lock()
		h := sessions[id]
		sessionsMu.Unlock()
		if h == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		sse := utils.NewSSE(w)
		c := h.sub()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				h.close(c)
				return
			case ev := <-c:
				_ = sse.Send(ev)
			}
		}
	})

	r.Post("/browser/har/stop", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			HarSessionID string `json:"har_session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sessionsMu.Lock()
		_, ok := sessions[body.HarSessionID]
		delete(sessions, body.HarSessionID)
		sessionsMu.Unlock()
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	return r
}