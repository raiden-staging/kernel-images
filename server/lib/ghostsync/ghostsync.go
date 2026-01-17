// Package ghostsync provides real-time DOM bounding box synchronization from the browser
// to connected clients. It injects a MutationObserver into the browser page and broadcasts
// element positions to Vue clients via WebSocket.
package ghostsync

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/onkernel/kernel-images/server/lib/devtoolsproxy"
)

// GhostElement represents an interactive element in the browser
type GhostElement struct {
	ID   string      `json:"id"`
	Tag  string      `json:"tag"`
	Rect GhostRect   `json:"rect"`
	Z    json.Number `json:"z"`
}

// GhostRect represents a bounding rectangle
type GhostRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// GhostViewport represents the browser viewport dimensions
type GhostViewport struct {
	W  int `json:"w"`
	H  int `json:"h"`
	SX int `json:"sx"` // scrollX
	SY int `json:"sy"` // scrollY
}

// GhostSyncPayload is the data sent to clients
type GhostSyncPayload struct {
	Seq      int64          `json:"seq"`
	Ts       int64          `json:"ts"`
	Elements []GhostElement `json:"elements"`
	Viewport GhostViewport  `json:"viewport"`
	URL      string         `json:"url"`
}

// GhostMessage is the WebSocket message wrapper
type GhostMessage struct {
	Event string            `json:"event"`
	Data  *GhostSyncPayload `json:"data,omitempty"`
}

// Manager handles ghost DOM sync operations
type Manager struct {
	upstreamMgr *devtoolsproxy.UpstreamManager
	logger      *slog.Logger

	// Connected client WebSockets
	clientsMu sync.RWMutex
	clients   map[*websocket.Conn]struct{}

	// CDP connection state
	cdpMu      sync.Mutex
	cdpConn    *websocket.Conn
	cdpCancel  context.CancelFunc
	cdpRunning bool

	// Current attached target
	currentSessionID string
	currentTargetID  string

	// Last known state for new clients
	lastPayloadMu sync.RWMutex
	lastPayload   *GhostSyncPayload

	// Sequence counter
	seq atomic.Int64

	// Throttle state
	throttleMu      sync.Mutex
	lastBroadcastAt time.Time
	pendingPayload  *GhostSyncPayload
	throttleTimer   *time.Timer

	// CDP message counter for request IDs
	cdpMsgID atomic.Int64
}

const (
	throttleInterval = 50 * time.Millisecond // Max 20Hz updates
	bindingName      = "__ghostDomCallback__"
)

// Observer script to inject into the browser page
const observerScript = `
(function() {
  if (window.__ghostDomInitialized__) return;
  window.__ghostDomInitialized__ = true;

  const SELECTORS = 'input,button,a,select,textarea,[role="button"],[role="link"],[role="checkbox"],[role="radio"],[role="textbox"],[role="combobox"],[role="listbox"],[role="menuitem"],[onclick],[contenteditable]';
  let idCounter = 0;

  function extractElements() {
    const elements = [];
    document.querySelectorAll(SELECTORS).forEach((el) => {
      const rect = el.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return;

      const style = getComputedStyle(el);
      if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return;

      if (!el.dataset.ghostId) {
        el.dataset.ghostId = 'g' + (idCounter++);
      }

      elements.push({
        id: el.dataset.ghostId,
        tag: el.tagName.toLowerCase(),
        rect: {
          x: Math.round(rect.x),
          y: Math.round(rect.y),
          w: Math.round(rect.width),
          h: Math.round(rect.height)
        },
        z: parseInt(style.zIndex, 10) || 0
      });
    });

    return {
      elements,
      viewport: {
        w: window.innerWidth,
        h: window.innerHeight,
        sx: Math.round(window.scrollX),
        sy: Math.round(window.scrollY)
      },
      url: location.href
    };
  }

  function sendUpdate() {
    try {
      window.__ghostDomCallback__(JSON.stringify(extractElements()));
    } catch (e) {
      // Binding may not be ready yet
    }
  }

  // Debounce to avoid too many updates
  let debounceTimer = null;
  function debouncedSendUpdate() {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(sendUpdate, 16); // ~60fps max
  }

  const observer = new MutationObserver(debouncedSendUpdate);
  if (document.body) {
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['style', 'class', 'hidden', 'disabled']
    });
  }

  window.addEventListener('scroll', debouncedSendUpdate, { passive: true });
  window.addEventListener('resize', debouncedSendUpdate, { passive: true });

  // Send initial update after a short delay to ensure page is ready
  setTimeout(sendUpdate, 100);

  // Also send periodic updates to catch any missed changes
  setInterval(sendUpdate, 1000);

  const originalPushState = history.pushState;
  const originalReplaceState = history.replaceState;
  history.pushState = function(...args) {
    originalPushState.apply(this, args);
    setTimeout(sendUpdate, 200);
  };
  history.replaceState = function(...args) {
    originalReplaceState.apply(this, args);
    setTimeout(sendUpdate, 200);
  };
  window.addEventListener('popstate', () => setTimeout(sendUpdate, 200));

  // Re-observe when body changes (SPAs)
  const bodyObserver = new MutationObserver(() => {
    if (document.body && !document.body.__ghostObserving) {
      document.body.__ghostObserving = true;
      observer.observe(document.body, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['style', 'class', 'hidden', 'disabled']
      });
    }
  });
  bodyObserver.observe(document.documentElement, { childList: true });
})();
`

