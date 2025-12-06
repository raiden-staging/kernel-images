package api

import (
	"net/http"

	"github.com/coder/websocket"

	"github.com/onkernel/kernel-images/server/lib/logger"
)

// HandleVirtualInputFeedSocket broadcasts the live virtual video feed chunks to websocket listeners.
func (s *ApiService) HandleVirtualInputFeedSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("failed to accept virtual feed websocket", "err", err)
		return
	}
	defer s.virtualFeed.remove(conn)

	s.virtualFeed.add(conn)

	// Consume incoming frames until the client disconnects.
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}
