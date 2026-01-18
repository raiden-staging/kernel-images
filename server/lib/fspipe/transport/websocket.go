package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onkernel/kernel-images/server/lib/fspipe/logging"
	"github.com/onkernel/kernel-images/server/lib/fspipe/protocol"
	"github.com/onkernel/kernel-images/server/lib/fspipe/queue"
)

// WebSocketClient manages WebSocket connection to the remote listener
type WebSocketClient struct {
	url    string
	config ClientConfig

	mu   sync.RWMutex
	conn *websocket.Conn

	state atomic.Int32 // ConnectionState

	// Message queue for non-blocking sends
	sendQueue *queue.Queue

	// Response channel for SendAndReceive
	responseCh chan wsResponse

	// Background goroutine management
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	startOnce  sync.Once
	shutdownMu sync.Mutex
	shutdown   bool

	// Metrics
	messagesSent     atomic.Uint64
	messagesAcked    atomic.Uint64
	messagesRetried  atomic.Uint64
	connectionLost   atomic.Uint64
	reconnectSuccess atomic.Uint64
}

type wsResponse struct {
	msgType byte
	data    []byte
	err     error
}

// NewWebSocketClient creates a new WebSocket transport client
func NewWebSocketClient(url string, config ClientConfig) *WebSocketClient {
	ctx, cancel := context.WithCancel(context.Background())

	c := &WebSocketClient{
		url:    url,
		config: config,
		ctx:    ctx,
		cancel: cancel,
		sendQueue: queue.New(queue.Config{
			MaxSize:    config.QueueSize,
			AckTimeout: config.AckTimeout,
			MaxRetries: 3,
		}),
		responseCh: make(chan wsResponse, 10),
	}

	c.state.Store(int32(StateDisconnected))
	return c
}

// Connect establishes WebSocket connection
func (c *WebSocketClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connectLocked(); err != nil {
		return err
	}

	// Start background workers
	c.startOnce.Do(func() {
		c.wg.Add(3)
		go c.sendLoop()
		go c.readLoop()
		go c.pingLoop()
	})

	return nil
}

func (c *WebSocketClient) connectLocked() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	c.state.Store(int32(StateConnecting))

	backoff := c.config.InitialBackoff
	attempt := 0

	dialer := websocket.Dialer{
		HandshakeTimeout: c.config.DialTimeout,
	}

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		attempt++

		conn, resp, err := dialer.Dial(c.url, http.Header{})
		if err != nil {
			if resp != nil {
				logging.Warn("WebSocket dial attempt %d failed: %v (status: %d)", attempt, err, resp.StatusCode)
			} else {
				logging.Warn("WebSocket dial attempt %d failed: %v", attempt, err)
			}

			if c.config.MaxRetries > 0 && attempt >= c.config.MaxRetries {
				c.state.Store(int32(StateFailed))
				return fmt.Errorf("failed to connect after %d retries: %w", attempt, err)
			}

			select {
			case <-c.ctx.Done():
				return c.ctx.Err()
			case <-time.After(backoff):
			}

			backoff = time.Duration(float64(backoff) * c.config.BackoffMultiplier)
			if backoff > c.config.MaxBackoff {
				backoff = c.config.MaxBackoff
			}
			continue
		}

		c.conn = conn
		c.state.Store(int32(StateConnected))
		logging.Info("WebSocket connected to %s (attempt %d)", c.url, attempt)
		c.reconnectSuccess.Add(1)
		return nil
	}
}

// sendLoop processes the message queue
func (c *WebSocketClient) sendLoop() {
	defer c.wg.Done()

	for {
		msg, err := c.sendQueue.Dequeue(c.ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, queue.ErrQueueClosed) {
				return
			}
			logging.Error("Dequeue error: %v", err)
			continue
		}

		err = c.sendMessage(msg)
		if err != nil {
			logging.Debug("Send failed for message %d: %v", msg.ID, err)
			c.handleSendError(msg, err)
		} else {
			c.messagesSent.Add(1)
		}
	}
}

func (c *WebSocketClient) sendMessage(msg *queue.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}

	// Build frame: [Length: 4B] [Type: 1B] [Payload: NB]
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	frameLen := uint32(1 + len(payload))
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, frameLen)
	buf.WriteByte(msg.Type)
	buf.Write(payload)

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(websocket.BinaryMessage, buf.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// For messages expecting ACK, track them
	if msg.Type == protocol.MsgWriteChunk {
		c.sendQueue.TrackPending(msg)
	} else {
		select {
		case msg.Result <- nil:
		default:
		}
	}

	return nil
}