// NewManager creates a new ghost sync manager
func NewManager(upstreamMgr *devtoolsproxy.UpstreamManager, logger *slog.Logger) *Manager {
	return &Manager{
		upstreamMgr: upstreamMgr,
		logger:      logger,
		clients:     make(map[*websocket.Conn]struct{}),
	}
}

// Start begins the ghost sync manager, connecting to the browser via CDP
func (m *Manager) Start(ctx context.Context) {
	go m.cdpLoop(ctx)
}

// Stop shuts down the manager
func (m *Manager) Stop() {
	m.cdpMu.Lock()
	if m.cdpCancel != nil {
		m.cdpCancel()
	}
	m.cdpMu.Unlock()

	m.clientsMu.Lock()
	for conn := range m.clients {
		conn.Close(websocket.StatusGoingAway, "server shutting down")
		delete(m.clients, conn)
	}
	m.clientsMu.Unlock()
}

// HandleWebSocket handles a client WebSocket connection
func (m *Manager) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		m.logger.Error("[ghost-sync] websocket accept failed", "err", err)
		return
	}

	m.logger.Info("[ghost-sync] client connected")

	// Register client
	m.clientsMu.Lock()
	m.clients[conn] = struct{}{}
	m.clientsMu.Unlock()

	// Send last known state immediately
	m.lastPayloadMu.RLock()
	lastPayload := m.lastPayload
	m.lastPayloadMu.RUnlock()

	if lastPayload != nil {
		msg := GhostMessage{Event: "ghost/sync", Data: lastPayload}
		if data, err := json.Marshal(msg); err == nil {
			conn.Write(r.Context(), websocket.MessageText, data)
		}
	}

	// Handle incoming messages (client can send start/stop)
	go func() {
		defer func() {
			m.clientsMu.Lock()
			delete(m.clients, conn)
			m.clientsMu.Unlock()
			conn.Close(websocket.StatusNormalClosure, "")
			m.logger.Info("[ghost-sync] client disconnected")
		}()

		for {
			_, _, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			// We don't need to handle client messages for now
		}
	}()
}

// cdpLoop maintains the CDP connection and reinjects the observer on page loads
func (m *Manager) cdpLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Try with current URL
		if url := m.upstreamMgr.Current(); url != "" {
			m.connectAndObserve(ctx, url)
		}
		time.Sleep(2 * time.Second)
	}
}

