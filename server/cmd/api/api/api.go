package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/onkernel/kernel-images/server/lib/devtoolsproxy"
	"github.com/onkernel/kernel-images/server/lib/logger"
	"github.com/onkernel/kernel-images/server/lib/nekoclient"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/recorder"
	"github.com/onkernel/kernel-images/server/lib/scaletozero"
	"github.com/onkernel/kernel-images/server/lib/streamer"
)

type ApiService struct {
	// defaultRecorderID is used whenever the caller doesn't specify an explicit ID.
	defaultRecorderID string
	defaultStreamID   string

	recordManager recorder.RecordManager
	factory       recorder.FFmpegRecorderFactory
	streamManager streamer.StreamManager
	streamFactory streamer.FFmpegStreamerFactory
	// Filesystem watch management
	watchMu sync.RWMutex
	watches map[string]*fsWatch

	// Process management
	procMu sync.RWMutex
	procs  map[string]*processHandle

	// Neko authenticated client
	nekoAuthClient *nekoclient.AuthClient

	// DevTools upstream manager (Chromium supervisord log tailer)
	upstreamMgr *devtoolsproxy.UpstreamManager
	stz         scaletozero.Controller

	// inputMu serializes input-related operations (mouse, keyboard, screenshot)
	inputMu sync.Mutex

	// playwrightMu serializes Playwright code execution (only one execution at a time)
	playwrightMu sync.Mutex
}

var _ oapi.StrictServerInterface = (*ApiService)(nil)

func New(recordManager recorder.RecordManager, factory recorder.FFmpegRecorderFactory, streamManager streamer.StreamManager, streamFactory streamer.FFmpegStreamerFactory, upstreamMgr *devtoolsproxy.UpstreamManager, stz scaletozero.Controller, nekoAuthClient *nekoclient.AuthClient) (*ApiService, error) {
	switch {
	case recordManager == nil:
		return nil, fmt.Errorf("recordManager cannot be nil")
	case factory == nil:
		return nil, fmt.Errorf("factory cannot be nil")
	case streamManager == nil:
		return nil, fmt.Errorf("streamManager cannot be nil")
	case streamFactory == nil:
		return nil, fmt.Errorf("streamFactory cannot be nil")
	case upstreamMgr == nil:
		return nil, fmt.Errorf("upstreamMgr cannot be nil")
	case nekoAuthClient == nil:
		return nil, fmt.Errorf("nekoAuthClient cannot be nil")
	}

	return &ApiService{
		recordManager:     recordManager,
		factory:           factory,
		defaultRecorderID: "default",
		defaultStreamID:   "default-stream",
		streamManager:     streamManager,
		streamFactory:     streamFactory,
		watches:           make(map[string]*fsWatch),
		procs:             make(map[string]*processHandle),
		upstreamMgr:       upstreamMgr,
		stz:               stz,
		nekoAuthClient:    nekoAuthClient,
	}, nil
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func nilIfEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return ptrOf(value)
}

