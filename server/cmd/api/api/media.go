package api

import (
	"context"
	"fmt"

	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/mediastreamer"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

// StartMediaInput starts streaming media to virtual input devices
func (s *ApiService) StartMediaInput(ctx context.Context, req oapi.StartMediaInputRequestObject) (oapi.StartMediaInputResponseObject, error) {
	log := logger.FromContext(ctx)

	if req.Body == nil {
		log.Error("missing request body")
		return oapi.StartMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "request body is required",
			},
		}, nil
	}

	// Check if already streaming
	if s.mediaStreamer.IsActive() {
		log.Error("attempted to start media input while one is already active")
		return oapi.StartMediaInput409JSONResponse{
			ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{
				Message: "a media input stream is already active",
			},
		}, nil
	}

	// Validate media type
	var mediaType mediastreamer.MediaType
	switch req.Body.MediaType {
	case oapi.StartMediaInputRequestMediaTypeVideo:
		mediaType = mediastreamer.MediaTypeVideo
	case oapi.StartMediaInputRequestMediaTypeAudio:
		mediaType = mediastreamer.MediaTypeAudio
	case oapi.StartMediaInputRequestMediaTypeBoth:
		mediaType = mediastreamer.MediaTypeBoth
	default:
		log.Error("invalid media type", "type", req.Body.MediaType)
		return oapi.StartMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: fmt.Sprintf("invalid media type: %s", req.Body.MediaType),
			},
		}, nil
	}

	// Build stream configuration
	config := mediastreamer.StreamConfig{
		URL:       req.Body.Url,
		MediaType: mediaType,
		Loop:      req.Body.Loop != nil && *req.Body.Loop,
	}

	// Use custom video device if specified
	if req.Body.VideoDevice != nil && *req.Body.VideoDevice != "" {
		config.VideoDevice = *req.Body.VideoDevice
	}

	// Start streaming
	if err := s.mediaStreamer.Start(ctx, config); err != nil {
		log.Error("failed to start media input", "err", err, "url", req.Body.Url)
		return oapi.StartMediaInput500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to start media input: %v", err),
			},
		}, nil
	}

	log.Info("media input started", "url", req.Body.Url, "type", mediaType)
	return oapi.StartMediaInput201Response{}, nil
}

// StopMediaInput stops the active media input stream
func (s *ApiService) StopMediaInput(ctx context.Context, req oapi.StopMediaInputRequestObject) (oapi.StopMediaInputResponseObject, error) {
	log := logger.FromContext(ctx)

	if !s.mediaStreamer.IsActive() {
		log.Warn("attempted to stop media input when none is active")
		// Return success anyway for idempotency
		return oapi.StopMediaInput200Response{}, nil
	}

	if err := s.mediaStreamer.Stop(ctx); err != nil {
		log.Error("failed to stop media input", "err", err)
		return oapi.StopMediaInput500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to stop media input: %v", err),
			},
		}, nil
	}

	log.Info("media input stopped")
	return oapi.StopMediaInput200Response{}, nil
}

// PauseMediaInput pauses the active media input stream
func (s *ApiService) PauseMediaInput(ctx context.Context, req oapi.PauseMediaInputRequestObject) (oapi.PauseMediaInputResponseObject, error) {
	log := logger.FromContext(ctx)

	if !s.mediaStreamer.IsActive() {
		log.Error("attempted to pause media input when none is active")
		return oapi.PauseMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "no active media input to pause",
			},
		}, nil
	}

	if err := s.mediaStreamer.Pause(ctx); err != nil {
		log.Error("failed to pause media input", "err", err)
		return oapi.PauseMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: fmt.Sprintf("failed to pause media input: %v", err),
			},
		}, nil
	}

	log.Info("media input paused")
	return oapi.PauseMediaInput200Response{}, nil
}

// ResumeMediaInput resumes a paused media input stream
func (s *ApiService) ResumeMediaInput(ctx context.Context, req oapi.ResumeMediaInputRequestObject) (oapi.ResumeMediaInputResponseObject, error) {
	log := logger.FromContext(ctx)

	status := s.mediaStreamer.Status(ctx)
	if status.State != mediastreamer.StatePaused {
		log.Error("attempted to resume media input when not paused", "state", status.State)
		return oapi.ResumeMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "media input is not paused",
			},
		}, nil
	}

	if err := s.mediaStreamer.Resume(ctx); err != nil {
		log.Error("failed to resume media input", "err", err)
		return oapi.ResumeMediaInput400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: fmt.Sprintf("failed to resume media input: %v", err),
			},
		}, nil
	}

	log.Info("media input resumed")
	return oapi.ResumeMediaInput200Response{}, nil
}

// GetMediaInputStatus returns the current status of virtual media input
func (s *ApiService) GetMediaInputStatus(ctx context.Context, req oapi.GetMediaInputStatusRequestObject) (oapi.GetMediaInputStatusResponseObject, error) {
	status := s.mediaStreamer.Status(ctx)

	// Convert internal state to API state
	var apiState oapi.MediaInputStatusState
	switch status.State {
	case mediastreamer.StateStopped:
		apiState = oapi.Stopped
	case mediastreamer.StatePlaying:
		apiState = oapi.Playing
	case mediastreamer.StatePaused:
		apiState = oapi.Paused
	default:
		apiState = oapi.Stopped
	}

	response := oapi.MediaInputStatus{
		Active: status.Active,
		State:  apiState,
	}

	// Include additional details if active
	if status.Active {
		response.Url = &status.URL
		response.Loop = &status.Loop
		response.StartedAt = &status.StartedAt

		// Convert media type
		var apiMediaType oapi.MediaInputStatusMediaType
		switch status.MediaType {
		case mediastreamer.MediaTypeVideo:
			apiMediaType = oapi.MediaInputStatusMediaTypeVideo
		case mediastreamer.MediaTypeAudio:
			apiMediaType = oapi.MediaInputStatusMediaTypeAudio
		case mediastreamer.MediaTypeBoth:
			apiMediaType = oapi.MediaInputStatusMediaTypeBoth
		}
		response.MediaType = &apiMediaType
	}

	return oapi.GetMediaInputStatus200JSONResponse(response), nil
}
