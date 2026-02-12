package webmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Bridge manages a CDP connection to Chrome and interacts with the WebMCP
// APIs (navigator.modelContext / navigator.modelContextTesting) on the active tab.
type Bridge struct {
	logger       *slog.Logger
	upstreamURL  string // Chrome CDP browser-level WS URL
	eventCh      chan ServerMessage
	stopCh       chan struct{}
	done         chan struct{}

	mu           sync.Mutex
	cdpConn      *websocket.Conn
	cdpMsgID     atomic.Int64
	pendingCalls map[int64]chan json.RawMessage
	sessionID    string // CDP session for attached page target
	activeTarget TabInfo
	lastTools    []Tool
	subscribed   bool
}

// NewBridge creates a new WebMCP CDP bridge.
func NewBridge(upstreamURL string, logger *slog.Logger) *Bridge {
	return &Bridge{
		logger:       logger,
		upstreamURL:  upstreamURL,
		eventCh:      make(chan ServerMessage, 64),
		stopCh:       make(chan struct{}),
		done:         make(chan struct{}),
		pendingCalls: make(map[int64]chan json.RawMessage),
	}
}

// Events returns the channel of server events to forward to the WS client.
func (b *Bridge) Events() <-chan ServerMessage {
	return b.eventCh
}

// Start connects to Chrome CDP and begins monitoring.
func (b *Bridge) Start(ctx context.Context) error {
	// Parse upstream to get the Chrome host for HTTP API
	parsed, err := url.Parse(b.upstreamURL)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}

	// Connect to browser-level CDP WS
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, b.upstreamURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Host": []string{parsed.Host}},
	})
	if err != nil {
		return fmt.Errorf("failed to connect to CDP: %w", err)
	}
	conn.SetReadLimit(100 * 1024 * 1024)
	b.cdpConn = conn

	// Start CDP message reader
	go b.readCDPMessages(ctx)

	// Enable target discovery
	if err := b.enableTargetDiscovery(ctx); err != nil {
		b.Close()
		return fmt.Errorf("failed to enable target discovery: %w", err)
	}

	// Synchronously attach to the active page target so that callers
	// (e.g. the REST status endpoint) can immediately query tools.
	if err := b.findAndAttachPageTarget(ctx); err != nil {
		b.logger.Warn("initial page attachment failed", "err", err)
		// Non-fatal: the monitor loop will retry.
	}

	// Start the monitoring loop
	go b.monitorLoop(ctx)

	return nil
}

// Close shuts down the bridge.
func (b *Bridge) Close() {
	select {
	case <-b.stopCh:
		return // already stopped
	default:
	}
	close(b.stopCh)
	b.mu.Lock()
	if b.cdpConn != nil {
		_ = b.cdpConn.Close(websocket.StatusNormalClosure, "bridge closing")
		b.cdpConn = nil
	}
	b.mu.Unlock()
}

// Subscribe starts active monitoring and emits events.
func (b *Bridge) Subscribe() {
	b.mu.Lock()
	b.subscribed = true
	b.mu.Unlock()
}

// Unsubscribe stops active monitoring and drains any pending events.
func (b *Bridge) Unsubscribe() {
	b.mu.Lock()
	b.subscribed = false
	b.mu.Unlock()
	// Drain any buffered events so they are not forwarded after unsubscribe.
	for {
		select {
		case <-b.eventCh:
		default:
			return
		}
	}
}

// ListTools queries the current page for WebMCP tools.
func (b *Bridge) ListTools(ctx context.Context) ([]Tool, error) {
	b.mu.Lock()
	session := b.sessionID
	b.mu.Unlock()

	if session == "" {
		return nil, fmt.Errorf("no page target attached")
	}

	return b.queryTools(ctx, session)
}

