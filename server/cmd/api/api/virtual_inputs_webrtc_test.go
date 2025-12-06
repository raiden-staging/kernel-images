package api

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

func TestNegotiateVirtualInputsWebrtc(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("missing offer sdp returns 400", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		resp, err := svc.NegotiateVirtualInputsWebrtc(ctx, oapi.NegotiateVirtualInputsWebrtcRequestObject{})
		require.NoError(t, err)
		_, ok := resp.(oapi.NegotiateVirtualInputsWebrtc400JSONResponse)
		require.True(t, ok)
	})

	t.Run("ingest not configured returns 409", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		body := oapi.VirtualInputWebRTCOffer{Sdp: "offer"}
		resp, err := svc.NegotiateVirtualInputsWebrtc(ctx, oapi.NegotiateVirtualInputsWebrtcRequestObject{Body: &body})
		require.NoError(t, err)
		_, ok := resp.(oapi.NegotiateVirtualInputsWebrtc409JSONResponse)
		require.True(t, ok)
	})

	t.Run("invalid sdp returns 500 when ingest configured", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, vimgr := newTestApiService(t, mgr)

		pipeDir := t.TempDir()
		videoPipe := filepath.Join(pipeDir, "video.pipe")
		require.NoError(t, syscall.Mkfifo(videoPipe, 0o666))

		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: "webrtc", Path: videoPipe, Format: "ivf"},
		}
		svc.virtualInputsWebRTC.Configure(videoPipe, "ivf", "", "")

		body := oapi.VirtualInputWebRTCOffer{Sdp: "bad-sdp"}
		resp, err := svc.NegotiateVirtualInputsWebrtc(ctx, oapi.NegotiateVirtualInputsWebrtcRequestObject{Body: &body})
		require.NoError(t, err)
		_, ok := resp.(oapi.NegotiateVirtualInputsWebrtc500JSONResponse)
		require.True(t, ok)
	})

	t.Run("successful negotiation returns answer", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, vimgr := newTestApiService(t, mgr)

		pipeDir := t.TempDir()
		videoPipe := filepath.Join(pipeDir, "video.pipe")
		require.NoError(t, syscall.Mkfifo(videoPipe, 0o666))

		vimgr.status.Ingest = &virtualinputs.IngestStatus{
			Video: &virtualinputs.IngestEndpoint{Protocol: "webrtc", Path: videoPipe, Format: "ivf"},
		}
		svc.virtualInputsWebRTC.Configure(videoPipe, "ivf", "", "")

		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		require.NoError(t, err)
		defer pc.Close()

		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo)
		require.NoError(t, err)

		offer, err := pc.CreateOffer(nil)
		require.NoError(t, err)
		require.NoError(t, pc.SetLocalDescription(offer))
		<-webrtc.GatheringCompletePromise(pc)

		body := oapi.VirtualInputWebRTCOffer{Sdp: pc.LocalDescription().SDP}
		resp, err := svc.NegotiateVirtualInputsWebrtc(ctx, oapi.NegotiateVirtualInputsWebrtcRequestObject{Body: &body})
		require.NoError(t, err)
		out, ok := resp.(oapi.NegotiateVirtualInputsWebrtc200JSONResponse)
		require.True(t, ok)
		require.NotNil(t, out.Sdp)
		require.NotEmpty(t, *out.Sdp)
	})
}
