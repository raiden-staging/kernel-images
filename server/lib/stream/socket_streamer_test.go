package stream

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
	"github.com/stretchr/testify/require"
)

func TestSocketStreamerMetadataAndRegisterClient(t *testing.T) {
	t.Parallel()

	fr := 30
	display := 0
	streamer, err := NewSocketStreamer("sock", Params{FrameRate: &fr, DisplayNum: &display, Mode: ModeSocket}, "ffmpeg", scaletozero.NewNoopController())
	require.NoError(t, err)

	meta := streamer.Metadata()
	require.Equal(t, ModeSocket, meta.Mode)
	require.NotNil(t, meta.WebsocketURL)
	require.Equal(t, "/stream/socket/sock", *meta.WebsocketURL)

	conn := &mockWebSocketConn{}
	require.NoError(t, streamer.RegisterClient(conn))
	require.Len(t, conn.writes, 1)
	require.Equal(t, websocket.MessageText, conn.writes[0].messageType)
	require.Equal(t, "mpegts", string(conn.writes[0].data))
}

type wsWrite struct {
	messageType websocket.MessageType
	data        []byte
}

type mockWebSocketConn struct {
	writes []wsWrite
}

func (m *mockWebSocketConn) Read(ctx context.Context) (int, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return 0, nil, ctx.Err()
	}
}

func (m *mockWebSocketConn) Write(ctx context.Context, messageType int, data []byte) error {
	m.writes = append(m.writes, wsWrite{messageType: websocket.MessageType(messageType), data: data})
	return nil
}

func (m *mockWebSocketConn) Close(status int, reason string) error {
	return nil
}