// CallTool executes a WebMCP tool on the current page.
func (b *Bridge) CallTool(ctx context.Context, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	b.mu.Lock()
	session := b.sessionID
	b.mu.Unlock()

	if session == "" {
		return nil, fmt.Errorf("no page target attached")
	}

	argsStr := "{}"
	if len(arguments) > 0 {
		argsStr = string(arguments)
	}

	js := fmt.Sprintf(`(async () => {
		const mc = navigator.modelContextTesting;
		if (!mc) throw new Error("WebMCP testing API not available");
		const result = await mc.executeTool(%q, %q);
		return JSON.stringify(result);
	})()`, toolName, argsStr)

	raw, err := b.evaluateInSession(ctx, session, js, true)
	if err != nil {
		return nil, err
	}

	return raw, nil
}

// --- CDP protocol helpers ---

type cdpMessage struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (b *Bridge) sendCDP(ctx context.Context, method string, params interface{}, sessionID string) (json.RawMessage, error) {
	id := b.cdpMsgID.Add(1)

	var paramsRaw json.RawMessage
	if params != nil {
		var err error
		paramsRaw, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
	}

	msg := cdpMessage{
		ID:        id,
		Method:    method,
		Params:    paramsRaw,
		SessionID: sessionID,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal CDP message: %w", err)
	}

	// Register pending call
	resultCh := make(chan json.RawMessage, 1)
	b.mu.Lock()
	b.pendingCalls[id] = resultCh
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pendingCalls, id)
		b.mu.Unlock()
	}()

	b.mu.Lock()
	conn := b.cdpConn
	b.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("CDP connection closed")
	}

	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		return nil, fmt.Errorf("write CDP: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("CDP call timed out: %s", method)
	case <-b.stopCh:
		return nil, fmt.Errorf("bridge stopped")
	}
}

func (b *Bridge) readCDPMessages(ctx context.Context) {
	for {
		select {
		case <-b.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		b.mu.Lock()
		conn := b.cdpConn
		b.mu.Unlock()
		if conn == nil {
			return
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			select {
			case <-b.stopCh:
			case <-ctx.Done():
			default:
				b.logger.Error("CDP read error", "err", err)
			}
			return
		}

		var msg cdpMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			b.logger.Error("CDP unmarshal error", "err", err)
			continue
		}

		// Handle response to a pending call
		if msg.ID > 0 {
			b.mu.Lock()
			ch, ok := b.pendingCalls[msg.ID]
			b.mu.Unlock()
			if ok {
				if msg.Error != nil {
					// Encode error as a JSON string for the response channel
					errJSON, _ := json.Marshal(map[string]string{"error": msg.Error.Message})
					ch <- errJSON
				} else {
					ch <- msg.Result
				}
			}
			continue
		}

		// Handle CDP events
		b.handleCDPEvent(ctx, msg)
	}
}

func (b *Bridge) handleCDPEvent(ctx context.Context, msg cdpMessage) {
	switch msg.Method {
	case "Target.targetInfoChanged":
		var params struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
				Title    string `json:"title"`
				Attached bool   `json:"attached"`
			} `json:"targetInfo"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		if params.TargetInfo.Type != "page" {
			return
		}
		b.mu.Lock()
		subscribed := b.subscribed
		prev := b.activeTarget
		b.mu.Unlock()

		if subscribed && params.TargetInfo.URL != prev.URL {
			b.mu.Lock()
			b.activeTarget = TabInfo{
				TargetID: params.TargetInfo.TargetID,
				URL:      params.TargetInfo.URL,
				Title:    params.TargetInfo.Title,
			}
			b.mu.Unlock()

			b.emit(NewTabChangedMsg(params.TargetInfo.URL, params.TargetInfo.Title))

			// Re-check WebMCP availability and tools on the new page
			go b.checkAndEmitTools(ctx)
		}

	case "Target.targetCreated":
		var params struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
				Title    string `json:"title"`
			} `json:"targetInfo"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return
		}
		if params.TargetInfo.Type == "page" {
			b.logger.Info("new page target", "id", params.TargetInfo.TargetID, "url", params.TargetInfo.URL)
		}
	}
}

