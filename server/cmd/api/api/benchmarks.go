package api

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/onkernel/kernel-images/server/lib/benchmarks"
	"github.com/onkernel/kernel-images/server/lib/logger"
	oapi "github.com/onkernel/kernel-images/server/lib/oapi"
)

// RunBenchmark implements the benchmark endpoint
func (s *ApiService) RunBenchmark(ctx context.Context, request oapi.RunBenchmarkRequestObject) (oapi.RunBenchmarkResponseObject, error) {
	log := logger.FromContext(ctx)
	log.Info("starting benchmark run")

	// Parse parameters
	components := parseComponents(request.Params.Components)
	duration := parseDuration(request.Params.Duration)

	// Initialize results
	startTime := time.Now()
	results := &benchmarks.BenchmarkResults{
		Timestamp:       startTime,
		DurationSeconds: int(duration.Seconds()),
		System:          getSystemInfo(),
		Results:         benchmarks.ComponentResults{},
		Errors:          []string{},
	}

	// Run requested benchmarks
	for _, component := range components {
		switch component {
		case benchmarks.ComponentCDP:
			if cdpResults, err := s.runCDPBenchmark(ctx, duration); err != nil {
				log.Error("CDP benchmark failed", "err", err)
				results.Errors = append(results.Errors, fmt.Sprintf("CDP: %v", err))
			} else {
				results.Results.CDPProxy = cdpResults
			}

		case benchmarks.ComponentWebRTC:
			if webrtcResults, err := s.runWebRTCBenchmark(ctx, duration); err != nil {
				log.Error("WebRTC benchmark failed", "err", err)
				results.Errors = append(results.Errors, fmt.Sprintf("WebRTC: %v", err))
			} else {
				results.Results.WebRTCLiveView = webrtcResults
			}

		case benchmarks.ComponentRecording:
			if recordingResults, err := s.runRecordingBenchmark(ctx, duration); err != nil {
				log.Error("Recording benchmark failed", "err", err)
				results.Errors = append(results.Errors, fmt.Sprintf("Recording: %v", err))
			} else {
				results.Results.Recording = recordingResults
			}
		}
	}

	log.Info("benchmark run completed", "duration", time.Since(startTime))

	// Convert to oapi response type
	return oapi.RunBenchmark200JSONResponse(convertToOAPIBenchmarkResults(results)), nil
}

// convertToOAPIBenchmarkResults converts benchmarks.BenchmarkResults to oapi.BenchmarkResults
func convertToOAPIBenchmarkResults(results *benchmarks.BenchmarkResults) oapi.BenchmarkResults {
	resp := oapi.BenchmarkResults{
		Timestamp:       &results.Timestamp,
		DurationSeconds: &results.DurationSeconds,
		System:          convertSystemInfo(results.System),
		Results:         convertComponentResults(results.Results),
		Errors:          &results.Errors,
	}
	return resp
}

func convertSystemInfo(info benchmarks.SystemInfo) *oapi.SystemInfo {
	memTotal := int(info.MemoryTotalMB)
	return &oapi.SystemInfo{
		Arch:          &info.Arch,
		CpuCount:      &info.CPUCount,
		MemoryTotalMb: &memTotal,
		Os:            &info.OS,
	}
}

func convertComponentResults(results benchmarks.ComponentResults) *oapi.ComponentResults {
	resp := &oapi.ComponentResults{}

	if results.CDPProxy != nil {
		resp.CdpProxy = convertCDPProxyResults(results.CDPProxy)
	}

	if results.WebRTCLiveView != nil {
		resp.WebrtcLiveView = convertWebRTCResults(results.WebRTCLiveView)
	}

	if results.Recording != nil {
		resp.Recording = convertRecordingResults(results.Recording)
	}

	return resp
}

func convertCDPProxyResults(cdp *benchmarks.CDPProxyResults) *oapi.CDPProxyResults {
	throughput := float32(cdp.ThroughputMsgsPerSec)
	return &oapi.CDPProxyResults{
		ThroughputMsgsPerSec:  &throughput,
		LatencyMs:             convertLatencyMetrics(cdp.LatencyMS),
		ConcurrentConnections: &cdp.ConcurrentConnections,
		MemoryMb:              convertMemoryMetrics(cdp.MemoryMB),
		MessageSizeBytes:      convertMessageSizeMetrics(cdp.MessageSizeBytes),
	}
}

func convertWebRTCResults(webrtc *benchmarks.WebRTCLiveViewResults) *oapi.WebRTCLiveViewResults {
	setupMs := float32(webrtc.ConnectionSetupMS)
	cpuPct := float32(webrtc.CPUUsagePercent)
	return &oapi.WebRTCLiveViewResults{
		FrameRateFps:      convertFrameRateMetrics(webrtc.FrameRateFPS),
		FrameLatencyMs:    convertLatencyMetrics(webrtc.FrameLatencyMS),
		BitrateKbps:       convertBitrateMetrics(webrtc.BitrateKbps),
		ConnectionSetupMs: &setupMs,
		ConcurrentViewers: &webrtc.ConcurrentViewers,
		CpuUsagePercent:   &cpuPct,
		MemoryMb:          convertMemoryMetrics(webrtc.MemoryMB),
	}
}

