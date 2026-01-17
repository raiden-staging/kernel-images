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
	Seq      int64         `json:"seq"`
	Ts       int64         `json:"ts"`
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

  const observer = new MutationObserver(() => sendUpdate());
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['style', 'class', 'hidden', 'disabled']
  });

  window.addEventListener('scroll', sendUpdate, { passive: true });
  window.addEventListener('resize', sendUpdate, { passive: true });

  sendUpdate();

  const originalPushState = history.pushState;
  const originalReplaceState = history.replaceState;
  history.pushState = function(...args) {
    originalPushState.apply(this, args);
    setTimeout(sendUpdate, 100);
  };
  history.replaceState = function(...args) {
    originalReplaceState.apply(this, args);
    setTimeout(sendUpdate, 100);
  };
  window.addEventListener('popstate', () => setTimeout(sendUpdate, 100));
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
	// Subscribe to upstream URL changes to reconnect when Chrome restarts
	upstreamCh, cancelSub := m.upstreamMgr.Subscribe()
	defer cancelSub()

	for {
		select {
		case <-ctx.Done():
			return
		case upstreamURL, ok := <-upstreamCh:
			if !ok {
				return
			}
			m.connectAndObserve(ctx, upstreamURL)
		default:
			// Try with current URL
			if url := m.upstreamMgr.Current(); url != "" {
				m.connectAndObserve(ctx, url)
			}
			time.Sleep(2 * time.Second)
		}
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

	// Add the binding for the callback
	if err := m.cdpSend(cdpCtx, conn, "Runtime.addBinding", map[string]interface{}{
		"name": bindingName,
	}); err != nil {
		m.logger.Error("[ghost-sync] failed to add binding", "err", err)
		return
	}

	// Enable Runtime domain to receive binding calls
	if err := m.cdpSend(cdpCtx, conn, "Runtime.enable", nil); err != nil {
		m.logger.Error("[ghost-sync] failed to enable Runtime", "err", err)
		return
	}

	// Enable Page domain for load events
	if err := m.cdpSend(cdpCtx, conn, "Page.enable", nil); err != nil {
		m.logger.Error("[ghost-sync] failed to enable Page", "err", err)
		return
	}

	// Inject the observer script
	if err := m.injectObserverScript(cdpCtx, conn); err != nil {
		m.logger.Error("[ghost-sync] failed to inject observer", "err", err)
		return
	}

	m.logger.Info("[ghost-sync] CDP connected and observer injected")

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

		switch method {
		case "Runtime.bindingCalled":
			// Handle ghost DOM callback
			name, _ := params["name"].(string)
			payload, _ := params["payload"].(string)
			if name == bindingName {
				m.handleGhostCallback(payload)
			}
		case "Page.loadEventFired", "Page.frameNavigated":
			// Re-inject observer on navigation
			go func() {
				time.Sleep(100 * time.Millisecond)
				m.injectObserverScript(cdpCtx, conn)
			}()
		}
	}
}

func (m *Manager) cdpSend(ctx context.Context, conn *websocket.Conn, method string, params interface{}) error {
	id := m.cdpMsgID.Add(1)
	msg := map[string]interface{}{
		"id":     id,
		"method": method,
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

func (m *Manager) injectObserverScript(ctx context.Context, conn *websocket.Conn) error {
	return m.cdpSend(ctx, conn, "Runtime.evaluate", map[string]interface{}{
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
	Connected   bool  `json:"connected"`
	ClientCount int   `json:"clientCount"`
	LastSeq     int64 `json:"lastSeq"`
}

func (m *Manager) Status() Status {
	m.lastPayloadMu.RLock()
	lastSeq := int64(0)
	if m.lastPayload != nil {
		lastSeq = m.lastPayload.Seq
	}
	m.lastPayloadMu.RUnlock()

	return Status{
		Connected:   m.IsConnected(),
		ClientCount: m.ClientCount(),
		LastSeq:     lastSeq,
	}
}
