package api

import (
	"context"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/stream"
)

// StreamWebrtcOffer exchanges SDP for an active WebRTC livestream.
func (s *ApiService) StreamWebrtcOffer(ctx context.Context, req oapi.StreamWebrtcOfferRequestObject) (oapi.StreamWebrtcOfferResponseObject, error) {
	log := logger.FromContext(ctx)
	if req.Body == nil || req.Body.Sdp == "" {
		return oapi.StreamWebrtcOffer400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "sdp is required"},
		}, nil
	}
	streamID := s.defaultStreamID
	if req.Body.Id != nil && *req.Body.Id != "" {
		streamID = *req.Body.Id
	}

	st, ok := s.streamManager.GetStream(streamID)
	if !ok {
		return oapi.StreamWebrtcOffer404JSONResponse{
			NotFoundErrorJSONResponse: oapi.NotFoundErrorJSONResponse{Message: "stream not found"},
		}, nil
	}

	negotiator, ok := st.(stream.WebRTCNegotiator)
	if !ok {
		return oapi.StreamWebrtcOffer409JSONResponse{
			ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{Message: "stream does not support webrtc"},
		}, nil
	}

	answer, err := negotiator.HandleOffer(ctx, req.Body.Sdp)
	if err != nil {
		log.Error("failed to negotiate webrtc stream", "err", err, "stream_id", streamID)
		return oapi.StreamWebrtcOffer500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to negotiate stream"},
		}, nil
	}

	return oapi.StreamWebrtcOffer200JSONResponse(oapi.StreamWebRTCAnswer{Sdp: &answer}), nil
}
