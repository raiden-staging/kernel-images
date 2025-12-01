package streamer

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/onkernel/kernel-images/server/lib/scaletozero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultStreamParams() FFmpegStreamParams {
	fr := 5
	disp := 0
	return FFmpegStreamParams{
		FrameRate:  &fr,
		DisplayNum: &disp,
		Protocol:   StreamProtocolRTMP,
		Mode:       StreamModeInternal,
		ListenHost: "127.0.0.1",
		ListenPort: 1935,
		App:        "live",
	}
}

func TestFFmpegStreamer_StartAndStop(t *testing.T) {
	mockBin := filepath.Join("..", "recorder", "testdata", "mock_ffmpeg.sh")
	factory := NewFFmpegStreamerFactory(mockBin, defaultStreamParams(), scaletozero.NewNoopController())

	stream, err := factory("stream-1", FFmpegStreamOverrides{})
	require.NoError(t, err)

	require.NoError(t, stream.Start(t.Context()))
	require.True(t, stream.IsStreaming(t.Context()))

	meta := stream.Metadata()
	assert.Equal(t, "rtmp://127.0.0.1:1935/live/stream-1", meta.URL)
	assert.Equal(t, "/live/stream-1", meta.RelativeURL)

	time.Sleep(25 * time.Millisecond)

	err = stream.Stop(t.Context())
	require.Error(t, err)
	assert.False(t, stream.IsStreaming(t.Context()))
}

func TestFFmpegStreamManager_RegisterAndStopAll(t *testing.T) {
	ctx := t.Context()
	mockBin := filepath.Join("..", "recorder", "testdata", "mock_ffmpeg.sh")
	factory := NewFFmpegStreamerFactory(mockBin, defaultStreamParams(), scaletozero.NewNoopController())

	manager := NewFFmpegStreamManager()
	stream, err := factory("stream-2", FFmpegStreamOverrides{})
	require.NoError(t, err)

	require.NoError(t, manager.RegisterStream(ctx, stream))
	require.Len(t, manager.streams, 1)

	require.NoError(t, stream.Start(ctx))
	require.True(t, stream.IsStreaming(ctx))

	err = manager.StopAll(ctx)
	require.Error(t, err)
	assert.Len(t, manager.streams, 0)
}