func (m *Manager) connectAndObserve(ctx context.Context, upstreamURL string) {
	m.cdpMu.Lock()
	if m.cdpRunning {
		m.cdpMu.Unlock()
		return
	}
	m.cdpRunning = true
	cdpCtx, cancel := context.WithCancel(ctx)
	m.cdpCancel = cancel
	m.cdpMu.Unlock()

	defer func() {
		m.cdpMu.Lock()
		m.cdpRunning = false
		m.cdpCancel = nil
		m.currentSessionID = ""
		m.currentTargetID = ""
		m.cdpMu.Unlock()
	}()

	m.logger.Info("[ghost-sync] connecting to CDP", "url", upstreamURL)

	conn, _, err := websocket.Dial(cdpCtx, upstreamURL, nil)
	if err != nil {
		m.logger.Error("[ghost-sync] CDP dial failed", "err", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	m.cdpMu.Lock()
	m.cdpConn = conn
	m.cdpMu.Unlock()

	// Enable Target domain to discover and attach to pages
	if err := m.cdpSend(cdpCtx, conn, "", "Target.setDiscoverTargets", map[string]interface{}{
		"discover": true,
	}); err != nil {
		m.logger.Error("[ghost-sync] failed to enable target discovery", "err", err)
		return
	}

	m.logger.Info("[ghost-sync] CDP connected, discovering targets")

	// Find and attach to the first page target
	if err := m.findAndAttachToPage(cdpCtx, conn); err != nil {
		m.logger.Error("[ghost-sync] failed to attach to page", "err", err)
		return
	}

	// Listen for CDP events
	for {
		select {
		case <-cdpCtx.Done():
			return
		default:
		}

		_, msg, err := conn.Read(cdpCtx)
		if err != nil {
			m.logger.Error("[ghost-sync] CDP read error", "err", err)
			return
		}

		var event map[string]interface{}
		if err := json.Unmarshal(msg, &event); err != nil {
			continue
		}

		method, _ := event["method"].(string)
		params, _ := event["params"].(map[string]interface{})
		sessionId, _ := event["sessionId"].(string)

		switch method {
		case "Runtime.bindingCalled":
			// Handle ghost DOM callback - only from our session
			if sessionId == m.currentSessionID {
				name, _ := params["name"].(string)
				payload, _ := params["payload"].(string)
				if name == bindingName && payload != "" {
					m.handleGhostCallback(payload)
				}
			}

		case "Page.loadEventFired", "Page.domContentEventFired":
			// Re-inject observer on page load
			if sessionId == m.currentSessionID {
				go func() {
					time.Sleep(200 * time.Millisecond)
					m.injectObserverScript(cdpCtx, conn, m.currentSessionID)
				}()
			}

		case "Page.frameNavigated":
			// Re-inject on navigation
			if sessionId == m.currentSessionID {
				go func() {
					time.Sleep(300 * time.Millisecond)
					m.injectObserverScript(cdpCtx, conn, m.currentSessionID)
				}()
			}

		case "Target.targetInfoChanged":
			// Check if a different tab became active
			targetInfo, _ := params["targetInfo"].(map[string]interface{})
			if targetInfo != nil {
				targetType, _ := targetInfo["type"].(string)
				targetID, _ := targetInfo["targetId"].(string)
				attached, _ := targetInfo["attached"].(bool)

				// If this is a page and it's not our current target, consider switching
				if targetType == "page" && targetID != m.currentTargetID && !attached {
					m.logger.Debug("[ghost-sync] detected new page target", "targetId", targetID)
				}
			}

		case "Target.targetCreated":
			// A new target was created
			targetInfo, _ := params["targetInfo"].(map[string]interface{})
			if targetInfo != nil {
				targetType, _ := targetInfo["type"].(string)
				if targetType == "page" {
					m.logger.Debug("[ghost-sync] new page target created")
				}
			}

		case "Target.targetDestroyed":
			// Our target was destroyed, need to find a new one
			targetID, _ := params["targetId"].(string)
			if targetID == m.currentTargetID {
				m.logger.Info("[ghost-sync] current target destroyed, finding new target")
				go func() {
					time.Sleep(500 * time.Millisecond)
					m.findAndAttachToPage(cdpCtx, conn)
				}()
			}

		case "Target.detachedFromTarget":
			// We were detached from the target
			detachedSessionId, _ := params["sessionId"].(string)
			if detachedSessionId == m.currentSessionID {
				m.logger.Info("[ghost-sync] detached from target, reattaching")
				m.currentSessionID = ""
				m.currentTargetID = ""
				go func() {
					time.Sleep(500 * time.Millisecond)
					m.findAndAttachToPage(cdpCtx, conn)
				}()
			}
		}
	}
}

func (m *Manager) findAndAttachToPage(ctx context.Context, conn *websocket.Conn) error {
	// Get list of targets
	id := m.cdpMsgID.Add(1)
	msg := map[string]interface{}{
		"id":     id,
		"method": "Target.getTargets",
	}
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		return err
	}

	// Read response - we need to find the result
	for i := 0; i < 10; i++ {
		_, respData, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(respData, &resp); err != nil {
			continue
		}

		// Check if this is our response
		respID, _ := resp["id"].(float64)
		if int64(respID) == id {
			result, _ := resp["result"].(map[string]interface{})
			targetInfos, _ := result["targetInfos"].([]interface{})

			// Find the first page target
			for _, ti := range targetInfos {
				targetInfo, _ := ti.(map[string]interface{})
				targetType, _ := targetInfo["type"].(string)
				targetID, _ := targetInfo["targetId"].(string)

				if targetType == "page" && targetID != "" {
					m.logger.Info("[ghost-sync] found page target", "targetId", targetID)
					return m.attachToTarget(ctx, conn, targetID)
				}
			}
			m.logger.Warn("[ghost-sync] no page targets found")
			return nil
		}

		// If it's an event, we might need to handle it
		if method, ok := resp["method"].(string); ok {
			m.logger.Debug("[ghost-sync] received event while waiting for targets", "method", method)
		}
	}

	return nil
}

func (m *Manager) attachToTarget(ctx context.Context, conn *websocket.Conn, targetID string) error {
	// Attach to the target with flatten=true to get a session
	id := m.cdpMsgID.Add(1)
	msg := map[string]interface{}{
		"id":     id,
		"method": "Target.attachToTarget",
		"params": map[string]interface{}{
			"targetId": targetID,
			"flatten":  true,
		},
	}
	data, _ := json.Marshal(msg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		return err
	}

	// Read response to get session ID
	for i := 0; i < 10; i++ {
		_, respData, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(respData, &resp); err != nil {
			continue
		}

		respID, _ := resp["id"].(float64)
		if int64(respID) == id {
			result, _ := resp["result"].(map[string]interface{})
			sessionId, _ := result["sessionId"].(string)

			if sessionId == "" {
				m.logger.Error("[ghost-sync] no session ID in attach response")
				return nil
			}

			m.cdpMu.Lock()
			m.currentSessionID = sessionId
			m.currentTargetID = targetID
			m.cdpMu.Unlock()

			m.logger.Info("[ghost-sync] attached to target", "sessionId", sessionId, "targetId", targetID)

			// Now set up the session
			return m.setupSession(ctx, conn, sessionId)
		}
	}

	return nil
}

func (m *Manager) setupSession(ctx context.Context, conn *websocket.Conn, sessionId string) error {
	// Add the binding for the callback
	if err := m.cdpSend(ctx, conn, sessionId, "Runtime.addBinding", map[string]interface{}{
		"name": bindingName,
	}); err != nil {
		m.logger.Error("[ghost-sync] failed to add binding", "err", err)
		return err
	}

	// Enable Runtime domain to receive binding calls
	if err := m.cdpSend(ctx, conn, sessionId, "Runtime.enable", nil); err != nil {
		m.logger.Error("[ghost-sync] failed to enable Runtime", "err", err)
		return err
	}

	// Enable Page domain for load events
	if err := m.cdpSend(ctx, conn, sessionId, "Page.enable", nil); err != nil {
		m.logger.Error("[ghost-sync] failed to enable Page", "err", err)
		return err
	}

	// Add script to evaluate on every new document (persists across navigations)
	if err := m.cdpSend(ctx, conn, sessionId, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"source": observerScript,
	}); err != nil {
		m.logger.Warn("[ghost-sync] failed to add script to new documents", "err", err)
	}

	// Inject the observer script now
	if err := m.injectObserverScript(ctx, conn, sessionId); err != nil {
		m.logger.Error("[ghost-sync] failed to inject observer", "err", err)
		return err
	}

	m.logger.Info("[ghost-sync] session setup complete")
	return nil
}

