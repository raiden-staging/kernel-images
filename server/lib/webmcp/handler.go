package webmcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
)

// Handler manages WebSocket connections for the WebMCP feature.
type Handler struct {
	logger      *slog.Logger
	getUpstream func() string // returns the current Chrome CDP upstream WS URL
}

// NewHandler creates a new WebMCP WebSocket handler.
func NewHandler(logger *slog.Logger, getUpstream func() string) *Handler {
	return &Handler{
		logger:      logger,
		getUpstream: getUpstream,
	}
}

// ServeHTTP upgrades the connection to WebSocket and manages the WebMCP session.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upstreamURL := h.getUpstream()
	if upstreamURL == "" {
		http.Error(w, "Chrome CDP upstream not ready", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		h.logger.Error("webmcp: websocket accept failed", "err", err)
		return
	}
	conn.SetReadLimit(10 * 1024 * 1024) // 10 MB

	ctx := r.Context()

	bridge := NewBridge(upstreamURL, h.logger)
	if err := bridge.Start(ctx); err != nil {
		h.logger.Error("webmcp: bridge start failed", "err", err)
		_ = conn.Close(websocket.StatusInternalError, "failed to connect to Chrome CDP")
		return
	}
	defer bridge.Close()

	h.logger.Info("webmcp: session started")

	// Forward bridge events to client (only when subscribed)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-bridge.stopCh:
				return
			case msg, ok := <-bridge.Events():
				if !ok {
					return
				}
				// Drop events if client has unsubscribed
				bridge.mu.Lock()
				subscribed := bridge.subscribed
				bridge.mu.Unlock()
				if !subscribed {
					continue
				}
				data, err := json.Marshal(msg)
				if err != nil {
					h.logger.Error("webmcp: marshal event", "err", err)
					continue
				}
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					h.logger.Debug("webmcp: write to client failed", "err", err)
					return
				}
			}
		}
	}()

	// Read client messages
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			h.logger.Debug("webmcp: client read error", "err", err)
			break
		}

		var clientMsg ClientMessage
		if err := json.Unmarshal(data, &clientMsg); err != nil {
			h.writeError(ctx, conn, "", "invalid message format")
			continue
		}

		h.handleClientMessage(ctx, conn, bridge, clientMsg)
	}

	h.logger.Info("webmcp: session ended")
}

func (h *Handler) handleClientMessage(ctx context.Context, conn *websocket.Conn, bridge *Bridge, msg ClientMessage) {
	switch msg.Type {
	case MsgSubscribe:
		bridge.Subscribe()
		// Immediately check tools on subscribe
		go bridge.checkAndEmitTools(ctx)

	case MsgUnsubscribe:
		bridge.Unsubscribe()

	case MsgListTools:
		tools, err := bridge.ListTools(ctx)
		if err != nil {
			h.writeJSON(ctx, conn, NewToolErrorMsg(msg.ID, err.Error()))
			return
		}
		h.writeJSON(ctx, conn, NewToolsChangedMsg(tools))

	case MsgCallTool:
		if msg.ToolName == "" {
			h.writeJSON(ctx, conn, NewToolErrorMsg(msg.ID, "tool_name is required"))
			return
		}
		// Execute tool call asynchronously
		go func() {
			result, err := bridge.CallTool(ctx, msg.ToolName, msg.Arguments)
			if err != nil {
				h.writeJSON(ctx, conn, NewToolErrorMsg(msg.ID, err.Error()))
				return
			}
			h.writeJSON(ctx, conn, NewToolResultMsg(msg.ID, result))
		}()

	default:
		h.writeError(ctx, conn, msg.ID, "unknown message type: "+msg.Type)
	}
}

func (h *Handler) writeJSON(ctx context.Context, conn *websocket.Conn, msg ServerMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("webmcp: marshal response", "err", err)
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		h.logger.Debug("webmcp: write failed", "err", err)
	}
}

func (h *Handler) writeError(ctx context.Context, conn *websocket.Conn, id, errMsg string) {
	h.writeJSON(ctx, conn, NewErrorMsg(errMsg))
}
