package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

func TestVirtualInputVideoSocketMirrorsFeed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dir := t.TempDir()
	pipePath := filepath.Join(dir, "video.pipe")
	require.NoError(t, syscall.Mkfifo(pipePath, 0o666))

	readerReady := make(chan struct{})
	go func() {
		defer close(readerReady)
		f, err := openPipeReader(pipePath)
		if err != nil {
			t.Errorf("open pipe reader: %v", err)
			return
		}
		defer f.Close()
		_, _ = io.Copy(io.Discard, f)
	}()
	select {
	case <-readerReady:
	case <-time.After(2 * time.Second):
		t.Fatal("pipe reader did not open in time")
	}

	svc, vimgr := newTestApiService(t, recorder.NewFFmpegManager())
	vimgr.status.Ingest = &virtualinputs.IngestStatus{
		Video: &virtualinputs.IngestEndpoint{
			Protocol: string(virtualinputs.SourceTypeSocket),
			Format:   "mpegts",
			Path:     pipePath,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/input/devices/virtual/socket/video", svc.HandleVirtualInputVideoSocket)
	mux.HandleFunc("/input/devices/virtual/feed/socket", svc.HandleVirtualInputFeedSocket)
	server := httptest.NewServer(mux)
	defer server.Close()

	feedURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/input/devices/virtual/feed/socket"
	feedConn, _, err := websocket.Dial(ctx, feedURL, nil)
	require.NoError(t, err)
	defer feedConn.Close(websocket.StatusNormalClosure, "")

	ingestURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/input/devices/virtual/socket/video"
	ingestConn, _, err := websocket.Dial(ctx, ingestURL, nil)
	require.NoError(t, err)
	defer ingestConn.Close(websocket.StatusNormalClosure, "")

	payload := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 8*1024)
	require.NoError(t, ingestConn.Write(ctx, websocket.MessageBinary, payload))

	msgType, msg, err := feedConn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageBinary, msgType)
	require.Equal(t, payload, msg)
}

// openPipeReader opens the read end of a FIFO without blocking forever.
func openPipeReader(path string) (*os.File, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = syscall.SetNonblock(int(f.Fd()), false)
			return f, nil
		}
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.EAGAIN) {
			if time.Now().After(deadline) {
				return nil, err
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return nil, err
	}
}
