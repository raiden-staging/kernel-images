package intercept

import (
	"sync"
	"time"

	"kernel-operator-api/internal/utils"
)

type Match struct {
	Method      string `json:"method"`
	HostContains string `json:"host_contains"`
	PathRegex   string `json:"path_regex"`
	Protocol    string `json:"protocol"`
}
type Action struct {
	Type string `json:"type"`

	DelayMS int               `json:"delay_ms"`
	Status  int               `json:"status"`
	SetResponseHeaders map[string]string `json:"set_response_headers"`
	BodyB64 string           `json:"body_b64"`

	SetRequestHeaders map[string]string `json:"set_request_headers"`
}
type Rule struct {
	Match  Match  `json:"match"`
	Action Action `json:"action"`
}

type RuleSet struct {
	ID    string `json:"rule_set_id"`
	Rules []Rule `json:"rules"`
}

var (
	mu       sync.Mutex
	ruleSets = map[string]*RuleSet{}
	harMu    sync.Mutex
	harSubs  = map[chan map[string]any]struct{}{}
)

func ApplyRules(rules []Rule) *RuleSet {
	id := utils.UID()
	rs := &RuleSet{ID: id, Rules: rules}
	mu.Lock(); ruleSets[id] = rs; mu.Unlock()
	return rs
}

func DeleteRuleSet(id string) error {
	mu.Lock(); defer mu.Unlock()
	if _, ok := ruleSets[id]; !ok {
		return errNotFound
	}
	delete(ruleSets, id)
	return nil
}

var errNotFound = &nf{}
type nf struct{}
func (*nf) Error() string { return "Not Found" }

func HARStream() <-chan map[string]any {
	ch := make(chan map[string]any, 8)
	harMu.Lock(); harSubs[ch] = struct{}{}; harMu.Unlock()

	// send periodic empty events so consumers see data; real proxy not implemented here
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			EmitHAR(map[string]any{
				"ts": time.Now().UTC().Format(time.RFC3339),
				"entry": map[string]any{
					"request":  map[string]any{"method": "GET", "url": "http://example/heartbeat", "headers": []map[string]string{}},
					"response": map[string]any{"status": 200, "headers": []map[string]string{}},
				},
			})
		}
	}()

	return ch
}

func EmitHAR(obj map[string]any) {
	harMu.Lock()
	defer harMu.Unlock()
	for c := range harSubs {
		select { case c <- obj: default: }
	}
}