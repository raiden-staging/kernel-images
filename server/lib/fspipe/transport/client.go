package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onkernel/kernel-images/server/lib/fspipe/logging"
	"github.com/onkernel/kernel-images/server/lib/fspipe/protocol"
	"github.com/onkernel/kernel-images/server/lib/fspipe/queue"
)

var (
	ErrNotConnected = errors.New("not connected")
	ErrSendFailed   = errors.New("send failed")
)

// ConnectionState represents the connection status
type ConnectionState int32

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	StateFailed
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// ClientConfig holds client configuration
type ClientConfig struct {
	// Connection settings
	DialTimeout       time.Duration
	MaxRetries        int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64

	// Health check settings
	HealthCheckInterval time.Duration
	PingTimeout         time.Duration

	// Queue settings
	QueueSize  int
	AckTimeout time.Duration
}

// DefaultClientConfig returns production-ready defaults
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		DialTimeout:         10 * time.Second,
		MaxRetries:          0, // 0 = infinite retries
		InitialBackoff:      1 * time.Second,
		MaxBackoff:          60 * time.Second,
		BackoffMultiplier:   2.0,
		HealthCheckInterval: 10 * time.Second,
		PingTimeout:         5 * time.Second,
		QueueSize:           1000,
		AckTimeout:          30 * time.Second,
	}
}

// Client manages the connection to the remote listener
type Client struct {
	addr   string
	config ClientConfig

	mu      sync.RWMutex
	conn    net.Conn
	encoder *protocol.Encoder
	decoder *protocol.Decoder
	bufW    *bufio.Writer

	state atomic.Int32 // ConnectionState

	// Message queue for non-blocking sends
	sendQueue *queue.Queue

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

// NewClient creates a new transport client with default config
func NewClient(addr string) *Client {
	return NewClientWithConfig(addr, DefaultClientConfig())
}

// NewClientWithConfig creates a new transport client with custom config
func NewClientWithConfig(addr string, config ClientConfig) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Client{
		addr:   addr,
		config: config,
		ctx:    ctx,
		cancel: cancel,
		sendQueue: queue.New(queue.Config{
			MaxSize:    config.QueueSize,
			AckTimeout: config.AckTimeout,
			MaxRetries: 3,
		}),
	}

	c.state.Store(int32(StateDisconnected))
	return c
}

// Connect establishes connection to the remote server
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connectLocked(); err != nil {
		return err
	}

	// Start background workers
	c.startOnce.Do(func() {
		c.wg.Add(2)
		go c.sendLoop()
		go c.healthCheckLoop()
	})

	return nil
}

func (c *Client) connectLocked() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	c.state.Store(int32(StateConnecting))

	backoff := c.config.InitialBackoff
	attempt := 0

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		attempt++
		conn, err := net.DialTimeout("tcp", c.addr, c.config.DialTimeout)
		if err != nil {
			logging.Warn("Connection attempt %d to %s failed: %v", attempt, c.addr, err)

			if c.config.MaxRetries > 0 && attempt >= c.config.MaxRetries {
				c.state.Store(int32(StateFailed))
				return fmt.Errorf("failed to connect after %d retries: %w", attempt, err)
			}

			// Exponential backoff
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

		// Configure connection
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
			tcpConn.SetNoDelay(true)
		}

		c.conn = conn
		c.bufW = bufio.NewWriterSize(conn, 64*1024)
		c.encoder = protocol.NewEncoder(c.bufW)
		c.decoder = protocol.NewDecoder(bufio.NewReaderSize(conn, 64*1024))

		c.state.Store(int32(StateConnected))
		logging.Info("Connected to %s (attempt %d)", c.addr, attempt)
		c.reconnectSuccess.Add(1)
		return nil
	}
}

// sendLoop processes the message queue
func (c *Client) sendLoop() {
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

func (c *Client) sendMessage(msg *queue.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}

	// Set write deadline
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	if err := c.encoder.Encode(msg.Type, msg.Payload); err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	if err := c.bufW.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	// For messages expecting ACK, track them
	if msg.Type == protocol.MsgWriteChunk {
		c.sendQueue.TrackPending(msg)
	} else {
		// Non-ACK messages complete immediately
		select {
		case msg.Result <- nil:
		default:
		}
	}

	return nil
}

func (c *Client) handleSendError(msg *queue.Message, err error) {
	// Trigger reconnection
	c.reconnect()

	// Re-queue the message
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

func (c *Client) reconnect() {
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

	// Re-queue pending messages
	count := c.sendQueue.RetryPending()
	if count > 0 {
		logging.Info("Re-queued %d pending messages for retry", count)
	}

	// Reconnect in background
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if err := c.connectLocked(); err != nil {
			logging.Error("Reconnection failed: %v", err)
		}
	}()
}

// healthCheckLoop monitors connection health
func (c *Client) healthCheckLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if ConnectionState(c.state.Load()) != StateConnected {
				continue
			}

			// Simple health check: try to write
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()

			if conn != nil {
				conn.SetWriteDeadline(time.Now().Add(c.config.PingTimeout))
				// TCP keepalive handles actual ping
			}
		}
	}
}

// Send sends a message asynchronously (non-blocking)
func (c *Client) Send(msgType byte, payload interface{}) error {
	_, err := c.sendQueue.Enqueue(c.ctx, msgType, payload)
	return err
}

// SendSync sends a message and waits for completion
func (c *Client) SendSync(msgType byte, payload interface{}) error {
	return c.sendQueue.EnqueueSync(c.ctx, msgType, payload)
}

// SendAndReceive sends a message and waits for a response
func (c *Client) SendAndReceive(msgType byte, payload interface{}) (byte, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return 0, nil, ErrNotConnected
	}

	// Set deadlines
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	c.conn.SetReadDeadline(time.Now().Add(c.config.AckTimeout))

	if err := c.encoder.Encode(msgType, payload); err != nil {
		return 0, nil, fmt.Errorf("send: %w", err)
	}

	if err := c.bufW.Flush(); err != nil {
		return 0, nil, fmt.Errorf("flush: %w", err)
	}

	respType, respData, err := c.decoder.Decode()
	if err != nil {
		return 0, nil, fmt.Errorf("receive: %w", err)
	}

	c.messagesAcked.Add(1)
	return respType, respData, nil
}

// State returns the current connection state
func (c *Client) State() ConnectionState {
	return ConnectionState(c.state.Load())
}

// Stats returns client statistics
func (c *Client) Stats() map[string]uint64 {
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

// Close closes the connection
func (c *Client) Close() error {
	c.shutdownMu.Lock()
	if c.shutdown {
		c.shutdownMu.Unlock()
		return nil
	}
	c.shutdown = true
	c.shutdownMu.Unlock()

	// Signal goroutines to stop
	c.cancel()

	// Close queue
	c.sendQueue.Close()

	// Wait for goroutines
	c.wg.Wait()

	// Close connection
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}