func (m *Manager) cdpSend(ctx context.Context, conn *websocket.Conn, sessionId string, method string, params interface{}) error {
	id := m.cdpMsgID.Add(1)
	msg := map[string]interface{}{
		"id":     id,
		"method": method,
	}
	if sessionId != "" {
		msg["sessionId"] = sessionId
	}
	if params != nil {
		msg["params"] = params
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func (m *Manager) injectObserverScript(ctx context.Context, conn *websocket.Conn, sessionId string) error {
	m.logger.Debug("[ghost-sync] injecting observer script", "sessionId", sessionId)
	return m.cdpSend(ctx, conn, sessionId, "Runtime.evaluate", map[string]interface{}{
		"expression":            observerScript,
		"includeCommandLineAPI": true,
	})
}

func (m *Manager) handleGhostCallback(payload string) {
	var data struct {
		Elements []GhostElement `json:"elements"`
		Viewport GhostViewport  `json:"viewport"`
		URL      string         `json:"url"`
	}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		m.logger.Error("[ghost-sync] failed to parse callback payload", "err", err)
		return
	}

	syncPayload := &GhostSyncPayload{
		Seq:      m.seq.Add(1),
		Ts:       time.Now().UnixMilli(),
		Elements: data.Elements,
		Viewport: data.Viewport,
		URL:      data.URL,
	}

	// Throttle broadcasts
	m.throttleMu.Lock()
	now := time.Now()
	timeSinceLast := now.Sub(m.lastBroadcastAt)

	if timeSinceLast >= throttleInterval {
		m.lastBroadcastAt = now
		m.throttleMu.Unlock()
		m.broadcast(syncPayload)
	} else {
		// Schedule deferred broadcast
		m.pendingPayload = syncPayload
		if m.throttleTimer == nil {
			m.throttleTimer = time.AfterFunc(throttleInterval-timeSinceLast, func() {
				m.throttleMu.Lock()
				pending := m.pendingPayload
				m.pendingPayload = nil
				m.throttleTimer = nil
				m.lastBroadcastAt = time.Now()
				m.throttleMu.Unlock()
				if pending != nil {
					m.broadcast(pending)
				}
			})
		}
		m.throttleMu.Unlock()
	}
}

func (m *Manager) broadcast(payload *GhostSyncPayload) {
	// Store last payload for new clients
	m.lastPayloadMu.Lock()
	m.lastPayload = payload
	m.lastPayloadMu.Unlock()

	msg := GhostMessage{Event: "ghost/sync", Data: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		m.logger.Error("[ghost-sync] failed to marshal payload", "err", err)
		return
	}

	m.clientsMu.RLock()
	clients := make([]*websocket.Conn, 0, len(m.clients))
	for conn := range m.clients {
		clients = append(clients, conn)
	}
	m.clientsMu.RUnlock()

	for _, conn := range clients {
		if err := conn.Write(context.Background(), websocket.MessageText, data); err != nil {
			m.logger.Debug("[ghost-sync] failed to send to client", "err", err)
		}
	}

	if len(clients) > 0 {
		m.logger.Debug("[ghost-sync] broadcast to clients",
			"clients", len(clients),
			"elements", len(payload.Elements),
			"seq", payload.Seq)
	}
}

// ClientCount returns the number of connected clients
func (m *Manager) ClientCount() int {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()
	return len(m.clients)
}

// IsConnected returns whether the manager is connected to CDP
func (m *Manager) IsConnected() bool {
	m.cdpMu.Lock()
	defer m.cdpMu.Unlock()
	return m.cdpRunning
}

// Handler returns an http.Handler for the ghost sync WebSocket endpoint
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.HandleWebSocket(w, r)
	})
}

// GetLastPayload returns the last known ghost sync payload
func (m *Manager) GetLastPayload() *GhostSyncPayload {
	m.lastPayloadMu.RLock()
	defer m.lastPayloadMu.RUnlock()
	return m.lastPayload
}

// Status returns the current status of the ghost sync manager
type Status struct {
	Connected   bool   `json:"connected"`
	ClientCount int    `json:"clientCount"`
	LastSeq     int64  `json:"lastSeq"`
	SessionID   string `json:"sessionId"`
	TargetID    string `json:"targetId"`
}

func (m *Manager) Status() Status {
	m.lastPayloadMu.RLock()
	lastSeq := int64(0)
	if m.lastPayload != nil {
		lastSeq = m.lastPayload.Seq
	}
	m.lastPayloadMu.RUnlock()

	m.cdpMu.Lock()
	sessionID := m.currentSessionID
	targetID := m.currentTargetID
	m.cdpMu.Unlock()

	return Status{
		Connected:   m.IsConnected(),
		ClientCount: m.ClientCount(),
		LastSeq:     lastSeq,
		SessionID:   sessionID,
		TargetID:    targetID,
	}
}
