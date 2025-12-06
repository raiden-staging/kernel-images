package api

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/stream"
)

func (s *ApiService) HandleStreamSocket(w http.ResponseWriter, r *http.Request) {
	log := logger.FromContext(r.Context())
	streamID := chi.URLParam(r, "id")
	if streamID == "" {
		http.Error(w, "stream id required", http.StatusBadRequest)
		return
	}
	st, ok := s.streamManager.GetStream(streamID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	wsStreamer, ok := st.(stream.WebSocketEndpoint)
	if !ok {
		http.Error(w, "stream not websocket-enabled", http.StatusConflict)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		log.Error("failed to accept websocket for stream", "err", err, "stream_id", streamID)
		return
	}
	adapter := wsConnAdapter{Conn: conn}
	if err := wsStreamer.RegisterClient(adapter); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "registration failed")
		return
	}

	// Keep the connection open until the client disconnects.
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

// wsConnAdapter adapts coder/websocket.Conn to the stream.WebSocketConn interface.
type wsConnAdapter struct {
	*websocket.Conn
}

func (w wsConnAdapter) Read(ctx context.Context) (int, []byte, error) {
	mt, data, err := w.Conn.Read(ctx)
	return int(mt), data, err
}

func (w wsConnAdapter) Write(ctx context.Context, messageType int, data []byte) error {
	return w.Conn.Write(ctx, websocket.MessageType(messageType), data)
}

func (w wsConnAdapter) Close(status int, reason string) error {
	return w.Conn.Close(websocket.StatusCode(status), reason)
}
