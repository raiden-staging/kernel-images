package api

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

func (s *ApiService) StartVirtualMedia(ctx context.Context, req oapi.StartVirtualMediaRequestObject) (oapi.StartVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	mediaType := string(req.Body.MediaType)
	sourceURL := req.Body.SourceUrl
	loop := req.Body.Loop
	restart := req.Body.Restart

	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()

	if cancel, exists := s.mediaCancels[mediaType]; exists {
		if !restart {
			return oapi.StartVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: fmt.Sprintf("%s stream already running", mediaType)}}, nil
		}
		cancel()
		delete(s.mediaCancels, mediaType)
	}

	cmdArgs := []string{"-re"}
	if loop {
		cmdArgs = append(cmdArgs, "-stream_loop", "-1")
	}
	cmdArgs = append(cmdArgs, "-i", sourceURL)

	if mediaType == "audio" {
		cmdArgs = append(cmdArgs, "-f", "pulse", "audio_input")
	} else if mediaType == "video" {
		// Assuming /dev/video0 is the device created by v4l2loopback
		// We use yuv420p as it is widely supported
		cmdArgs = append(cmdArgs, "-f", "v4l2", "-pix_fmt", "yuv420p", "/dev/video0")
	} else {
		return oapi.StartVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: "invalid media type"}}, nil
	}

	// Create a context that can be cancelled
	// We use context.Background() because the process should run after the request completes
	cmdCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(cmdCtx, "ffmpeg", cmdArgs...)

	log.Info("Starting virtual media", "type", mediaType, "args", cmdArgs)

	if err := cmd.Start(); err != nil {
		cancel()
		log.Error("failed to start ffmpeg", "err", err)
		return oapi.StartVirtualMedia500JSONResponse{InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to start media process"}}, nil
	}

	s.mediaCancels[mediaType] = cancel

	// Clean up when process exits
	go func() {
		err := cmd.Wait()
		s.mediaMu.Lock()
		defer s.mediaMu.Unlock()
		
		// If the process exited, log it.
		// Note: We don't remove the cancel func from the map here if we can't be sure it's the SAME process.
		// But in this simple implementation, if the user restarts, the map entry is overwritten.
		// The old goroutine will finish.
		// If the process dies unexpectedly, the map entry remains. The user can Restart=true to fix it.
		// Or Stop() which will call cancel (no-op) and remove it.
		
		if err != nil {
			// If context was cancelled, err is usually "signal: killed" or similar
			if cmdCtx.Err() == context.Canceled {
				log.Info("media process stopped by request", "type", mediaType)
			} else {
				log.Error("media process exited with error", "type", mediaType, "err", err)
			}
		} else {
			log.Info("media process exited successfully", "type", mediaType)
		}
	}()

	return oapi.StartVirtualMedia200Response{}, nil
}

func (s *ApiService) StopVirtualMedia(ctx context.Context, req oapi.StopVirtualMediaRequestObject) (oapi.StopVirtualMediaResponseObject, error) {
	log := logger.FromContext(ctx)
	mediaType := string(req.Body.MediaType)

	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()

	cancel, exists := s.mediaCancels[mediaType]
	if !exists {
		// It's benign if it's already stopped, but strict API might want 400.
		// The requirement doesn't specify. I'll return success if it's already stopped?
		// But earlier I wrote 400 in my thought process. Let's stick to 400 to match "stream not running".
		return oapi.StopVirtualMedia400JSONResponse{BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{Message: fmt.Sprintf("%s stream not running", mediaType)}}, nil
	}

	cancel()
	delete(s.mediaCancels, mediaType)
	log.Info("Stopped virtual media", "type", mediaType)

	return oapi.StopVirtualMedia200Response{}, nil
}
