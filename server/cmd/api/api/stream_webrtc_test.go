package api

import (
	"context"
	"errors"
	"testing"

	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/stream"
	"github.com/stretchr/testify/require"
)

func TestStreamWebrtcOfferEndpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("not found when no stream registered", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		req := oapi.StreamWebrtcOfferRequestObject{Body: &oapi.StreamWebRTCOffer{Sdp: "offer"}}
		resp, err := svc.StreamWebrtcOffer(ctx, req)
		require.NoError(t, err)
		_, ok := resp.(oapi.StreamWebrtcOffer404JSONResponse)
		require.True(t, ok, "expected 404 when stream missing")
	})

	t.Run("conflict when stream does not support webrtc", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		streamer := &mockStreamEndpoint{
			id: "default",
			meta: stream.Metadata{
				ID:   "default",
				Mode: stream.ModeInternal,
			},
		}
		require.NoError(t, svc.streamManager.RegisterStream(ctx, streamer))

		req := oapi.StreamWebrtcOfferRequestObject{Body: &oapi.StreamWebRTCOffer{Sdp: "offer"}}
		resp, err := svc.StreamWebrtcOffer(ctx, req)
		require.NoError(t, err)
		_, ok := resp.(oapi.StreamWebrtcOffer409JSONResponse)
		require.True(t, ok, "expected 409 for non-webrtc stream")
	})

	t.Run("negotiation error surfaces as 500", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		streamer := &mockWebRTCStream{
			mockStreamEndpoint: mockStreamEndpoint{
				id: "default",
				meta: stream.Metadata{
					ID:   "default",
					Mode: stream.ModeWebRTC,
				},
			},
			err: errors.New("negotiate boom"),
		}
		require.NoError(t, svc.streamManager.RegisterStream(ctx, streamer))

		req := oapi.StreamWebrtcOfferRequestObject{Body: &oapi.StreamWebRTCOffer{Sdp: "offer"}}
		resp, err := svc.StreamWebrtcOffer(ctx, req)
		require.NoError(t, err)
		_, ok := resp.(oapi.StreamWebrtcOffer500JSONResponse)
		require.True(t, ok, "expected 500 on negotiation failure")
	})

	t.Run("successful negotiation returns answer", func(t *testing.T) {
		mgr := recorderManagerWithDefault(t)
		svc, _ := newTestApiService(t, mgr)

		streamer := &mockWebRTCStream{
			mockStreamEndpoint: mockStreamEndpoint{
				id: "default",
				meta: stream.Metadata{
					ID:   "default",
					Mode: stream.ModeWebRTC,
				},
			},
			answer: "answer-sdp",
		}
		require.NoError(t, svc.streamManager.RegisterStream(ctx, streamer))

		req := oapi.StreamWebrtcOfferRequestObject{Body: &oapi.StreamWebRTCOffer{Sdp: "offer"}}
		resp, err := svc.StreamWebrtcOffer(ctx, req)
		require.NoError(t, err)
		out, ok := resp.(oapi.StreamWebrtcOffer200JSONResponse)
		require.True(t, ok, "expected 200 response")
		require.NotNil(t, out.Sdp)
		require.Equal(t, "answer-sdp", *out.Sdp)
	})
}

type mockStreamEndpoint struct {
	id    string
	meta  stream.Metadata
	start bool
}

func (m *mockStreamEndpoint) ID() string                           { return m.id }
func (m *mockStreamEndpoint) Start(ctx context.Context) error      { m.start = true; return nil }
func (m *mockStreamEndpoint) Stop(ctx context.Context) error       { m.start = false; return nil }
func (m *mockStreamEndpoint) IsStreaming(ctx context.Context) bool { return m.start }
func (m *mockStreamEndpoint) Metadata() stream.Metadata            { return m.meta }

type mockWebRTCStream struct {
	mockStreamEndpoint
	answer string
	err    error
}

func (m *mockWebRTCStream) HandleOffer(ctx context.Context, offer string) (string, error) {
	return m.answer, m.err
}

func recorderManagerWithDefault(t *testing.T) recorder.RecordManager {
	t.Helper()
	return recorder.NewFFmpegManager()
}
