package virtualinputs

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

func TestWebRTCIngestorHandleOfferRequiresConfig(t *testing.T) {
	t.Parallel()

	ing := NewWebRTCIngestor()
	_, err := ing.HandleOffer(context.Background(), "dummy")
	require.ErrorContains(t, err, "webrtc ingest not configured")
}

func TestWebRTCIngestorHandleOfferRequiresPaths(t *testing.T) {
	t.Parallel()

	ing := NewWebRTCIngestor()
	ing.Configure("", "", "", "")
	_, err := ing.HandleOffer(context.Background(), "dummy")
	require.ErrorContains(t, err, "webrtc ingest paths not configured")
}

func TestWebRTCIngestorHandleOfferNegotiates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	videoPipe := filepath.Join(dir, "video.pipe")
	require.NoError(t, syscall.Mkfifo(videoPipe, 0o666))

	ing := NewWebRTCIngestor()
	ing.Configure(videoPipe, "ivf", "", "")
	t.Cleanup(func() { ing.Clear() })

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	defer pc.Close()

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo)
	require.NoError(t, err)

	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))
	<-webrtc.GatheringCompletePromise(pc)

	answer, err := ing.HandleOffer(context.Background(), pc.LocalDescription().SDP)
	require.NoError(t, err)
	require.NotEmpty(t, answer)

	require.NoError(t, pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}))
}
