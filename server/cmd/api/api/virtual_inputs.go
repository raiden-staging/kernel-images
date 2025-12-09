package api

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/onkernel/kernel-images/server/lib/chromiumflags"
	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/virtualinputs"
)

func (s *ApiService) ConfigureVirtualInputs(ctx context.Context, req oapi.ConfigureVirtualInputsRequestObject) (oapi.ConfigureVirtualInputsResponseObject, error) {
	log := logger.FromContext(ctx)

	if req.Body == nil {
		return oapi.ConfigureVirtualInputs400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "request body required"},
		}, nil
	}

	cfg, startPaused := fromVirtualInputsRequest(*req.Body)
	status, err := s.virtualInputs.Configure(ctx, cfg, startPaused)
	if err != nil {
		if isVirtualInputBadRequest(err) {
			return oapi.ConfigureVirtualInputs400JSONResponse{
				BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()},
			}, nil
		}
		log.Error("failed to configure virtual inputs", "err", err)
		return oapi.ConfigureVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to configure virtual inputs"},
		}, nil
	}

	if err := s.applyChromiumCaptureFlags(ctx, status); err != nil {
		log.Error("failed to apply chromium capture flags", "err", err)
		return oapi.ConfigureVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to apply chromium flags"},
		}, nil
	}
	s.updateVirtualInputIngest(status)

	return oapi.ConfigureVirtualInputs200JSONResponse(toVirtualInputsStatus(status)), nil
}

func (s *ApiService) PauseVirtualInputs(ctx context.Context, _ oapi.PauseVirtualInputsRequestObject) (oapi.PauseVirtualInputsResponseObject, error) {
	log := logger.FromContext(ctx)
	status, err := s.virtualInputs.Pause(ctx)
	if err != nil {
		if isVirtualInputBadRequest(err) {
			return oapi.PauseVirtualInputs400JSONResponse{
				BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()},
			}, nil
		}
		log.Error("failed to pause virtual inputs", "err", err)
		return oapi.PauseVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to pause virtual inputs"},
		}, nil
	}
	return oapi.PauseVirtualInputs200JSONResponse(toVirtualInputsStatus(status)), nil
}

func (s *ApiService) ResumeVirtualInputs(ctx context.Context, _ oapi.ResumeVirtualInputsRequestObject) (oapi.ResumeVirtualInputsResponseObject, error) {
	log := logger.FromContext(ctx)
	status, err := s.virtualInputs.Resume(ctx)
	if err != nil {
		if isVirtualInputBadRequest(err) {
			return oapi.ResumeVirtualInputs400JSONResponse{
				BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()},
			}, nil
		}
		log.Error("failed to resume virtual inputs", "err", err)
		return oapi.ResumeVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to resume virtual inputs"},
		}, nil
	}
	s.updateVirtualInputIngest(status)
	return oapi.ResumeVirtualInputs200JSONResponse(toVirtualInputsStatus(status)), nil
}

func (s *ApiService) StopVirtualInputs(ctx context.Context, _ oapi.StopVirtualInputsRequestObject) (oapi.StopVirtualInputsResponseObject, error) {
	log := logger.FromContext(ctx)
	status, err := s.virtualInputs.Stop(ctx)
	if err != nil {
		log.Error("failed to stop virtual inputs", "err", err)
		return oapi.StopVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to stop virtual inputs"},
		}, nil
	}
	if err := s.applyChromiumCaptureFlags(ctx, status); err != nil {
		log.Error("failed to clear chromium capture flags", "err", err)
		return oapi.StopVirtualInputs500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to clear chromium flags"},
		}, nil
	}
	s.updateVirtualInputIngest(status)
	return oapi.StopVirtualInputs200JSONResponse(toVirtualInputsStatus(status)), nil
}

func (s *ApiService) GetVirtualInputsStatus(ctx context.Context, _ oapi.GetVirtualInputsStatusRequestObject) (oapi.GetVirtualInputsStatusResponseObject, error) {
	status := s.virtualInputs.Status(ctx)
	return oapi.GetVirtualInputsStatus200JSONResponse(toVirtualInputsStatus(status)), nil
}

func (s *ApiService) GetVirtualInputFeedSocketInfo(ctx context.Context, _ oapi.GetVirtualInputFeedSocketInfoRequestObject) (oapi.GetVirtualInputFeedSocketInfoResponseObject, error) {
	status := s.virtualInputs.Status(ctx)
	if status.Ingest == nil || status.Ingest.Video == nil {
		return oapi.GetVirtualInputFeedSocketInfo409JSONResponse{
			ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{Message: "virtual video ingest not active"},
		}, nil
	}

	format := status.Ingest.Video.Format
	switch {
	case format != "":
	case status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeWebRTC):
		format = "ivf"
	case status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeSocket):
		format = "mpegts"
	}

	resp := oapi.VirtualFeedSocketInfo{
		Url: "/input/devices/virtual/feed/socket",
	}
	if format != "" {
		resp.Format = &format
	}

	return oapi.GetVirtualInputFeedSocketInfo200JSONResponse(resp), nil
}

