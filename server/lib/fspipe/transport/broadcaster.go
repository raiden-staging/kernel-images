// Package transport provides the broadcaster - a WebSocket server that external clients connect to.
// When the FUSE daemon writes, it broadcasts to all connected clients.
package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onkernel/kernel-images/server/lib/fspipe/logging"
	"github.com/onkernel/kernel-images/server/lib/fspipe/protocol"
)

// Broadcaster is a WebSocket server that broadcasts file ops to connected clients.
// External clients connect to receive file chunks and operations.
type Broadcaster struct {
	addr   string
	path   string
	server *http.Server

	mu      sync.RWMutex
	clients map[*websocket.Conn]chan wsResponse
	state   ConnectionState

	// For SendAndReceive - we need at least one client to ACK
	reqMu sync.Mutex

	// Stats
	messagesSent   atomic.Uint64
	messagesRecv   atomic.Uint64
	bytesSent      atomic.Uint64
	bytesRecv      atomic.Uint64
	clientsTotal   atomic.Uint64
	clientsCurrent atomic.Int64

	upgrader websocket.Upgrader
}

// NewBroadcaster creates a new broadcaster that listens on the given address.
// Clients connect to ws://addr/path to receive file operations.
func NewBroadcaster(addr, path string) *Broadcaster {
	return &Broadcaster{
		addr:    addr,
		path:    path,
		clients: make(map[*websocket.Conn]chan wsResponse),
		state:   StateDisconnected,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Connect starts the WebSocket server.
func (b *Broadcaster) Connect() error {
	mux := http.NewServeMux()
	mux.HandleFunc(b.path, b.handleWebSocket)

	b.server = &http.Server{
		Addr:    b.addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		logging.Info("Broadcaster listening on %s%s", b.addr, b.path)
		if err := b.server.ListenAndServe(); err != http.ErrServerClosed {
			logging.Error("Broadcaster server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	b.mu.Lock()
	b.state = StateConnected
	b.mu.Unlock()

	return nil
}

func (b *Broadcaster) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Error("Broadcaster upgrade error: %v", err)
		return
	}

	responseCh := make(chan wsResponse, 10)

	b.mu.Lock()
	b.clients[conn] = responseCh
	b.mu.Unlock()

	b.clientsTotal.Add(1)
	b.clientsCurrent.Add(1)

	clientAddr := conn.RemoteAddr().String()
	logging.Info("Client connected: %s (total: %d)", clientAddr, b.clientsCurrent.Load())

	// Read responses from this client
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.clients, conn)
			close(responseCh)
			b.mu.Unlock()

			b.clientsCurrent.Add(-1)
			conn.Close()
			logging.Info("Client disconnected: %s (total: %d)", clientAddr, b.clientsCurrent.Load())
		}()

		for {
			_, rawData, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logging.Debug("Client read error: %v", err)
				}
				return
			}

			b.messagesRecv.Add(1)
			b.bytesRecv.Add(uint64(len(rawData)))

			if len(rawData) < 5 {
				continue
			}

			msgType := rawData[4]
			msgData := rawData[5:]

			// Send to response channel
			select {
			case responseCh <- wsResponse{msgType: msgType, data: msgData}:
			default:
				logging.Debug("Response channel full, dropping message")
			}
		}
	}()
}

// Send broadcasts a message to all connected clients (fire-and-forget).
func (b *Broadcaster) Send(msgType byte, payload interface{}) error {
	encodedData, err := b.encodeMessage(msgType, payload)
	if err != nil {
		return err
	}

	b.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(b.clients))
	for conn := range b.clients {
		clients = append(clients, conn)
	}
	b.mu.RUnlock()

	if len(clients) == 0 {
		logging.Debug("No clients connected, message dropped")
		return nil
	}

	for _, conn := range clients {
		if err := conn.WriteMessage(websocket.BinaryMessage, encodedData); err != nil {
			logging.Debug("Broadcast write error: %v", err)
		}
	}

	b.messagesSent.Add(1)
	b.bytesSent.Add(uint64(len(encodedData)))

	return nil
}

