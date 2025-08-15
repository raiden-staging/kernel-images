package metrics

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"kernel-operator-api/internal/utils/sse"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/metrics/snapshot", snapshot)
	mux.HandleFunc("/metrics/stream", stream)
}

func snapshot(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	writeJSON(w, http.StatusOK, map[string]any{
		"cpu_pct": 0,
		"gpu_pct": 0,
		"mem": map[string]any{
			"used_bytes": m.Alloc,
			"total_bytes": m.Sys,
		},
		"disk": map[string]any{"read_bps": 0, "write_bps": 0},
		"net":  map[string]any{"rx_bps": 0, "tx_bps": 0},
	})
}

func stream(w http.ResponseWriter, r *http.Request) {
	sse.Headers(w)
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			_ = sse.WriteJSON(w, map[string]any{
				"ts": time.Now().UTC().Format(time.RFC3339),
				"cpu_pct": 0,
				"gpu_pct": 0,
				"mem": map[string]any{
					"used_bytes": m.Alloc,
					"total_bytes": m.Sys,
				},
				"disk": map[string]any{"read_bps": 0, "write_bps": 0},
				"net":  map[string]any{"rx_bps": 0, "tx_bps": 0},
			})
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}