func fromVirtualInputsRequest(body oapi.VirtualInputsRequest) (virtualinputs.Config, bool) {
	cfg := virtualinputs.Config{}
	if body.Video != nil {
		cfg.Video = &virtualinputs.MediaSource{
			Type: virtualinputs.SourceType(body.Video.Type),
		}
		if body.Video.Url != nil {
			cfg.Video.URL = *body.Video.Url
		}
		if body.Video.Format != nil {
			cfg.Video.Format = *body.Video.Format
		}
		if body.Video.Width != nil {
			cfg.Width = int(*body.Video.Width)
		}
		if body.Video.Height != nil {
			cfg.Height = int(*body.Video.Height)
		}
		if body.Video.FrameRate != nil {
			cfg.FrameRate = int(*body.Video.FrameRate)
		}
	}
	if body.Audio != nil {
		cfg.Audio = &virtualinputs.MediaSource{
			Type: virtualinputs.SourceType(body.Audio.Type),
		}
		if body.Audio.Url != nil {
			cfg.Audio.URL = *body.Audio.Url
		}
		if body.Audio.Format != nil {
			cfg.Audio.Format = *body.Audio.Format
		}
	}

	startPaused := false
	if body.StartPaused != nil {
		startPaused = *body.StartPaused
	}

	return cfg, startPaused
}

func toVirtualInputsStatus(status virtualinputs.Status) oapi.VirtualInputsStatus {
	resp := oapi.VirtualInputsStatus{
		State:            oapi.VirtualInputsStatusState(status.State),
		VideoDevice:      status.VideoDevice,
		AudioSink:        status.AudioSink,
		MicrophoneSource: status.MicrophoneSource,
	}
	if status.Mode != "" {
		resp.Mode = oapi.VirtualInputsStatusMode(status.Mode)
	} else {
		resp.Mode = oapi.Device
	}
	if status.LastError != "" {
		resp.LastError = &status.LastError
	}
	if status.StartedAt != nil {
		resp.StartedAt = status.StartedAt
	}
	if status.VideoFile != "" {
		resp.VideoFile = &status.VideoFile
	}
	if status.AudioFile != "" {
		resp.AudioFile = &status.AudioFile
	}
	if status.Video != nil {
		url := status.Video.URL
		resp.Video = &oapi.VirtualInputVideo{
			Type: oapi.VirtualInputType(status.Video.Type),
			Url:  &url,
			Format: func() *string {
				if status.Video.Format == "" {
					return nil
				}
				return &status.Video.Format
			}(),
		}
		if status.Width > 0 {
			resp.Video.Width = &status.Width
		}
		if status.Height > 0 {
			resp.Video.Height = &status.Height
		}
		if status.FrameRate > 0 {
			resp.Video.FrameRate = &status.FrameRate
		}
	}
	if status.Audio != nil {
		url := status.Audio.URL
		resp.Audio = &oapi.VirtualInputAudio{
			Type: oapi.VirtualInputType(status.Audio.Type),
			Url:  &url,
			Format: func() *string {
				if status.Audio.Format == "" {
					return nil
				}
				return &status.Audio.Format
			}(),
		}
	}
	if status.Ingest != nil {
		resp.Ingest = &oapi.VirtualInputsIngest{}
		if status.Ingest.Audio != nil {
			audioURL := status.Ingest.Audio.Path
			if status.Ingest.Audio.Protocol == string(virtualinputs.SourceTypeSocket) {
				audioURL = "/input/devices/virtual/socket/audio"
			} else if status.Ingest.Audio.Protocol == string(virtualinputs.SourceTypeWebRTC) {
				audioURL = "/input/devices/virtual/webrtc/offer"
			}
			resp.Ingest.Audio = &oapi.VirtualInputIngestEndpoint{}
			if status.Ingest.Audio.Protocol != "" {
				resp.Ingest.Audio.Protocol = &status.Ingest.Audio.Protocol
			}
			if status.Ingest.Audio.Format != "" {
				resp.Ingest.Audio.Format = &status.Ingest.Audio.Format
			}
			resp.Ingest.Audio.Url = &audioURL
		}
		if status.Ingest.Video != nil {
			videoURL := status.Ingest.Video.Path
			if status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeSocket) {
				videoURL = "/input/devices/virtual/socket/video"
			} else if status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeWebRTC) {
				videoURL = "/input/devices/virtual/webrtc/offer"
			}
			resp.Ingest.Video = &oapi.VirtualInputIngestEndpoint{}
			if status.Ingest.Video.Protocol != "" {
				resp.Ingest.Video.Protocol = &status.Ingest.Video.Protocol
			}
			if status.Ingest.Video.Format != "" {
				resp.Ingest.Video.Format = &status.Ingest.Video.Format
			}
			resp.Ingest.Video.Url = &videoURL
		}
	}
	return resp
}