func (s *ApiService) StartRecording(ctx context.Context, req oapi.StartRecordingRequestObject) (oapi.StartRecordingResponseObject, error) {
	log := logger.FromContext(ctx)

	var params recorder.FFmpegRecordingParams
	if req.Body != nil {
		params.FrameRate = req.Body.Framerate
		params.MaxSizeInMB = req.Body.MaxFileSizeInMB
		params.MaxDurationInSeconds = req.Body.MaxDurationInSeconds
	}

	// Determine recorder ID (use default if none provided)
	recorderID := s.defaultRecorderID
	if req.Body != nil && req.Body.Id != nil && *req.Body.Id != "" {
		recorderID = *req.Body.Id
	}

	// Create, register, and start a new recorder
	rec, err := s.factory(recorderID, params)
	if err != nil {
		log.Error("failed to create recorder", "err", err, "recorder_id", recorderID)
		return oapi.StartRecording500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to create recording"}}, nil
	}
	if err := s.recordManager.RegisterRecorder(ctx, rec); err != nil {
		if rec, exists := s.recordManager.GetRecorder(recorderID); exists {
			if rec.IsRecording(ctx) {
				log.Error("attempted to start recording while one is already active", "recorder_id", recorderID)
				return oapi.StartRecording409JSONResponse{ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{Message: "recording already in progress"}}, nil
			} else {
				log.Error("attempted to restart recording", "recorder_id", recorderID)
				return oapi.StartRecording400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "recording already completed"}}, nil
			}
		}
		log.Error("failed to register recorder", "err", err, "recorder_id", recorderID)
		return oapi.StartRecording500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to register recording"}}, nil
	}

	if err := rec.Start(ctx); err != nil {
		log.Error("failed to start recording", "err", err, "recorder_id", recorderID)
		// ensure the recorder is deregistered
		defer s.recordManager.DeregisterRecorder(ctx, rec)
		return oapi.StartRecording500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to start recording"}}, nil
	}

	return oapi.StartRecording201Response{}, nil
}

func (s *ApiService) StopRecording(ctx context.Context, req oapi.StopRecordingRequestObject) (oapi.StopRecordingResponseObject, error) {
	log := logger.FromContext(ctx)

	// Determine recorder ID
	recorderID := s.defaultRecorderID
	if req.Body != nil && req.Body.Id != nil && *req.Body.Id != "" {
		recorderID = *req.Body.Id
	}

	rec, exists := s.recordManager.GetRecorder(recorderID)
	if !exists {
		log.Error("attempted to stop recording when none is active", "recorder_id", recorderID)
		return oapi.StopRecording400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "no active recording to stop"}}, nil
	} else if !rec.IsRecording(ctx) {
		log.Warn("recording already stopped", "recorder_id", recorderID)
		return oapi.StopRecording200Response{}, nil
	}

	// Check if force stop is requested
	forceStop := false
	if req.Body != nil && req.Body.ForceStop != nil {
		forceStop = *req.Body.ForceStop
	}

	var err error
	if forceStop {
		log.Info("force stopping recording", "recorder_id", recorderID)
		err = rec.ForceStop(ctx)
	} else {
		log.Info("gracefully stopping recording", "recorder_id", recorderID)
		err = rec.Stop(ctx)
	}

	if err != nil {
		log.Error("error occurred while stopping recording", "err", err, "force", forceStop, "recorder_id", recorderID)
	}

	return oapi.StopRecording200Response{}, nil
}

const (
	minRecordingSizeInBytes = 100
)

func (s *ApiService) DownloadRecording(ctx context.Context, req oapi.DownloadRecordingRequestObject) (oapi.DownloadRecordingResponseObject, error) {
	log := logger.FromContext(ctx)

	// Determine recorder ID
	recorderID := s.defaultRecorderID
	if req.Params.Id != nil && *req.Params.Id != "" {
		recorderID = *req.Params.Id
	}

	// Get the recorder to access its output path
	rec, exists := s.recordManager.GetRecorder(recorderID)
	if !exists {
		log.Error("attempted to download non-existent recording", "recorder_id", recorderID)
		return oapi.DownloadRecording404JSONResponse{NotFoundErrorJSONResponse: oapi.NotFoundErrorJSONResponse{Message: "no recording found"}}, nil
	}
	if rec.IsDeleted(ctx) {
		log.Error("attempted to download deleted recording", "recorder_id", recorderID)
		return oapi.DownloadRecording400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "requested recording has been deleted"}}, nil
	}

	out, meta, err := rec.Recording(ctx)
	if err != nil {
		log.Error("failed to get recording", "err", err, "recorder_id", recorderID)
		return oapi.DownloadRecording500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to get recording"}}, nil
	}

	// short-circuit if the recording is still in progress and the file is arbitrary small
	if rec.IsRecording(ctx) && meta.Size <= minRecordingSizeInBytes {
		return oapi.DownloadRecording202Response{
			Headers: oapi.DownloadRecording202ResponseHeaders{
				RetryAfter: 300,
			},
		}, nil
	}

	log.Info("serving recording file for download", "size", meta.Size, "recorder_id", recorderID)
	return oapi.DownloadRecording200Videomp4Response{
		Body: out,
		Headers: oapi.DownloadRecording200ResponseHeaders{
			XRecordingStartedAt:  meta.StartTime.Format(time.RFC3339),
			XRecordingFinishedAt: meta.EndTime.Format(time.RFC3339),
		},
		ContentLength: meta.Size,
	}, nil
}

