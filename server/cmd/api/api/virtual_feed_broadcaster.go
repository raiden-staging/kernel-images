package api

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// virtualFeedBroadcaster fans out binary video chunks to multiple websocket listeners.
// This broadcaster operates in real-time mode: no caching of previous chunks.
// New clients joining the stream start from the current point, not from cached data.
type virtualFeedBroadcaster struct {
	mu       sync.Mutex
	conns    map[*websocket.Conn]struct{}
	format   string
	preamble []byte // IVF header only (32 bytes max) - needed for decoder initialization
	// Note: No intro cache - real-time streaming means no replay of old data
}

func isIVFHeader(data []byte) bool {
	return len(data) >= 4 &&
		data[0] == 'D' &&
		data[1] == 'K' &&
		data[2] == 'I' &&
		data[3] == 'F'
}

func newVirtualFeedBroadcaster() *virtualFeedBroadcaster {
	return &virtualFeedBroadcaster{
		conns: make(map[*websocket.Conn]struct{}),
	}
}

func (b *virtualFeedBroadcaster) add(conn *websocket.Conn) {
	b.mu.Lock()
	format := b.format
	needsHint := format != "" && format != "mpegts"
	preamble := append([]byte(nil), b.preamble...)
	b.conns[conn] = struct{}{}
	b.mu.Unlock()

	// Send format hint for non-MPEG-TS streams (e.g., IVF)
	if needsHint {
		_ = writeWithTimeout(context.Background(), conn, websocket.MessageText, []byte(format))
	}
	// Send IVF header (preamble) if available - needed for decoder initialization
	// This is the only cached data we send; no intro/buffered video data
	if len(preamble) > 0 {
		_ = writeWithTimeout(context.Background(), conn, websocket.MessageBinary, preamble)
	}
	// Real-time mode: client starts receiving from current stream position
	// No cached video data is sent - this ensures true real-time behavior
}

func (b *virtualFeedBroadcaster) remove(conn *websocket.Conn) {
	b.mu.Lock()
	delete(b.conns, conn)
	b.mu.Unlock()
	_ = conn.Close(websocket.StatusNormalClosure, "done")
}

func (b *virtualFeedBroadcaster) setFormat(format string) {
	b.mu.Lock()
	if format != "" && format != b.format {
		b.preamble = nil
	}
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
	b.preamble = nil
	b.mu.Unlock()
}

func (b *virtualFeedBroadcaster) broadcastWithFormat(format string, data []byte) {
	if len(data) == 0 {
		return
	}

	resetIVF := format == "ivf" && isIVFHeader(data)
	b.mu.Lock()
	if format != "" && format != b.format {
		b.preamble = nil
		b.format = format
		if format != "mpegts" {
			for conn := range b.conns {
				_ = writeWithTimeout(context.Background(), conn, websocket.MessageText, []byte(format))
			}
		}
	}
	if resetIVF {
		// Stream restarted with a fresh IVF header; refresh the cached preamble so
		// new listeners get the correct dimensions and existing clients can reset.
		b.preamble = nil
	}
	// Only cache the IVF header (32 bytes) - needed for decoder initialization
	// No other video data is cached to ensure true real-time behavior
	if format == "ivf" && len(b.preamble) < 32 {
		needed := 32 - len(b.preamble)
		if needed > len(data) {
			needed = len(data)
		}
		if needed > 0 {
			b.preamble = append(b.preamble, data[:needed]...)
		}
	}
	// Real-time mode: No intro buffer - all data is broadcast immediately
	// and not cached for later clients. This ensures:
	// 1. Page refresh shows current stream state (blank if no data coming in)
	// 2. No "replay from scratch" behavior
	// 3. True real-time experience like a live webcam
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
