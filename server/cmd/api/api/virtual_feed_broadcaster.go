package api

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// virtualFeedBroadcaster fans out binary video chunks to multiple websocket listeners.
type virtualFeedBroadcaster struct {
	mu     sync.Mutex
	conns  map[*websocket.Conn]struct{}
	format string
}

func newVirtualFeedBroadcaster() *virtualFeedBroadcaster {
	return &virtualFeedBroadcaster{
		conns: make(map[*websocket.Conn]struct{}),
	}
}

func (b *virtualFeedBroadcaster) add(conn *websocket.Conn) {
	b.mu.Lock()
	if b.format != "" && b.format != "mpegts" {
		_ = writeWithTimeout(context.Background(), conn, websocket.MessageText, []byte(b.format))
	}
	b.conns[conn] = struct{}{}
	b.mu.Unlock()
}

func (b *virtualFeedBroadcaster) remove(conn *websocket.Conn) {
	b.mu.Lock()
	delete(b.conns, conn)
	b.mu.Unlock()
	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

func (b *virtualFeedBroadcaster) setFormat(format string) {
	b.mu.Lock()
	b.format = format
	// Avoid sending a text frame when using MPEG-TS so consumers like jsmpeg aren't confused.
	if format != "" && format != "mpegts" {
		for conn := range b.conns {
			_ = writeWithTimeout(context.Background(), conn, websocket.MessageText, []byte(format))
		}
	}
	b.mu.Unlock()
}

func (b *virtualFeedBroadcaster) clear() {
	b.mu.Lock()
	for conn := range b.conns {
		_ = conn.Close(websocket.StatusNormalClosure, "feed reset")
	}
	b.conns = make(map[*websocket.Conn]struct{})
	b.format = ""
	b.mu.Unlock()
}

func (b *virtualFeedBroadcaster) broadcastWithFormat(format string, data []byte) {
	if len(data) == 0 {
		return
	}

	b.mu.Lock()
	if format != "" && format != b.format {
		b.format = format
		if format != "mpegts" {
			for conn := range b.conns {
				_ = writeWithTimeout(context.Background(), conn, websocket.MessageText, []byte(format))
			}
		}
	}
	currentFormat := b.format
	conns := make([]*websocket.Conn, 0, len(b.conns))
	for conn := range b.conns {
		conns = append(conns, conn)
	}
	b.mu.Unlock()

	for _, conn := range conns {
		if err := writeWithTimeout(context.Background(), conn, websocket.MessageBinary, data); err != nil {
			b.remove(conn)
		}
	}

	// Ensure the stored format reflects the last broadcast even if no connections existed.
	if currentFormat == "" && format != "" {
		b.setFormat(format)
	}
}

func (b *virtualFeedBroadcaster) writer(format string) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		b.broadcastWithFormat(format, p)
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) {
	return w(p)
}

func writeWithTimeout(ctx context.Context, conn *websocket.Conn, msgType websocket.MessageType, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return conn.Write(writeCtx, msgType, data)
}
