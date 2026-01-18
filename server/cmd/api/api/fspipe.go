package api

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/onkernel/fspipe/pkg/daemon"
	"github.com/onkernel/fspipe/pkg/health"
	"github.com/onkernel/fspipe/pkg/transport"
	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

const (
	defaultFspipeMountPath  = "/home/kernel/Downloads"
	defaultFspipeHealthPort = 8090
)

// fspipeState holds the state of the running fspipe daemon
type fspipeState struct {
	mu sync.RWMutex

	running       bool
	transportMode string // "websocket" or "s3"
	mountPath     string
	wsEndpoint    string
	s3Bucket      string
	healthPort    int

	transport    transport.Transport
	fuseServer   *fuse.Server
	healthServer *health.Server
}

var fspipe = &fspipeState{}

// StartFspipe starts the fspipe daemon with the given configuration
func (s *ApiService) StartFspipe(ctx context.Context, req oapi.StartFspipeRequestObject) (oapi.StartFspipeResponseObject, error) {
	log := logger.FromContext(ctx)

	fspipe.mu.Lock()
	defer fspipe.mu.Unlock()

	// Check if already running
	if fspipe.running {
		return oapi.StartFspipe409JSONResponse{
			ConflictErrorJSONResponse: oapi.ConflictErrorJSONResponse{
				Message: "fspipe daemon is already running",
			},
		}, nil
	}

	// Determine mount path
	mountPath := defaultFspipeMountPath
	if req.Body != nil && req.Body.MountPath != nil && *req.Body.MountPath != "" {
		mountPath = *req.Body.MountPath
	}

	// Determine health port
	healthPort := defaultFspipeHealthPort
	if req.Body != nil && req.Body.HealthPort != nil {
		healthPort = *req.Body.HealthPort
	}

	// Validate transport configuration
	hasWS := req.Body != nil && req.Body.WsEndpoint != nil && *req.Body.WsEndpoint != ""
	hasS3 := req.Body != nil && req.Body.S3Config != nil

	if !hasWS && !hasS3 {
		return oapi.StartFspipe400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "either ws_endpoint or s3_config is required",
			},
		}, nil
	}

	if hasWS && hasS3 {
		return oapi.StartFspipe400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "ws_endpoint and s3_config are mutually exclusive",
			},
		}, nil
	}

	// Create mountpoint if it doesn't exist
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		log.Error("failed to create fspipe mountpoint", "path", mountPath, "error", err)
		return oapi.StartFspipe500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to create mountpoint: %v", err),
			},
		}, nil
	}

	// Create transport
	var client transport.Transport
	var transportMode string
	var wsEndpoint string
	var s3Bucket string

	if hasWS {
		transportMode = "websocket"
		wsEndpoint = *req.Body.WsEndpoint

		var err error
		client, err = transport.NewTransport(wsEndpoint, transport.DefaultClientConfig())
		if err != nil {
			log.Error("failed to create websocket transport", "endpoint", wsEndpoint, "error", err)
			return oapi.StartFspipe500JSONResponse{
				InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
					Message: fmt.Sprintf("failed to create websocket transport: %v", err),
				},
			}, nil
		}
	} else {
		transportMode = "s3"
		s3Cfg := req.Body.S3Config

		// Build S3Config from API request
		region := "auto"
		if s3Cfg.Region != nil {
			region = *s3Cfg.Region
		}
		prefix := ""
		if s3Cfg.Prefix != nil {
			prefix = *s3Cfg.Prefix
		}

		s3Config := transport.S3Config{
			Endpoint:        s3Cfg.Endpoint,
			Bucket:          s3Cfg.Bucket,
			AccessKeyID:     s3Cfg.AccessKeyId,
			SecretAccessKey: s3Cfg.SecretAccessKey,
			Region:          region,
			Prefix:          prefix,
		}
		s3Bucket = s3Cfg.Bucket

		var err error
		client, err = transport.NewS3Client(s3Config)
		if err != nil {
			log.Error("failed to create S3 transport", "bucket", s3Cfg.Bucket, "error", err)
			return oapi.StartFspipe500JSONResponse{
				InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
					Message: fmt.Sprintf("failed to create S3 transport: %v", err),
				},
			}, nil
		}
	}

	// Connect transport
	if err := client.Connect(); err != nil {
		client.Close()
		log.Error("failed to connect fspipe transport", "error", err)
		return oapi.StartFspipe500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to connect transport: %v", err),
			},
		}, nil
	}

	// Mount the FUSE filesystem
	fuseServer, err := daemon.Mount(mountPath, client)
	if err != nil {
		client.Close()
		log.Error("failed to mount fspipe filesystem", "path", mountPath, "error", err)
		return oapi.StartFspipe500JSONResponse{
			InternalErrorJSONResponse: oapi.InternalErrorJSONResponse{
				Message: fmt.Sprintf("failed to mount filesystem: %v", err),
			},
		}, nil
	}

	// Start health server
	healthAddr := fmt.Sprintf(":%d", healthPort)
	healthServer := health.NewServer(healthAddr)

	// Register health checks
	healthServer.RegisterCheck("transport", func() (health.Status, string) {
		state := client.State()
		switch state {
		case transport.StateConnected:
			return health.StatusHealthy, "connected"
		case transport.StateReconnecting:
			return health.StatusDegraded, "reconnecting"
		default:
			return health.StatusUnhealthy, state.String()
		}
	})

	// Register stats provider
	healthServer.RegisterStats("transport", func() map[string]interface{} {
		stats := client.Stats()
		result := make(map[string]interface{})
		for k, v := range stats {
			result[k] = v
		}
		result["state"] = client.State().String()
		return result
	})

	if err := healthServer.Start(); err != nil {
		log.Warn("failed to start fspipe health server", "error", err)
		// Don't fail the whole operation for this
	}

	// Store state
	fspipe.running = true
	fspipe.transportMode = transportMode
	fspipe.mountPath = mountPath
	fspipe.wsEndpoint = wsEndpoint
	fspipe.s3Bucket = s3Bucket
	fspipe.healthPort = healthPort
	fspipe.transport = client
	fspipe.fuseServer = fuseServer
	fspipe.healthServer = healthServer

	log.Info("fspipe daemon started", "mode", transportMode, "mount", mountPath)

	// Set Chrome download directory and restart Chrome
	downloadDirFlag := fmt.Sprintf("--download-default-directory=%s", mountPath)
	if _, err := s.mergeAndWriteChromiumFlags(ctx, []string{downloadDirFlag}); err != nil {
		// Log but don't fail - fspipe is running
		log.Warn("failed to set Chrome download directory flag", "error", err)
	}

	// Restart Chromium to apply the download directory
	if err := s.restartChromiumAndWait(ctx, "fspipe setup"); err != nil {
		// Log but don't fail - fspipe is running
		log.Warn("failed to restart Chrome for fspipe setup", "error", err)
	}

	// Build response
	response := oapi.FspipeStartResult{
		Running:       true,
		TransportMode: oapi.FspipeStartResultTransportMode(transportMode),
		MountPath:     mountPath,
	}

	if transportMode == "websocket" {
		response.WsEndpoint = &wsEndpoint
	} else {
		response.S3Bucket = &s3Bucket
	}

	healthEndpoint := fmt.Sprintf("http://localhost:%d", healthPort)
	response.HealthEndpoint = &healthEndpoint

	return oapi.StartFspipe200JSONResponse(response), nil
}

