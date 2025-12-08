package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

func readFeedBody(t *testing.T, resp oapi.GetVirtualInputFeedResponseObject) string {
	t.Helper()
	out, ok := resp.(oapi.GetVirtualInputFeed200TexthtmlResponse)
	require.True(t, ok, "unexpected response type %T", resp)
	data, err := io.ReadAll(out.Body)
	require.NoError(t, err)
	return string(data)
}

func TestGetVirtualInputFeed_AutoDetectsSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mgr := recorder.NewFFmpegManager()

	t.Run("socket ingests default to websocket feed and mpegts format", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, mgr)
		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: string(virtualinputs.SourceTypeSocket), Path: "/tmp/video.pipe"},
		}

		resp, err := svc.GetVirtualInputFeed(ctx, oapi.GetVirtualInputFeedRequestObject{Params: oapi.GetVirtualInputFeedParams{}})
		require.NoError(t, err)
		body := readFeedBody(t, resp)

		require.Contains(t, body, "/input/devices/virtual/feed/socket")
		require.Contains(t, body, `const defaultFormat = "mpegts";`)
	})

	t.Run("webrtc ingests default to ivf websocket preview", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, mgr)
		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: string(virtualinputs.SourceTypeWebRTC), Path: "/tmp/webrtc.pipe"},
		}

		resp, err := svc.GetVirtualInputFeed(ctx, oapi.GetVirtualInputFeedRequestObject{Params: oapi.GetVirtualInputFeedParams{}})
		require.NoError(t, err)
		body := readFeedBody(t, resp)

		require.Contains(t, body, "/input/devices/virtual/feed/socket")
		require.Contains(t, body, `const defaultFormat = "ivf";`)
	})

	t.Run("falls back to configured video url when no ingest", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, mgr)
		vimgr.status.Ingest = nil
		vimgr.status.Video = &virtualinputs.MediaSource{URL: "https://example.com/feed.m3u8"}

		resp, err := svc.GetVirtualInputFeed(ctx, oapi.GetVirtualInputFeedRequestObject{Params: oapi.GetVirtualInputFeedParams{}})
		require.NoError(t, err)
		body := readFeedBody(t, resp)

		require.Contains(t, body, `const defaultSource = "https://example.com/feed.m3u8";`)
	})

	t.Run("source query param overrides detection", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, mgr)
		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: string(virtualinputs.SourceTypeSocket), Path: "/tmp/video.pipe"},
		}
		override := "https://override.local/video"

		resp, err := svc.GetVirtualInputFeed(ctx, oapi.GetVirtualInputFeedRequestObject{
			Params: oapi.GetVirtualInputFeedParams{Source: &override},
		})
		require.NoError(t, err)
		body := readFeedBody(t, resp)

		require.Contains(t, body, `const defaultSource = "https://override.local/video";`)
	})
}

func TestVirtualFeedPageUsesWebsocketURLs(t *testing.T) {
	t.Parallel()

	svc, _ := newTestApiService(t, recorder.NewFFmpegManager())
	resp, err := svc.GetVirtualInputFeed(context.Background(), oapi.GetVirtualInputFeedRequestObject{
		Params: oapi.GetVirtualInputFeedParams{},
	})
	require.NoError(t, err)
	body := readFeedBody(t, resp)

	require.Contains(t, body, "function toWebSocketURL")
	require.Contains(t, body, "new JSMpeg.Player(toWebSocketURL")
	require.Contains(t, body, "new WebSocket(toWebSocketURL")
}

func TestVirtualFeedSocketBroadcasts(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	svc, _ := newTestApiService(t, recorder.NewFFmpegManager())
	svc.virtualFeed.setFormat("ivf")

	server := httptest.NewServer(http.HandlerFunc(svc.HandleVirtualInputFeedSocket))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	msgType, msg, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageText, msgType)
	require.Equal(t, "ivf", string(msg))

	payload := []byte{0x01, 0x02, 0x03, 0x04}
	svc.virtualFeed.broadcastWithFormat("ivf", payload)

	msgType, msg, err = conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageBinary, msgType)
	require.Equal(t, payload, msg)
}

func TestGetVirtualInputFeedSocketInfo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns 409 when no ingest is active", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, recorder.NewFFmpegManager())
		vimgr.status.Ingest = nil

		resp, err := svc.GetVirtualInputFeedSocketInfo(ctx, oapi.GetVirtualInputFeedSocketInfoRequestObject{})
		require.NoError(t, err)
		_, ok := resp.(oapi.GetVirtualInputFeedSocketInfo409JSONResponse)
		require.True(t, ok, "expected 409 when no ingest")
	})

	t.Run("reports websocket URL and mpegts for socket ingest", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, recorder.NewFFmpegManager())
		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: string(virtualinputs.SourceTypeSocket)},
		}

		resp, err := svc.GetVirtualInputFeedSocketInfo(ctx, oapi.GetVirtualInputFeedSocketInfoRequestObject{})
		require.NoError(t, err)
		out, ok := resp.(oapi.GetVirtualInputFeedSocketInfo200JSONResponse)
		require.True(t, ok, "expected 200 response")
		require.Equal(t, "/input/devices/virtual/feed/socket", out.Url)
		require.NotNil(t, out.Format)
		require.Equal(t, "mpegts", *out.Format)
	})

	t.Run("reports ivf for webrtc ingest", func(t *testing.T) {
		svc, vimgr := newTestApiService(t, recorder.NewFFmpegManager())
		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: string(virtualinputs.SourceTypeWebRTC), Format: "ivf"},
		}

		resp, err := svc.GetVirtualInputFeedSocketInfo(ctx, oapi.GetVirtualInputFeedSocketInfoRequestObject{})
		require.NoError(t, err)
		out, ok := resp.(oapi.GetVirtualInputFeedSocketInfo200JSONResponse)
		require.True(t, ok, "expected 200 response")
		require.Equal(t, "/input/devices/virtual/feed/socket", out.Url)
		require.NotNil(t, out.Format)
		require.Equal(t, "ivf", *out.Format)
	})
}
