package api

import (
	"context"
	"errors"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/virtualmedia"
)

func (s *ApiService) GetVirtualMediaStatus(ctx context.Context, _ oapi.GetVirtualMediaStatusRequestObject) (oapi.GetVirtualMediaStatusResponseObject, error) {
	status := s.virtualMedia.Status(ctx)
	return oapi.GetVirtualMediaStatus200JSONResponse(toOAPIVirtualMediaStatus(status)), nil
}

func (s *ApiService) SetVirtualMedia(ctx context.Context, req oapi.SetVirtualMediaRequestObject) (oapi.SetVirtualMediaResponseObject, error) {
	if req.Body == nil {
		return oapi.SetVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "request body required"}}, nil
	}

	cfg := virtualmedia.Config{
		Video: fromOAPISource(req.Body.Video),
		Audio: fromOAPISource(req.Body.Audio),
	}

	status, err := s.virtualMedia.SetSources(ctx, cfg)
	if err != nil {
		if isVirtualMediaBadRequest(err) {
			return oapi.SetVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
		}
		return oapi.SetVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: err.Error()}}, nil
	}

	return oapi.SetVirtualMedia200JSONResponse(toOAPIVirtualMediaStatus(status)), nil
}

func (s *ApiService) PauseVirtualMedia(ctx context.Context, _ oapi.PauseVirtualMediaRequestObject) (oapi.PauseVirtualMediaResponseObject, error) {
	status, err := s.virtualMedia.Pause(ctx)
	if err != nil {
		if isVirtualMediaBadRequest(err) {
			return oapi.PauseVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
		}
		return oapi.PauseVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: err.Error()}}, nil
	}
	logger.FromContext(ctx).Info("virtual media pause requested")
	return oapi.PauseVirtualMedia200JSONResponse(toOAPIVirtualMediaStatus(status)), nil
}

func (s *ApiService) ResumeVirtualMedia(ctx context.Context, _ oapi.ResumeVirtualMediaRequestObject) (oapi.ResumeVirtualMediaResponseObject, error) {
	status, err := s.virtualMedia.Resume(ctx)
	if err != nil {
		if isVirtualMediaBadRequest(err) {
			return oapi.ResumeVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: err.Error()}}, nil
		}
		return oapi.ResumeVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: err.Error()}}, nil
	}
	logger.FromContext(ctx).Info("virtual media resume requested")
	return oapi.ResumeVirtualMedia200JSONResponse(toOAPIVirtualMediaStatus(status)), nil
}

func (s *ApiService) StopVirtualMedia(ctx context.Context, _ oapi.StopVirtualMediaRequestObject) (oapi.StopVirtualMediaResponseObject, error) {
	status, err := s.virtualMedia.Stop(ctx)
	if err != nil {
		return oapi.StopVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: err.Error()}}, nil
	}
	logger.FromContext(ctx).Info("virtual media stop requested")
	return oapi.StopVirtualMedia200JSONResponse(toOAPIVirtualMediaStatus(status)), nil
}

func isVirtualMediaBadRequest(err error) bool {
	return errors.Is(err, virtualmedia.ErrNoSources) ||
		errors.Is(err, virtualmedia.ErrInvalidSrc) ||
		errors.Is(err, virtualmedia.ErrNotRunning) ||
		errors.Is(err, virtualmedia.ErrNotPaused)
}

func toOAPIVirtualMediaStatus(status virtualmedia.Status) oapi.VirtualMediaStatus {
	result := oapi.VirtualMediaStatus{
		State: oapi.VirtualMediaStatusState(status.State),
	}
	if status.VideoDevice != "" {
		result.VideoDevice = &status.VideoDevice
	}
	if status.AudioSink != "" {
		result.AudioSink = &status.AudioSink
	}
	if vs := toPipelineStatus(status.Video); vs != nil {
		result.Video = vs
	}
	if as := toPipelineStatus(status.Audio); as != nil {
		result.Audio = as
	}
	return result
}

func toPipelineStatus(ps virtualmedia.PipelineStatus) *oapi.VirtualMediaPipelineStatus {
	if ps.State == virtualmedia.StateStopped && ps.Source == nil && ps.PID == 0 {
		return nil
	}
	result := &oapi.VirtualMediaPipelineStatus{
		State: oapi.VirtualMediaPipelineStatusState(ps.State),
	}
	if ps.PID != 0 {
		result.Pid = &ps.PID
	}
	if src := toOAPISource(ps.Source); src != nil {
		result.Source = src
	}
	return result
}

func toOAPISource(src *virtualmedia.Source) *oapi.VirtualMediaSource {
	if src == nil {
		return nil
	}
	result := &oapi.VirtualMediaSource{
		Url: src.URL,
	}
	if src.Type != "" {
		t := oapi.VirtualMediaSourceType(src.Type)
		result.Type = &t
	}
	loop := src.Loop
	result.Loop = &loop
	return result
}

func fromOAPISource(src *oapi.VirtualMediaSource) *virtualmedia.Source {
	if src == nil {
		return nil
	}
	out := &virtualmedia.Source{
		URL: src.Url,
		Loop: func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		}(src.Loop),
	}
	if src.Type != nil {
		out.Type = virtualmedia.SourceType(*src.Type)
	}
	return out
}
