package macros

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"github.com/go-chi/chi/v5"

	"kernel-operator-api/internal/utils"
)

type Macro struct {
	MacroID string         `json:"macro_id"`
	Name    string         `json:"name"`
	Steps   []map[string]any `json:"steps"`
}

var (
	mu     sync.Mutex
	macros = map[string]*Macro{}
)

var (
	display = func() string { d := os.Getenv("DISPLAY"); if d == "" { d = ":0" }; return d }()
	xdotool = func() string { x := os.Getenv("XDOTOOL_BIN"); if x == "" { x = "/usr/bin/xdotool" }; return x }()
)

func runXdotool(args ...string) error {
	res := utils.Capture(xdotool, args, map[string]string{"DISPLAY": display}, "", 0)
	if res.Code != 0 {
		return &xdErr{stderr: string(res.Stderr)}
	}
	return nil
}
type xdErr struct{ stderr string }
func (e *xdErr) Error() string { return e.stderr }

func Router() *chi.Mux {
	r := chi.NewRouter()

	r.Post("/macros/create", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name  string           `json:"name"`
			Steps []map[string]any `json:"steps"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Name == "" || len(b.Steps) == 0 {
			http.Error(w, `{"message":"Bad Request"}`, 400); return
		}
		id := utils.UID()
		m := &Macro{MacroID: id, Name: b.Name, Steps: b.Steps}
		mu.Lock(); macros[id] = m; mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"macro_id": id})
	})

	r.Post("/macros/run", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ MacroID string `json:"macro_id"` }
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock(); m := macros[b.MacroID]; mu.Unlock()
		if m == nil {
			http.Error(w, `{"message":"Not Found"}`, 404); return
		}
		runID := utils.UID()
		go func() {
			for _, step := range m.Steps {
				switch step["action"] {
				case "keyboard.type":
					if t, ok := step["text"].(string); ok {
						_ = runXdotool("type", "--clearmodifiers", "--", t)
					}
				case "keyboard.key":
					if k, ok := step["key"].(string); ok {
						_ = runXdotool("key", k)
					}
				case "sleep":
					if ms, ok := step["ms"].(float64); ok {
						runXdotool("sleep", formatFloat(ms/1000.0))
					}
				}
			}
		}()
		json.NewEncoder(w).Encode(map[string]any{"started": true, "run_id": runID})
	})

	r.Get("/macros/list", func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			MacroID    string `json:"macro_id"`
			Name       string `json:"name"`
			StepsCount int    `json:"steps_count"`
		}
		mu.Lock()
		var items []item
		for _, m := range macros {
			items = append(items, item{MacroID: m.MacroID, Name: m.Name, StepsCount: len(m.Steps)})
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"items": items})
	})

	r.Delete("/macros/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		mu.Lock(); _, ok := macros[id]; delete(macros, id); mu.Unlock()
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, 404); return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	return r
}

func formatFloat(f float64) string {
	s := strconvIt(int(f * 1000))
	// already seconds with 3 decimals
	return s[:len(s)-3] + "." + s[len(s)-3:]
}
func strconvIt(i int) string { return itoa(i) }
func itoa(i int) string { return fmtInt(i) }

// tiny int -> string
func fmtInt(i int) string {
	if i == 0 { return "0" }
	sign := ""
	if i < 0 { sign = "-"; i = -i }
	d := []byte{}
	for i > 0 {
		d = append([]byte{byte('0'+i%10)}, d...)
		i/=10
	}
	return sign+string(d)
}