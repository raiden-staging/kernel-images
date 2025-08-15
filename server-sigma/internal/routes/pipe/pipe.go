package pipe

import (
	"github.com/go-chi/chi/v5"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"kernel-operator-api/internal/utils"
)

type emitter struct {
	mu sync.Mutex
	subs map[chan map[string]any]struct{}
}
func newEmitter() *emitter { return &emitter{subs: map[chan map[string]any]struct{}{}} }
func (e *emitter) sub() chan map[string]any { e.mu.Lock(); defer e.mu.Unlock(); c:=make(chan map[string]any,8); e.subs[c]=struct{}{}; return c }
func (e *emitter) pub(v map[string]any) { e.mu.Lock(); defer e.mu.Unlock(); for c:= range e.subs { select { case c<-v: default: } } }

var (
	mu sync.Mutex
	channels = map[string]*emitter{}
)
func get(name string) *emitter {
	mu.Lock(); defer mu.Unlock()
	if channels[name]==nil { channels[name]=newEmitter() }
	return channels[name]
}

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Post("/pipe/send", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Channel string `json:"channel"`
			Object  map[string]any `json:"object"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Channel == "" { b.Channel = "default" }
		get(b.Channel).pub(map[string]any{"ts": time.Now().UTC().Format(time.RFC3339), "object": b.Object})
		json.NewEncoder(w).Encode(map[string]any{"enqueued": true})
	})

	r.Get("/pipe/recv/stream", func(w http.ResponseWriter, r *http.Request) {
		chName := r.URL.Query().Get("channel")
		if chName == "" { chName = "default" }
		c := get(chName).sub()
		sse := utils.NewSSE(w)
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-c:
				_ = sse.Send(ev)
			}
		}
	})

	return r
}