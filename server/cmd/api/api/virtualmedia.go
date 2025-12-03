package api

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/onkernel/kernel-images/server/lib/chromiumflags"
	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/virtualmedia"
)

const (
	chromiumFlagsPath        = "/chromium/flags"
	chromiumRuntimeFlagsPath = "/chromium/runtime-flags"
)

func (s *ApiService) StartVirtualMedia(ctx context.Context, request oapi.StartVirtualMediaRequestObject) (oapi.StartVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	if request.Body == nil {
		return oapi.StartVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "request body required"}}, nil
	}

	cfg, err := parseVirtualMediaConfig(request.Body)
	if err != nil {
		return oapi.StartVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
	}

	if cfg.Video != nil {
		if reason := strings.TrimSpace(os.Getenv("VIRTUAL_MEDIA_VIDEO_UNAVAILABLE_REASON")); reason != "" {
			return oapi.StartVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: fmt.Sprintf("virtual camera unavailable: %s", reason)}}, nil
		}
	}

	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()

	paths, err := s.virtualMedia.Configure(ctx, cfg)
	if err != nil {
		log.Error("failed to configure virtual media", "error", err)
		return oapi.StartVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to configure virtual media"}}, nil
	}

	flags := buildVirtualMediaFlags(paths)
	if _, err := s.replaceVirtualMediaFlags(ctx, flags); err != nil {
		log.Error("failed to write chromium flags for virtual media", "error", err)
		_ = s.virtualMedia.Stop(context.WithoutCancel(ctx))
		return oapi.StartVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to update chromium flags"}}, nil
	}

	if err := s.restartChromiumAndWait(ctx, "virtual media start"); err != nil {
		log.Error("failed to restart chromium after configuring virtual media", "error", err)
		return oapi.StartVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to restart chromium"}}, nil
	}

	status := s.virtualMedia.Status()
	return oapi.StartVirtualMedia200JSONResponse(toVirtualMediaStatus(status, s.virtualMediaFlags)), nil
}

func (s *ApiService) StopVirtualMedia(ctx context.Context, _ oapi.StopVirtualMediaRequestObject) (oapi.StopVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()

	current := s.virtualMedia.Status()
	hadFlags := len(s.virtualMediaFlags) > 0

	if err := s.virtualMedia.Stop(ctx); err != nil {
		log.Error("failed to stop virtual media", "error", err)
		return oapi.StopVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to stop virtual media"}}, nil
	}

	if _, err := s.replaceVirtualMediaFlags(ctx, nil); err != nil {
		log.Error("failed to clear chromium flags for virtual media", "error", err)
		return oapi.StopVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to update chromium flags"}}, nil
	}

	shouldRestart := hadFlags || current.Video != nil || current.Audio != nil
	if shouldRestart {
		if err := s.restartChromiumAndWait(ctx, "virtual media stop"); err != nil {
			log.Error("failed to restart chromium after stopping virtual media", "error", err)
			return oapi.StopVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to restart chromium"}}, nil
		}
	}

	status := s.virtualMedia.Status()
	return oapi.StopVirtualMedia200JSONResponse(toVirtualMediaStatus(status, s.virtualMediaFlags)), nil
}

func (s *ApiService) PauseVirtualMedia(ctx context.Context, request oapi.PauseVirtualMediaRequestObject) (oapi.PauseVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	pauseVideo, pauseAudio, err := parseVirtualMediaTargets(request.Body)
	if err != nil {
		return oapi.PauseVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
	}

	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()

	state := s.virtualMedia.Status()
	if pauseVideo && state.Video == nil {
		return oapi.PauseVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "virtual video input not active"}}, nil
	}
	if pauseAudio && state.Audio == nil {
		return oapi.PauseVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "virtual audio input not active"}}, nil
	}

	if err := s.virtualMedia.Pause(ctx, pauseVideo, pauseAudio); err != nil {
		log.Error("failed to pause virtual media", "error", err)
		return oapi.PauseVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to pause virtual media"}}, nil
	}

	status := s.virtualMedia.Status()
	return oapi.PauseVirtualMedia200JSONResponse(toVirtualMediaStatus(status, s.virtualMediaFlags)), nil
}

func (s *ApiService) ResumeVirtualMedia(ctx context.Context, request oapi.ResumeVirtualMediaRequestObject) (oapi.ResumeVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	resumeVideo, resumeAudio, err := parseVirtualMediaTargets(request.Body)
	if err != nil {
		return oapi.ResumeVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
	}

	s.virtualMediaMu.Lock()
	defer s.virtualMediaMu.Unlock()

	state := s.virtualMedia.Status()
	if resumeVideo && state.Video == nil {
		return oapi.ResumeVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "virtual video input not active"}}, nil
	}
	if resumeAudio && state.Audio == nil {
		return oapi.ResumeVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "virtual audio input not active"}}, nil
	}

	if err := s.virtualMedia.Resume(ctx, resumeVideo, resumeAudio); err != nil {
		log.Error("failed to resume virtual media", "error", err)
		return oapi.ResumeVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to resume virtual media"}}, nil
	}

	status := s.virtualMedia.Status()
	return oapi.ResumeVirtualMedia200JSONResponse(toVirtualMediaStatus(status, s.virtualMediaFlags)), nil
}

func (s *ApiService) VirtualMediaStatus(ctx context.Context, _ oapi.VirtualMediaStatusRequestObject) (oapi.VirtualMediaStatusResponseObject, error) {
	s.virtualMediaMu.Lock()
	flags := append([]string{}, s.virtualMediaFlags...)
	status := s.virtualMedia.Status()
	s.virtualMediaMu.Unlock()
	return oapi.VirtualMediaStatus200JSONResponse(toVirtualMediaStatus(status, flags)), nil
}