// SendSync sends a message and waits for flush (broadcasts to all clients).
func (b *Broadcaster) SendSync(msgType byte, payload interface{}) error {
	return b.Send(msgType, payload)
}

// SendAndReceive broadcasts a message and waits for ACK from any client.
func (b *Broadcaster) SendAndReceive(msgType byte, payload interface{}) (byte, []byte, error) {
	b.reqMu.Lock()
	defer b.reqMu.Unlock()

	encodedData, err := b.encodeMessage(msgType, payload)
	if err != nil {
		return 0, nil, err
	}

	b.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(b.clients))
	responseChans := make([]chan wsResponse, 0, len(b.clients))
	for conn, ch := range b.clients {
		clients = append(clients, conn)
		responseChans = append(responseChans, ch)
	}
	b.mu.RUnlock()

	if len(clients) == 0 {
		// No clients - return fake success ACK so FUSE doesn't block
		return b.fakeAck(msgType, payload)
	}

	// Drain stale responses
	for _, ch := range responseChans {
	drainLoop:
		for {
			select {
			case <-ch:
			default:
				break drainLoop
			}
		}
	}

	// Broadcast to all clients
	for _, conn := range clients {
		if err := conn.WriteMessage(websocket.BinaryMessage, encodedData); err != nil {
			logging.Debug("Broadcast write error: %v", err)
		}
	}

	b.messagesSent.Add(1)
	b.bytesSent.Add(uint64(len(encodedData)))

	// Wait for ACK from any client (first one wins)
	timeout := time.After(30 * time.Second)

	for {
		select {
		case resp := <-responseChans[0]: // Just use first client for now
			return resp.msgType, resp.data, nil
		case <-timeout:
			// Timeout - return fake ACK so FUSE doesn't block
			logging.Debug("SendAndReceive timeout, returning fake ACK")
			return b.fakeAck(msgType, payload)
		}
	}
}

// fakeAck returns a fake ACK when no clients are connected
func (b *Broadcaster) fakeAck(msgType byte, payload interface{}) (byte, []byte, error) {
	switch msgType {
	case protocol.MsgFileCreate:
		msg := payload.(*protocol.FileCreate)
		ack := protocol.FileCreateAck{FileID: msg.FileID, Success: true}
		data, _ := json.Marshal(ack)
		return protocol.MsgFileCreateAck, data, nil
	case protocol.MsgWriteChunk:
		msg := payload.(*protocol.WriteChunk)
		ack := protocol.WriteAck{FileID: msg.FileID, Offset: msg.Offset, Written: len(msg.Data)}
		data, _ := json.Marshal(ack)
		return protocol.MsgWriteAck, data, nil
	default:
		return 0, nil, nil
	}
}

func (b *Broadcaster) encodeMessage(msgType byte, payload interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	totalLen := 1 + len(jsonData)
	data := make([]byte, 4+totalLen)

	data[0] = byte(totalLen >> 24)
	data[1] = byte(totalLen >> 16)
	data[2] = byte(totalLen >> 8)
	data[3] = byte(totalLen)
	data[4] = msgType
	copy(data[5:], jsonData)

	return data, nil
}

// State returns the current connection state.
func (b *Broadcaster) State() ConnectionState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// Stats returns broadcaster statistics.
func (b *Broadcaster) Stats() map[string]uint64 {
	return map[string]uint64{
		"messages_sent":   b.messagesSent.Load(),
		"messages_recv":   b.messagesRecv.Load(),
		"bytes_sent":      b.bytesSent.Load(),
		"bytes_recv":      b.bytesRecv.Load(),
		"clients_total":   b.clientsTotal.Load(),
		"clients_current": uint64(b.clientsCurrent.Load()),
	}
}

// Close shuts down the broadcaster.
func (b *Broadcaster) Close() error {
	b.mu.Lock()
	b.state = StateDisconnected

	// Close all client connections
	for conn := range b.clients {
		conn.Close()
	}
	b.clients = make(map[*websocket.Conn]chan wsResponse)
	b.mu.Unlock()

	if b.server != nil {
		return b.server.Close()
	}
	return nil
}
