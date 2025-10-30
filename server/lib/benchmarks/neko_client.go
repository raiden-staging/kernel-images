package benchmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

const (
	// Neko events
	NekoEventSystemBenchmarkCollect = "system/benchmark_collect"
	NekoEventSystemBenchmarkReady   = "system/benchmark_ready"
)

// NekoClient is a minimal websocket client for communicating with neko server
type NekoClient struct {
	logger *slog.Logger
	url    string
}

// NewNekoClient creates a new neko websocket client
func NewNekoClient(logger *slog.Logger, nekoURL string) *NekoClient {
	return &NekoClient{
		logger: logger,
		url:    nekoURL,
	}
}

// NekoMessage represents a neko websocket message
type NekoMessage struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NekoBenchmarkReadyPayload represents the benchmark ready response
type NekoBenchmarkReadyPayload struct {
	Timestamp int64 `json:"timestamp"`
}

// TriggerBenchmarkCollection sends a websocket message to neko to trigger benchmark collection
// Returns when neko responds with benchmark_ready
func (c *NekoClient) TriggerBenchmarkCollection(ctx context.Context) error {
	// Parse WebSocket URL from neko base URL
	wsURL, err := c.buildWebSocketURL()
	if err != nil {
		return fmt.Errorf("failed to build websocket URL: %w", err)
	}

	c.logger.Info("connecting to neko websocket", "url", wsURL)

	// Connect to websocket
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to neko websocket: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "benchmark collection complete")

	// Send benchmark collection trigger
	triggerMsg := NekoMessage{
		Event: NekoEventSystemBenchmarkCollect,
	}

	triggerData, err := json.Marshal(triggerMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal trigger message: %w", err)
	}

	c.logger.Info("sending benchmark collection trigger to neko")
	if err := conn.Write(ctx, websocket.MessageText, triggerData); err != nil {
		return fmt.Errorf("failed to send trigger message: %w", err)
	}

	// Wait for benchmark_ready response (with timeout)
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	c.logger.Info("waiting for neko to complete benchmark collection")

	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			if readCtx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timeout waiting for benchmark ready response")
			}
			return fmt.Errorf("failed to read websocket message: %w", err)
		}

		var msg NekoMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Warn("failed to unmarshal message, skipping", "err", err)
			continue
		}

		// Check if this is the benchmark_ready event
		if msg.Event == NekoEventSystemBenchmarkReady {
			c.logger.Info("received benchmark ready signal from neko")
			return nil
		}

		// Ignore other events (system/init, etc.)
		c.logger.Debug("received other event, ignoring", "event", msg.Event)
	}
}

// buildWebSocketURL converts neko HTTP URL to WebSocket URL
func (c *NekoClient) buildWebSocketURL() (string, error) {
	u, err := url.Parse(c.url)
	if err != nil {
		return "", err
	}

	// Convert http/https to ws/wss
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		// Already ws/wss
	}

	// Add /api/ws path
	u.Path = "/api/ws"

	return u.String(), nil
}