func (b *Bridge) enableTargetDiscovery(ctx context.Context) error {
	_, err := b.sendCDP(ctx, "Target.setDiscoverTargets", map[string]bool{"discover": true}, "")
	return err
}

func (b *Bridge) findAndAttachPageTarget(ctx context.Context) error {
	// Get targets via CDP
	result, err := b.sendCDP(ctx, "Target.getTargets", nil, "")
	if err != nil {
		return fmt.Errorf("getTargets: %w", err)
	}

	var targetsResp struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"title"`
			Attached bool   `json:"attached"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(result, &targetsResp); err != nil {
		return fmt.Errorf("unmarshal targets: %w", err)
	}

	// Find the first page target
	for _, t := range targetsResp.TargetInfos {
		if t.Type == "page" {
			// Attach to this target
			attachResult, err := b.sendCDP(ctx, "Target.attachToTarget", map[string]interface{}{
				"targetId": t.TargetID,
				"flatten":  true,
			}, "")
			if err != nil {
				return fmt.Errorf("attachToTarget: %w", err)
			}

			var attachResp struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(attachResult, &attachResp); err != nil {
				return fmt.Errorf("unmarshal attach: %w", err)
			}

			// Enable Runtime for the session
			_, err = b.sendCDP(ctx, "Runtime.enable", nil, attachResp.SessionID)
			if err != nil {
				b.logger.Warn("failed to enable Runtime", "err", err)
			}

			b.mu.Lock()
			b.sessionID = attachResp.SessionID
			b.activeTarget = TabInfo{
				TargetID: t.TargetID,
				URL:      t.URL,
				Title:    t.Title,
			}
			b.mu.Unlock()

			b.logger.Info("attached to page target", "id", t.TargetID, "url", t.URL, "session", attachResp.SessionID)
			return nil
		}
	}

	return fmt.Errorf("no page target found")
}