func (s *ApiService) DeleteRecording(ctx context.Context, req oapi.DeleteRecordingRequestObject) (oapi.DeleteRecordingResponseObject, error) {
	log := logger.FromContext(ctx)

	recorderID := s.defaultRecorderID
	if req.Body != nil && req.Body.Id != nil && *req.Body.Id != "" {
		recorderID = *req.Body.Id
	}
	rec, exists := s.recordManager.GetRecorder(recorderID)
	if !exists {
		log.Error("attempted to delete non-existent recording", "recorder_id", recorderID)
		return oapi.DeleteRecording404JSONResponse{NotFoundErrorJSONResponse: oapi.NotFoundErrorJSONResponse{Message: "no recording found"}}, nil
	}

	if rec.IsRecording(ctx) {
		log.Error("attempted to delete recording while still in progress", "recorder_id", recorderID)
		return oapi.DeleteRecording400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "recording must be stopped first"}}, nil
	}

	// fine to do this async
	go func() {
		if err := rec.Delete(context.Background()); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Error("failed to delete recording", "err", err, "recorder_id", recorderID)
		} else {
			log.Info("recording deleted", "recorder_id", recorderID)
		}
	}()

	return oapi.DeleteRecording200Response{}, nil
}

// ListRecorders returns a list of all registered recorders and whether each one is currently recording.
func (s *ApiService) ListRecorders(ctx context.Context, _ oapi.ListRecordersRequestObject) (oapi.ListRecordersResponseObject, error) {
	infos := []oapi.RecorderInfo{}

	recs := s.recordManager.ListActiveRecorders(ctx)
	for _, r := range recs {
		m := r.Metadata()
		infos = append(infos, oapi.RecorderInfo{
			Id:          r.ID(),
			IsRecording: r.IsRecording(ctx),
			StartedAt:   timeOrNil(m.StartTime),
			FinishedAt:  timeOrNil(m.EndTime),
		})
	}
	return oapi.ListRecorders200JSONResponse(infos), nil
}