// readLoop reads messages from WebSocket
func (c *WebSocketClient) readLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logging.Info("WebSocket closed normally")
			} else if !errors.Is(err, context.Canceled) {
				logging.Warn("WebSocket read error: %v", err)
				c.reconnect()
			}
			continue
		}

		if messageType != websocket.BinaryMessage {
			continue
		}

		// Parse frame
		if len(data) < 5 {
			logging.Warn("Invalid frame: too short")
			continue
		}

		frameLen := binary.BigEndian.Uint32(data[:4])
		if int(frameLen) != len(data)-4 {
			logging.Warn("Invalid frame length")
			continue
		}

		msgType := data[4]
		payload := data[5:]

		// Handle ACK messages
		if msgType == protocol.MsgWriteAck {
			var ack protocol.WriteAck
			if err := json.Unmarshal(payload, &ack); err == nil {
				// Find and complete the pending message
				// For now, just count it
				c.messagesAcked.Add(1)
			}
		}

		// Send to response channel for SendAndReceive
		select {
		case c.responseCh <- wsResponse{msgType: msgType, data: payload}:
		default:
		}
	}
}

// pingLoop sends periodic pings
func (c *WebSocketClient) pingLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					logging.Debug("Ping failed: %v", err)
				}
			}
		}
	}
}

func (c *WebSocketClient) handleSendError(msg *queue.Message, err error) {
	c.reconnect()

	msg.Retries++
	if msg.Retries <= 3 {
		c.messagesRetried.Add(1)
		c.sendQueue.Enqueue(c.ctx, msg.Type, msg.Payload)
	} else {
		select {
		case msg.Result <- err:
		default:
		}
	}
}

func (c *WebSocketClient) reconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ConnectionState(c.state.Load()) == StateReconnecting {
		return
	}

	c.state.Store(int32(StateReconnecting))
	c.connectionLost.Add(1)

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	count := c.sendQueue.RetryPending()
	if count > 0 {
		logging.Info("Re-queued %d pending messages for retry", count)
	}

	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.connectLocked(); err != nil {
			logging.Error("Reconnection failed: %v", err)
		}
	}()
}

// Send sends a message asynchronously
func (c *WebSocketClient) Send(msgType byte, payload interface{}) error {
	_, err := c.sendQueue.Enqueue(c.ctx, msgType, payload)
	return err
}

// SendAndReceive sends a message and waits for response
func (c *WebSocketClient) SendAndReceive(msgType byte, payload interface{}) (byte, []byte, error) {
	c.mu.Lock()

	if c.conn == nil {
		c.mu.Unlock()
		return 0, nil, ErrNotConnected
	}

	// Build and send frame
	data, err := json.Marshal(payload)
	if err != nil {
		c.mu.Unlock()
		return 0, nil, fmt.Errorf("marshal: %w", err)
	}

	frameLen := uint32(1 + len(data))
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, frameLen)
	buf.WriteByte(msgType)
	buf.Write(data)

	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := c.conn.WriteMessage(websocket.BinaryMessage, buf.Bytes()); err != nil {
		c.mu.Unlock()
		return 0, nil, fmt.Errorf("write: %w", err)
	}

	c.mu.Unlock()

	// Wait for response
	select {
	case resp := <-c.responseCh:
		c.messagesAcked.Add(1)
		return resp.msgType, resp.data, resp.err
	case <-time.After(c.config.AckTimeout):
		return 0, nil, errors.New("response timeout")
	case <-c.ctx.Done():
		return 0, nil, c.ctx.Err()
	}
}

// State returns current connection state
func (c *WebSocketClient) State() ConnectionState {
	return ConnectionState(c.state.Load())
}

// Stats returns client statistics
func (c *WebSocketClient) Stats() map[string]uint64 {
	return map[string]uint64{
		"messages_sent":     c.messagesSent.Load(),
		"messages_acked":    c.messagesAcked.Load(),
		"messages_retried":  c.messagesRetried.Load(),
		"connection_lost":   c.connectionLost.Load(),
		"reconnect_success": c.reconnectSuccess.Load(),
		"queue_length":      uint64(c.sendQueue.Len()),
		"pending_acks":      uint64(c.sendQueue.GetPendingCount()),
	}
}

// Close closes the WebSocket connection
func (c *WebSocketClient) Close() error {
	c.shutdownMu.Lock()
	if c.shutdown {
		c.shutdownMu.Unlock()
		return nil
	}
	c.shutdown = true
	c.shutdownMu.Unlock()

	c.cancel()
	c.sendQueue.Close()
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}