func convertRecordingResults(rec *benchmarks.RecordingResults) *oapi.RecordingResults {
	cpuOverhead := float32(rec.CPUOverheadPercent)
	memOverhead := float32(rec.MemoryOverheadMB)
	framesCaptured := int(rec.FramesCaptured)
	framesDropped := int(rec.FramesDropped)
	encodingLag := float32(rec.AvgEncodingLagMS)
	diskWrite := float32(rec.DiskWriteMBPS)
	return &oapi.RecordingResults{
		CpuOverheadPercent:   &cpuOverhead,
		MemoryOverheadMb:     &memOverhead,
		FramesCaptured:       &framesCaptured,
		FramesDropped:        &framesDropped,
		AvgEncodingLagMs:     &encodingLag,
		DiskWriteMbps:        &diskWrite,
		ConcurrentRecordings: &rec.ConcurrentRecordings,
	}
}

func convertLatencyMetrics(lat benchmarks.LatencyMetrics) *oapi.LatencyMetrics {
	p50 := float32(lat.P50)
	p95 := float32(lat.P95)
	p99 := float32(lat.P99)
	return &oapi.LatencyMetrics{
		P50: &p50,
		P95: &p95,
		P99: &p99,
	}
}

func convertFrameRateMetrics(fr benchmarks.FrameRateMetrics) *oapi.FrameRateMetrics {
	target := float32(fr.Target)
	achieved := float32(fr.Achieved)
	min := float32(fr.Min)
	max := float32(fr.Max)
	return &oapi.FrameRateMetrics{
		Target:   &target,
		Achieved: &achieved,
		Min:      &min,
		Max:      &max,
	}
}

func convertBitrateMetrics(br benchmarks.BitrateMetrics) *oapi.BitrateMetrics {
	target := float32(br.Target)
	actual := float32(br.Actual)
	return &oapi.BitrateMetrics{
		Target: &target,
		Actual: &actual,
	}
}

func convertMemoryMetrics(mem benchmarks.MemoryMetrics) *oapi.MemoryMetrics {
	baseline := float32(mem.Baseline)
	result := &oapi.MemoryMetrics{
		Baseline: &baseline,
	}
	if mem.PerConnection > 0 {
		perConn := float32(mem.PerConnection)
		result.PerConnection = &perConn
	}
	if mem.PerViewer > 0 {
		perViewer := float32(mem.PerViewer)
		result.PerViewer = &perViewer
	}
	return result
}

func convertMessageSizeMetrics(msg benchmarks.MessageSizeMetrics) *oapi.MessageSizeMetrics {
	return &oapi.MessageSizeMetrics{
		Min: &msg.Min,
		Max: &msg.Max,
		Avg: &msg.Avg,
	}
}

func (s *ApiService) runCDPBenchmark(ctx context.Context, duration time.Duration) (*benchmarks.CDPProxyResults, error) {
	log := logger.FromContext(ctx)
	log.Info("running CDP benchmark")

	// CDP proxy is exposed on port 9222
	cdpProxyURL := "http://localhost:9222"
	concurrency := 5 // Number of concurrent connections to test

	benchmark := benchmarks.NewCDPRuntimeBenchmark(log, cdpProxyURL, concurrency)
	return benchmark.Run(ctx, duration)
}

func (s *ApiService) runWebRTCBenchmark(ctx context.Context, duration time.Duration) (*benchmarks.WebRTCLiveViewResults, error) {
	log := logger.FromContext(ctx)
	log.Info("running WebRTC benchmark")

	// Neko is typically on localhost:8080
	nekoBaseURL := "http://127.0.0.1:8080"

	benchmark := benchmarks.NewWebRTCBenchmark(log, nekoBaseURL)
	return benchmark.Run(ctx, duration)
}

func (s *ApiService) runRecordingBenchmark(ctx context.Context, duration time.Duration) (*benchmarks.RecordingResults, error) {
	log := logger.FromContext(ctx)
	log.Info("running Recording benchmark")

	profiler := benchmarks.NewRecordingProfiler(log, s.recordManager, s.factory)
	return profiler.Run(ctx, duration)
}

func parseComponents(componentsParam *string) []benchmarks.BenchmarkComponent {
	if componentsParam == nil {
		return []benchmarks.BenchmarkComponent{benchmarks.ComponentAll}
	}

	componentsStr := *componentsParam
	if componentsStr == "" || componentsStr == "all" {
		return []benchmarks.BenchmarkComponent{
			benchmarks.ComponentCDP,
			benchmarks.ComponentWebRTC,
			benchmarks.ComponentRecording,
		}
	}

	// Parse comma-separated list
	parts := strings.Split(componentsStr, ",")
	components := make([]benchmarks.BenchmarkComponent, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "cdp":
			components = append(components, benchmarks.ComponentCDP)
		case "webrtc":
			components = append(components, benchmarks.ComponentWebRTC)
		case "recording":
			components = append(components, benchmarks.ComponentRecording)
		case "all":
			return []benchmarks.BenchmarkComponent{
				benchmarks.ComponentCDP,
				benchmarks.ComponentWebRTC,
				benchmarks.ComponentRecording,
			}
		}
	}

	if len(components) == 0 {
		// Default to all if none specified
		return []benchmarks.BenchmarkComponent{
			benchmarks.ComponentCDP,
			benchmarks.ComponentWebRTC,
			benchmarks.ComponentRecording,
		}
	}

	return components
}

func parseDuration(durationParam *int) time.Duration {
	if durationParam == nil {
		return 10 * time.Second
	}

	duration := *durationParam
	if duration < 1 {
		duration = 1
	} else if duration > 60 {
		duration = 60
	}

	return time.Duration(duration) * time.Second
}

func getSystemInfo() benchmarks.SystemInfo {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return benchmarks.SystemInfo{
		CPUCount:      runtime.NumCPU(),
		MemoryTotalMB: int64(memStats.Sys / 1024 / 1024),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
	}
}