func (s *ApiService) StartStream(ctx context.Context, req oapi.StartStreamRequestObject) (oapi.StartStreamResponseObject, error) {
	log := logger.FromContext(ctx)

	streamID := s.defaultStreamID
	var overrides streamer.FFmpegStreamOverrides
	if req.Body != nil {
		if req.Body.Id != nil && *req.Body.Id != "" {
			streamID = *req.Body.Id
		}
		overrides.FrameRate = req.Body.Framerate
		overrides.DisplayNum = req.Body.DisplayNum
		if req.Body.Mode != nil {
			mode := streamer.StreamMode(*req.Body.Mode)
			overrides.Mode = &mode
		}
		if req.Body.Protocol != nil {
			proto := streamer.StreamProtocol(*req.Body.Protocol)
			overrides.Protocol = &proto
		}
		overrides.ListenHost = req.Body.ListenHost
		overrides.ListenPort = req.Body.ListenPort
		overrides.App = req.Body.App
		overrides.TargetURL = req.Body.TargetUrl
		overrides.TLSCert = req.Body.TlsCertPath
		overrides.TLSKey = req.Body.TlsKeyPath
	}

	str, err := s.streamFactory(streamID, overrides)
	if err != nil {
		log.Error("failed to create stream", "err", err, "stream_id", streamID)
		return oapi.StartStream400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "invalid stream parameters"}}, nil
	}

	if err := s.streamManager.RegisterStream(ctx, str); err != nil {
		if existing, exists := s.streamManager.GetStream(streamID); exists {
			if existing.IsStreaming(ctx) {
				log.Error("attempted to start stream while one is already active", "stream_id", streamID)
				return oapi.StartStream409JSONResponse{ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{Message: "stream already in progress"}}, nil
			}
			_ = s.streamManager.DeregisterStream(ctx, existing)
			if err := s.streamManager.RegisterStream(ctx, str); err != nil {
				log.Error("failed to register stream after cleanup", "err", err, "stream_id", streamID)
				return oapi.StartStream500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to register stream"}}, nil
			}
		} else {
			log.Error("failed to register stream", "err", err, "stream_id", streamID)
			return oapi.StartStream500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to register stream"}}, nil
		}
	}

	if err := str.Start(ctx); err != nil {
		log.Error("failed to start stream", "err", err, "stream_id", streamID)
		defer s.streamManager.DeregisterStream(ctx, str)
		return oapi.StartStream500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to start stream"}}, nil
	}

	meta := str.Metadata()
	var relativeURL *string
	if meta.RelativeURL != "" {
		relativeURL = ptrOf(meta.RelativeURL)
	}
	return oapi.StartStream201JSONResponse(oapi.StartStreamResult{
		Id:          streamID,
		Url:         meta.URL,
		RelativeUrl: relativeURL,
		Mode:        oapi.StartStreamResultMode(meta.Mode),
		Protocol:    oapi.StartStreamResultProtocol(meta.Protocol),
		StartedAt:   timeOrNil(meta.StartTime),
	}), nil
}

func (s *ApiService) StopStream(ctx context.Context, req oapi.StopStreamRequestObject) (oapi.StopStreamResponseObject, error) {
	log := logger.FromContext(ctx)

	streamID := s.defaultStreamID
	if req.Body != nil && req.Body.Id != nil && *req.Body.Id != "" {
		streamID = *req.Body.Id
	}

	str, exists := s.streamManager.GetStream(streamID)
	if !exists {
		log.Error("attempted to stop stream when none is active", "stream_id", streamID)
		return oapi.StopStream400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "no active stream to stop"}}, nil
	}

	forceStop := false
	if req.Body != nil && req.Body.ForceStop != nil {
		forceStop = *req.Body.ForceStop
	}

	var err error
	if forceStop {
		log.Info("force stopping stream", "stream_id", streamID)
		err = str.ForceStop(ctx)
	} else {
		log.Info("gracefully stopping stream", "stream_id", streamID)
		err = str.Stop(ctx)
	}

	if err != nil {
		log.Error("error occurred while stopping stream", "err", err, "force", forceStop, "stream_id", streamID)
	}

	_ = s.streamManager.DeregisterStream(ctx, str)

	return oapi.StopStream200Response{}, nil
}

func (s *ApiService) ListStreams(ctx context.Context, _ oapi.ListStreamsRequestObject) (oapi.ListStreamsResponseObject, error) {
	streams := s.streamManager.ListActiveStreams(ctx)
	infos := make([]oapi.StreamInfo, 0, len(streams))

	for _, stream := range streams {
		meta := stream.Metadata()
		infos = append(infos, oapi.StreamInfo{
			Id:          stream.ID(),
			Url:         meta.URL,
			RelativeUrl: nilIfEmpty(meta.RelativeURL),
			Mode:        oapi.StreamInfoMode(meta.Mode),
			Protocol:    oapi.StreamInfoProtocol(meta.Protocol),
			IsStreaming: stream.IsStreaming(ctx),
			StartedAt:   timeOrNil(meta.StartTime),
			FinishedAt:  timeOrNil(meta.EndTime),
		})
	}

	return oapi.ListStreams200JSONResponse(infos), nil
}

func (s *ApiService) Shutdown(ctx context.Context) error {
	var errs []error
	if err := s.streamManager.StopAll(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.recordManager.StopAll(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
