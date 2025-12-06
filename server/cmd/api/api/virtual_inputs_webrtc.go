package api

import (
	"context"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

// NegotiateVirtualInputsWebrtc handles SDP offer/answer exchange for realtime ingest.
func (s *ApiService) NegotiateVirtualInputsWebrtc(ctx context.Context, req oapi.NegotiateVirtualInputsWebrtcRequestObject) (oapi.NegotiateVirtualInputsWebrtcResponseObject, error) {
	log := logger.FromContext(ctx)
	if req.Body == nil || req.Body.Sdp == "" {
		return oapi.NegotiateVirtualInputsWebrtc400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "sdp is required"},
		}, nil
	}

	status := s.virtualInputs.Status(ctx)
	if status.Ingest == nil || ((status.Ingest.Video == nil || status.Ingest.Video.Protocol != "webrtc") &&
		(status.Ingest.Audio == nil || status.Ingest.Audio.Protocol != "webrtc")) {
		return oapi.NegotiateVirtualInputsWebrtc409JSONResponse{
			ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{Message: "virtual input not configured for webrtc ingest"},
		}, nil
	}

	answer, err := s.virtualInputsWebRTC.HandleOffer(ctx, req.Body.Sdp)
	if err != nil {
		log.Error("failed to negotiate virtual input webrtc", "err", err)
		return oapi.NegotiateVirtualInputsWebrtc500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to negotiate webrtc ingest"},
		}, nil
	}

	return oapi.NegotiateVirtualInputsWebrtc200JSONResponse{
		VirtualInputWebRTCAnswer: oapi.VirtualInputWebRTCAnswer{Sdp: &answer},
	}, nil
}
