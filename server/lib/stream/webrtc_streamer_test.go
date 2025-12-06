package stream

import (
	"context"
	"testing"

	"github.com/onkernel/kernel-images/server/lib/scaletozero"
	"github.com/stretchr/testify/require"
)

func TestWebRTCStreamerMetadataAndHandleOfferGuard(t *testing.T) {
	t.Parallel()

	fr := 30
	display := 0
	streamer, err := NewWebRTCStreamer("webrtc", Params{FrameRate: &fr, DisplayNum: &display, Mode: ModeWebRTC}, "ffmpeg", scaletozero.NewNoopController())
	require.NoError(t, err)

	meta := streamer.Metadata()
	require.Equal(t, ModeWebRTC, meta.Mode)
	require.NotNil(t, meta.WebRTCOfferURL)
	require.Equal(t, "/stream/webrtc/offer", *meta.WebRTCOfferURL)

	_, err = streamer.HandleOffer(context.Background(), "bad")
	require.ErrorContains(t, err, "webrtc stream not ready")
}
