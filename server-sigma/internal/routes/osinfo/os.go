package osinfo

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
)

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Get("/os/locale", func(w http.ResponseWriter, r *http.Request) {
		locale := os.Getenv("LANG")
		if locale == "" {
			locale = "en_US.UTF-8"
		}
		kb := os.Getenv("XKB_DEFAULT_LAYOUT")
		if kb == "" {
			kb = "us"
		}
		tz := os.Getenv("TZ")
		if tz == "" {
			tz = "UTC"
		}
		json.NewEncoder(w).Encode(map[string]any{"locale": locale, "keyboard_layout": kb, "timezone": tz})
	})

	r.Post("/os/locale", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Locale         string `json:"locale"`
			KeyboardLayout string `json:"keyboard_layout"`
			Timezone       string `json:"timezone"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Locale != "" {
			os.Setenv("LANG", b.Locale)
		}
		if b.Timezone != "" {
			os.Setenv("TZ", b.Timezone)
		}
		json.NewEncoder(w).Encode(map[string]any{"updated": true, "requires_restart": false})
	})

	return r
}