func (s *ApiService) replaceVirtualMediaFlags(ctx context.Context, newFlags []string) ([]string, error) {
	log := logger.FromContext(ctx)

	existingTokens, err := chromiumflags.ReadOptionalFlagFile(chromiumRuntimeFlagsPath)
	if err != nil {
		log.Error("failed to read existing chromium runtime flags", "error", err)
		return nil, fmt.Errorf("failed to read existing chromium runtime flags: %w", err)
	}

	filtered := filterTokens(existingTokens, s.virtualMediaFlags)
	merged := chromiumflags.MergeFlags(filtered, newFlags)

	if err := os.MkdirAll("/chromium", 0o755); err != nil {
		log.Error("failed to create chromium dir", "error", err)
		return nil, fmt.Errorf("failed to create chromium dir: %w", err)
	}

	if err := chromiumflags.WriteFlagFile(chromiumRuntimeFlagsPath, merged); err != nil {
		log.Error("failed to write chromium runtime flags", "error", err)
		return nil, fmt.Errorf("failed to write chromium runtime flags: %w", err)
	}

	if len(newFlags) == 0 {
		s.virtualMediaFlags = nil
	} else {
		s.virtualMediaFlags = newFlags
	}
	return merged, nil
}

func filterTokens(tokens, toRemove []string) []string {
	out := make([]string, 0, len(tokens))
	remove := make(map[string]struct{}, len(toRemove))
	for _, t := range toRemove {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			remove[trimmed] = struct{}{}
		}
	}
	for _, t := range tokens {
		trimmed := strings.TrimSpace(t)
		if trimmed == "" {
			continue
		}
		if _, found := remove[trimmed]; found {
			continue
		}
		if virtualmedia.IsLegacyChromiumFlag(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func buildVirtualMediaFlags(paths virtualmedia.Paths) []string {
	_ = paths // legacy signature; no Chromium flags are required for OS-level virtual devices
	return nil
}

func parseVirtualMediaConfig(body *oapi.VirtualMediaStartRequest) (virtualmedia.Config, error) {
	var cfg virtualmedia.Config

	if body.Video != nil {
		video, err := toManagerSource(body.Video)
		if err != nil {
			return cfg, err
		}
		cfg.Video = video
	}
	if body.Audio != nil {
		audio, err := toManagerSource(body.Audio)
		if err != nil {
			return cfg, err
		}
		cfg.Audio = audio
	}
	if cfg.Video == nil && cfg.Audio == nil {
		return cfg, fmt.Errorf("at least one of audio or video is required")
	}
	return cfg, nil
}

func toManagerSource(src *oapi.VirtualMediaSource) (*virtualmedia.Source, error) {
	if src == nil {
		return nil, fmt.Errorf("source is required")
	}
	kind := virtualmedia.SourceKind(strings.ToLower(string(src.Kind)))
	switch kind {
	case virtualmedia.SourceKindFile, virtualmedia.SourceKindStream:
	default:
		return nil, fmt.Errorf("unsupported source kind: %s", src.Kind)
	}
	loop := false
	if src.Loop != nil {
		loop = *src.Loop
	}
	if kind == virtualmedia.SourceKindStream && loop {
		return nil, fmt.Errorf("loop is only supported for file sources")
	}
	if strings.TrimSpace(src.Url) == "" {
		return nil, fmt.Errorf("source url is required")
	}
	return &virtualmedia.Source{
		URL:  src.Url,
		Kind: kind,
		Loop: loop,
	}, nil
}

func parseVirtualMediaTargets(body *oapi.VirtualMediaControlRequest) (bool, bool, error) {
	// Default to both when not provided.
	if body == nil || body.Targets == nil {
		return true, true, nil
	}
	var video, audio bool
	for _, target := range *body.Targets {
		switch target {
		case oapi.Video:
			video = true
		case oapi.Audio:
			audio = true
		default:
			return false, false, fmt.Errorf("unsupported target: %s", target)
		}
	}
	if !video && !audio {
		return false, false, fmt.Errorf("at least one target is required")
	}
	return video, audio, nil
}

func toVirtualMediaStatus(status virtualmedia.Status, flags []string) oapi.VirtualMediaStatus {
	var flagPtr *[]string
	if len(flags) > 0 {
		copied := append([]string{}, flags...)
		flagPtr = &copied
	}

	return oapi.VirtualMediaStatus{
		Audio:         toVirtualMediaTrackStatus(status.Audio),
		Video:         toVirtualMediaTrackStatus(status.Video),
		ChromiumFlags: flagPtr,
	}
}

func toVirtualMediaTrackStatus(track *virtualmedia.TrackStatus) *oapi.VirtualMediaTrackStatus {
	if track == nil {
		return nil
	}
	resp := &oapi.VirtualMediaTrackStatus{
		Active: track.Active,
		Paused: track.Paused,
	}
	if track.PID != 0 {
		pid := track.PID
		resp.Pid = &pid
	}
	if track.OutputPath != "" {
		path := track.OutputPath
		resp.OutputPath = &path
	}
	if track.StartedAt != nil {
		resp.StartedAt = track.StartedAt
	}
	if track.LastError != "" {
		resp.LastError = &track.LastError
	}
	if track.Source != nil {
		resp.Source = &oapi.VirtualMediaSource{
			Kind: oapi.VirtualMediaSourceKind(track.Source.Kind),
			Url:  track.Source.URL,
		}
		if track.Source.Loop {
			loop := true
			resp.Source.Loop = &loop
		}
	}
	return resp
}
