package network

import (
	"encoding/json"
	"net/http"
	"sync"

	"kernel-operator-api/internal/utils/ids"
	"kernel-operator-api/internal/utils/sse"
)

type ruleSet struct {
	Rules []any `json:"rules"`
}

var (
	mu       sync.Mutex
	ruleSets = map[string]ruleSet{}
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("/network/intercept/rules", handleRules)
	mux.HandleFunc("/network/intercept/rules/", handleRulesDelete) // /network/intercept/rules/{id}
	mux.HandleFunc("/network/har/stream", harStream)
}

func handleRules(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body ruleSet
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := ids.New()
		mu.Lock()
		ruleSets[id] = body
		mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"rule_set_id": id, "applied": true})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func handleRulesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := last(r.URL.Path, "/")
	mu.Lock()
	defer mu.Unlock()
	if _, ok := ruleSets[id]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	delete(ruleSets, id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func harStream(w http.ResponseWriter, r *http.Request) {
	sse.Headers(w)
	<-r.Context().Done()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func last(s, sep string) string {
	i := len(s) - 1
	for i >= 0 && s[i] == '/' {
		i--
	}
	j := i
	for j >= 0 && s[j] != '/' {
		j--
	}
	return s[j+1 : i+1]
}