// StopFspipe stops the running fspipe daemon
func (s *ApiService) StopFspipe(ctx context.Context, req oapi.StopFspipeRequestObject) (oapi.StopFspipeResponseObject, error) {
	log := logger.FromContext(ctx)

	fspipe.mu.Lock()
	defer fspipe.mu.Unlock()

	if !fspipe.running {
		return oapi.StopFspipe400JSONResponse{
			BadRequestErrorJSONResponse: oapi.BadRequestErrorJSONResponse{
				Message: "fspipe daemon is not running",
			},
		}, nil
	}

	// Stop health server
	if fspipe.healthServer != nil {
		fspipe.healthServer.Stop(ctx)
	}

	// Unmount filesystem
	if fspipe.fuseServer != nil {
		if err := fspipe.fuseServer.Unmount(); err != nil {
			log.Warn("failed to unmount fspipe filesystem", "error", err)
		}
	}

	// Close transport
	if fspipe.transport != nil {
		if err := fspipe.transport.Close(); err != nil {
			log.Warn("failed to close fspipe transport", "error", err)
		}
	}

	// Reset state
	fspipe.running = false
	fspipe.transportMode = ""
	fspipe.mountPath = ""
	fspipe.wsEndpoint = ""
	fspipe.s3Bucket = ""
	fspipe.healthPort = 0
	fspipe.transport = nil
	fspipe.fuseServer = nil
	fspipe.healthServer = nil

	log.Info("fspipe daemon stopped")
	return oapi.StopFspipe200Response{}, nil
}

// GetFspipeStatus returns the current status of the fspipe daemon
func (s *ApiService) GetFspipeStatus(ctx context.Context, req oapi.GetFspipeStatusRequestObject) (oapi.GetFspipeStatusResponseObject, error) {
	fspipe.mu.RLock()
	defer fspipe.mu.RUnlock()

	if !fspipe.running {
		return oapi.GetFspipeStatus200JSONResponse(oapi.FspipeStatus{
			Running: false,
		}), nil
	}

	status := oapi.FspipeStatus{
		Running:   true,
		MountPath: &fspipe.mountPath,
	}

	// Set transport mode
	mode := oapi.FspipeStatusTransportMode(fspipe.transportMode)
	status.TransportMode = &mode

	// Set transport state
	if fspipe.transport != nil {
		stateStr := fspipe.transport.State().String()
		var state oapi.FspipeStatusTransportState
		switch stateStr {
		case "connected":
			state = oapi.Connected
		case "reconnecting":
			state = oapi.Reconnecting
		default:
			state = oapi.Disconnected
		}
		status.TransportState = &state

		// Get stats
		rawStats := fspipe.transport.Stats()
		stats := make(map[string]interface{})
		for k, v := range rawStats {
			stats[k] = v
		}
		status.Stats = &stats
	}

	// Set endpoint info
	if fspipe.transportMode == "websocket" && fspipe.wsEndpoint != "" {
		status.WsEndpoint = &fspipe.wsEndpoint
	}
	if fspipe.transportMode == "s3" && fspipe.s3Bucket != "" {
		status.S3Bucket = &fspipe.s3Bucket
	}

	return oapi.GetFspipeStatus200JSONResponse(status), nil
}
