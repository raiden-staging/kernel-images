package oslocale

import (
	"encoding/json"
	"net/http"
	"os"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/os/locale", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			locale := first(os.Getenv("LANG"), "en_US.UTF-8")
			kb := first(os.Getenv("XKB_DEFAULT_LAYOUT"), "us")
			tz := first(os.Getenv("TZ"), "UTC")
			writeJSON(w, http.StatusOK, map[string]any{"locale": locale, "keyboard_layout": kb, "timezone": tz})
		case http.MethodPost:
			var body struct {
				Locale         string `json:"locale"`
				KeyboardLayout string `json:"keyboard_layout"`
				Timezone       string `json:"timezone"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Locale != "" {
				os.Setenv("LANG", body.Locale)
			}
			if body.Timezone != "" {
				os.Setenv("TZ", body.Timezone)
			}
			writeJSON(w, http.StatusOK, map[string]any{"updated": true, "requires_restart": false})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

func first(v string, def string) string {
	if v != "" {
		return v
	}
	return def
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}