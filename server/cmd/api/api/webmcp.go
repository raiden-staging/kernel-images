package api

import (
	"context"
	"encoding/json"

	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
	"github.com/onkernel/kernel-images/server/lib/webmcp"
)

// GetWebMCPStatus returns the current WebMCP availability and registered tools on the active tab.
func (s *ApiService) GetWebMCPStatus(ctx context.Context, _ oapi.GetWebMCPStatusRequestObject) (oapi.GetWebMCPStatusResponseObject, error) {
	log := logger.FromContext(ctx)

	upstreamURL := s.upstreamMgr.Current()
	if upstreamURL == "" {
		return oapi.GetWebMCPStatus500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "Chrome CDP upstream not ready"},
		}, nil
	}

	bridge := webmcp.NewBridge(upstreamURL, log)
	if err := bridge.Start(ctx); err != nil {
		log.Error("webmcp status: bridge start failed", "err", err)
		return oapi.GetWebMCPStatus500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{Message: "failed to connect to Chrome CDP"},
		}, nil
	}
	defer bridge.Close()

	tools, err := bridge.ListTools(ctx)
	if err != nil {
		log.Warn("webmcp status: list tools failed", "err", err)
		// Return available=false if we can't list tools
		available := false
		return oapi.GetWebMCPStatus200JSONResponse(oapi.WebMCPStatus{
			Available: available,
		}), nil
	}

	available := true
	oapiTools := make([]oapi.WebMCPTool, len(tools))
	for i, t := range tools {
		oapiTools[i] = oapi.WebMCPTool{
			Name: t.Name,
		}
		if t.Description != "" {
			oapiTools[i].Description = &t.Description
		}
		if len(t.InputSchema) > 0 {
			var schema interface{}
			if err := json.Unmarshal(t.InputSchema, &schema); err == nil {
				oapiTools[i].InputSchema = &schema
			}
		}
		if len(t.Annotations) > 0 {
			var ann interface{}
			if err := json.Unmarshal(t.Annotations, &ann); err == nil {
				oapiTools[i].Annotations = &ann
			}
		}
	}

	resp := oapi.WebMCPStatus{
		Available: available,
	}
	if len(oapiTools) > 0 {
		resp.Tools = &oapiTools
	}

	return oapi.GetWebMCPStatus200JSONResponse(resp), nil
}
