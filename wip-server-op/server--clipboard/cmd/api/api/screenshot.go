package api

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

// CaptureScreenshot captures a screenshot of the entire screen
func (s *ApiService) CaptureScreenshot(ctx context.Context, _ oapi.CaptureScreenshotRequestObject) (oapi.CaptureScreenshotResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("capturing full screen screenshot")

	// Use import display from the helper function
	display := getDisplay()

	// Use imagemagick to capture the screenshot
	cmd := exec.CommandContext(ctx, "bash", "-lc", fmt.Sprintf("DISPLAY=%s import -window root png:-", display))
	screenshot, err := cmd.Output()
	if err != nil {
		log.Error("failed to capture screenshot", "err", err)
		return oapi.CaptureScreenshot500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to capture screenshot: %v", err),
			},
		}, nil
	}

	return oapi.CaptureScreenshot200ImagepngResponse{
		Body:          bytes.NewReader(screenshot),
		ContentLength: int64(len(screenshot)),
	}, nil
}

// CaptureScreenshotRegion captures a screenshot of a specific region
func (s *ApiService) CaptureScreenshotRegion(ctx context.Context, req oapi.CaptureScreenshotRegionRequestObject) (oapi.CaptureScreenshotRegionResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("capturing region screenshot", "x", req.Body.X, "y", req.Body.Y, "width", req.Body.Width, "height", req.Body.Height)

	// Use import display from the helper function
	display := getDisplay()

	// Use imagemagick to capture the screenshot of the specified region
	cmd := exec.CommandContext(ctx, "bash", "-lc", fmt.Sprintf(
		"DISPLAY=%s import -window root -crop %dx%d+%d+%d png:-",
		display, req.Body.Width, req.Body.Height, req.Body.X, req.Body.Y))

	screenshot, err := cmd.Output()
	if err != nil {
		log.Error("failed to capture region screenshot", "err", err)
		return oapi.CaptureScreenshotRegion500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to capture region screenshot: %v", err),
			},
		}, nil
	}

	return oapi.CaptureScreenshotRegion200ImagepngResponse{
		Body:          bytes.NewReader(screenshot),
		ContentLength: int64(len(screenshot)),
	}, nil
}
