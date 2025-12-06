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

func fromVirtualInputsRequest(body oapi.VirtualInputsRequest) (virtualinputs.Config, bool) {
	cfg := virtualinputs.Config{}
	if body.Video != nil {
		cfg.Video = &virtualinputs.MediaSource{
			Type: virtualinputs.SourceType(body.Video.Type),
			URL:  body.Video.Url,
		}
		if body.Video.Loop != nil {
			cfg.Video.Loop = *body.Video.Loop
		}
	}
	if body.Audio != nil {
		cfg.Audio = &virtualinputs.MediaSource{
			Type: virtualinputs.SourceType(body.Audio.Type),
			URL:  body.Audio.Url,
		}
		if body.Audio.Loop != nil {
			cfg.Audio.Loop = *body.Audio.Loop
		}
	}
	if body.Width != nil {
		cfg.Width = int(*body.Width)
	}
	if body.Height != nil {
		cfg.Height = int(*body.Height)
	}
	if body.FrameRate != nil {
		cfg.FrameRate = int(*body.FrameRate)
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
		Width:            status.Width,
		Height:           status.Height,
		FrameRate:        status.FrameRate,
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
		resp.Video = &oapi.VirtualInputSource{
			Type: oapi.VirtualInputType(status.Video.Type),
			Url:  status.Video.URL,
			Loop: &status.Video.Loop,
		}
	}
	if status.Audio != nil {
		resp.Audio = &oapi.VirtualInputSource{
			Type: oapi.VirtualInputType(status.Audio.Type),
			Url:  status.Audio.URL,
			Loop: &status.Audio.Loop,
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
}