func (b *Bridge) evaluateInSession(ctx context.Context, sessionID, expression string, awaitPromise bool) (json.RawMessage, error) {
	result, err := b.sendCDP(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    expression,
		"awaitPromise":  awaitPromise,
		"returnByValue": true,
	}, sessionID)
	if err != nil {
		return nil, err
	}

	var evalResult struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
			Subtype     string          `json:"subtype"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	// Check if the result contains a CDP error
	var cdpErr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(result, &cdpErr) == nil && cdpErr.Error != "" {
		return nil, fmt.Errorf("CDP error: %s", cdpErr.Error)
	}

	if err := json.Unmarshal(result, &evalResult); err != nil {
		return nil, fmt.Errorf("unmarshal eval result: %w", err)
	}

	if evalResult.ExceptionDetails != nil {
		errMsg := evalResult.ExceptionDetails.Text
		if evalResult.ExceptionDetails.Exception.Description != "" {
			errMsg = evalResult.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("JS exception: %s", errMsg)
	}

	if evalResult.Result.Subtype == "error" {
		return nil, fmt.Errorf("JS error: %s", evalResult.Result.Description)
	}

	return evalResult.Result.Value, nil
}

func (b *Bridge) checkWebMCPAvailable(ctx context.Context, sessionID string) (bool, error) {
	js := `(function() {
		return !!(navigator.modelContextTesting && navigator.modelContextTesting.listTools);
	})()`

	result, err := b.evaluateInSession(ctx, sessionID, js, false)
	if err != nil {
		return false, err
	}

	var available bool
	if err := json.Unmarshal(result, &available); err != nil {
		return false, err
	}
	return available, nil
}

func (b *Bridge) queryTools(ctx context.Context, sessionID string) ([]Tool, error) {
	js := `(async () => {
		const mc = navigator.modelContextTesting;
		if (!mc || !mc.listTools) return JSON.stringify([]);
		try {
			const tools = await mc.listTools();
			if (!tools) return JSON.stringify([]);
			return JSON.stringify(tools.map(t => ({
				name: t.name || "",
				description: t.description || "",
				inputSchema: t.inputSchema || null,
				annotations: t.annotations || null
			})));
		} catch(e) {
			return JSON.stringify([]);
		}
	})()`

	result, err := b.evaluateInSession(ctx, sessionID, js, true)
	if err != nil {
		return nil, err
	}

	// The result is a JSON string value; unwrap it
	var jsonStr string
	if err := json.Unmarshal(result, &jsonStr); err != nil {
		// Maybe it's already the array directly
		var tools []Tool
		if err2 := json.Unmarshal(result, &tools); err2 != nil {
			return nil, fmt.Errorf("unmarshal tools: %w (also tried direct: %w)", err, err2)
		}
		return tools, nil
	}

	var tools []Tool
	if err := json.Unmarshal([]byte(jsonStr), &tools); err != nil {
		return nil, fmt.Errorf("unmarshal tools from string: %w", err)
	}
	return tools, nil
}

func (b *Bridge) checkAndEmitTools(ctx context.Context) {
	b.mu.Lock()
	session := b.sessionID
	b.mu.Unlock()

	if session == "" {
		return
	}

	// Small delay to let page load/register tools
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return
	case <-b.stopCh:
		return
	}

	available, err := b.checkWebMCPAvailable(ctx, session)
	if err != nil {
		b.logger.Warn("failed to check WebMCP availability", "err", err)
		b.emit(NewAvailableMsg(false))
		return
	}
	b.emit(NewAvailableMsg(available))

	if !available {
		b.mu.Lock()
		b.lastTools = nil
		b.mu.Unlock()
		return
	}

	tools, err := b.queryTools(ctx, session)
	if err != nil {
		b.logger.Warn("failed to query tools", "err", err)
		return
	}

	b.mu.Lock()
	b.lastTools = tools
	b.mu.Unlock()

	b.emit(NewToolsChangedMsg(tools))
}

func (b *Bridge) monitorLoop(ctx context.Context) {
	defer close(b.done)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		case <-ticker.C:
			b.mu.Lock()
			subscribed := b.subscribed
			session := b.sessionID
			b.mu.Unlock()

			if !subscribed {
				continue
			}

			// Re-attach if needed
			if session == "" {
				if err := b.findAndAttachPageTarget(ctx); err != nil {
					continue
				}
			}

			// Poll for tool changes
			b.mu.Lock()
			session = b.sessionID
			b.mu.Unlock()

			if session == "" {
				continue
			}

			tools, err := b.queryTools(ctx, session)
			if err != nil {
				b.logger.Debug("poll tools error", "err", err)
				// Session might be stale, clear it to force re-attach
				b.mu.Lock()
				b.sessionID = ""
				b.mu.Unlock()
				continue
			}

			// Compare with last known tools
			b.mu.Lock()
			changed := !toolsEqual(b.lastTools, tools)
			if changed {
				b.lastTools = tools
			}
			b.mu.Unlock()

			if changed {
				available := len(tools) > 0
				b.emit(NewAvailableMsg(available))
				b.emit(NewToolsChangedMsg(tools))
			}
		}
	}
}

func (b *Bridge) emit(msg ServerMessage) {
	b.mu.Lock()
	subscribed := b.subscribed
	b.mu.Unlock()
	if !subscribed {
		return
	}
	select {
	case b.eventCh <- msg:
	default:
		// Drop if channel full
		b.logger.Debug("dropping event, channel full", "type", msg.Type)
	}
}

// toolsEqual compares two tool slices for equality by name set.
func toolsEqual(a, b []Tool) bool {
	if len(a) != len(b) {
		return false
	}
	names := make(map[string]struct{}, len(a))
	for _, t := range a {
		names[t.Name] = struct{}{}
	}
	for _, t := range b {
		if _, ok := names[t.Name]; !ok {
			return false
		}
	}
	return true
}