func (s *ApiService) applyChromiumCaptureFlags(ctx context.Context, status virtualinputs.Status) error {
	const (
		flagFakeDevice = "--use-fake-device-for-media-stream"
		flagAutoAccept = "--auto-accept-camera-and-microphone-capture"
		videoPrefix    = "--use-file-for-fake-video-capture="
		audioPrefix    = "--use-file-for-fake-audio-capture="
		flagsPath      = "/chromium/flags"
	)

	shouldUseVirtual := status.Mode == "virtual-file" && status.VideoFile != ""
	existing, err := chromiumflags.ReadOptionalFlagFile(flagsPath)
	if err != nil {
		return fmt.Errorf("read flags: %w", err)
	}

	filtered := filterTokens(existing, []string{flagFakeDevice}, []string{videoPrefix, audioPrefix})
	required := []string{flagAutoAccept}
	if shouldUseVirtual {
		required = append(required, flagFakeDevice, videoPrefix+status.VideoFile)
		if status.AudioFile != "" {
			required = append(required, audioPrefix+status.AudioFile)
		}
	}

	merged := chromiumflags.MergeFlags(filtered, required)
	if slicesEqual(existing, merged) {
		return nil
	}

	if err := chromiumflags.WriteFlagFile(flagsPath, merged); err != nil {
		return fmt.Errorf("write flags: %w", err)
	}

	return s.restartChromiumAndWait(ctx, "virtual inputs configure")
}

func filterTokens(tokens, exact []string, prefixes []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		skip := false
		for _, ex := range exact {
			if t == ex {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, t)
	}
	return out
}

func slicesEqual(a, b []string) bool {
	return reflect.DeepEqual(a, b)
}

func isVirtualInputBadRequest(err error) bool {
	return errors.Is(err, virtualinputs.ErrMissingSources) ||
		errors.Is(err, virtualinputs.ErrVideoURLRequired) ||
		errors.Is(err, virtualinputs.ErrAudioURLRequired) ||
		errors.Is(err, virtualinputs.ErrVideoTypeRequired) ||
		errors.Is(err, virtualinputs.ErrAudioTypeRequired) ||
		errors.Is(err, virtualinputs.ErrPauseWithoutSession) ||
		errors.Is(err, virtualinputs.ErrNoConfigToPause) ||
		errors.Is(err, virtualinputs.ErrNoConfigToResume)
}

func (s *ApiService) updateVirtualInputIngest(status virtualinputs.Status) {
	if s.virtualInputsWebRTC == nil {
		return
	}

	if status.Ingest == nil {
		s.virtualInputsWebRTC.Clear()
		s.virtualInputsWebRTC.SetSinks(nil, nil)
		if s.virtualFeed != nil {
			s.virtualFeed.clear()
		}
		return
	}

	videoPath := ""
	videoFmt := ""
	audioPath := ""
	audioFmt := ""
	if status.Ingest.Video != nil {
		videoPath = status.Ingest.Video.Path
		videoFmt = status.Ingest.Video.Format
	}
	if status.Ingest.Audio != nil {
		audioPath = status.Ingest.Audio.Path
		audioFmt = status.Ingest.Audio.Format
	}
	s.virtualInputsWebRTC.Configure(videoPath, videoFmt, audioPath, audioFmt)

	if s.virtualFeed == nil {
		return
	}

	format := ""
	if status.Ingest.Video == nil {
		s.virtualInputsWebRTC.SetSinks(nil, nil)
		s.virtualFeed.clear()
		return
	}
	format = status.Ingest.Video.Format
	if format == "" {
		if status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeWebRTC) {
			format = "ivf"
		} else if status.Ingest.Video.Protocol == string(virtualinputs.SourceTypeSocket) {
			format = "mpegts"
		}
	}

	videoSink := s.virtualFeed.writer(format)
	if format != "" {
		s.virtualFeed.setFormat(format)
	}
	s.virtualInputsWebRTC.SetSinks(videoSink, nil)
}
