package benchmarks

import "time"

// BenchmarkResults represents the complete benchmark output
type BenchmarkResults struct {
	Timestamp       time.Time              `json:"timestamp"`
	DurationSeconds int                    `json:"duration_seconds"`
	System          SystemInfo             `json:"system"`
	Results         ComponentResults       `json:"results"`
	Errors          []string               `json:"errors"`
	StartupTiming   *StartupTimingResults  `json:"startup_timing,omitempty"`
}

// SystemInfo contains system information
type SystemInfo struct {
	CPUCount        int    `json:"cpu_count"`
	MemoryTotalMB   int64  `json:"memory_total_mb"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
}

// ComponentResults contains results for each benchmarked component
type ComponentResults struct {
	CDPProxy           *CDPProxyResults          `json:"cdp_proxy,omitempty"`
	WebRTCLiveView     *WebRTCLiveViewResults    `json:"webrtc_live_view,omitempty"`
	Recording          *RecordingResults         `json:"recording,omitempty"`
	ScreenshotLatency  *ScreenshotLatencyResults `json:"screenshot_latency,omitempty"`
}

// CDPProxyResults contains CDP proxy benchmark results
type CDPProxyResults struct {
	ThroughputMsgsPerSec   float64                `json:"throughput_msgs_per_sec"`
	LatencyMS              LatencyMetrics         `json:"latency_ms"`
	ConcurrentConnections  int                    `json:"concurrent_connections"`
	MemoryMB               MemoryMetrics          `json:"memory_mb"`
	MessageSizeBytes       MessageSizeMetrics     `json:"message_size_bytes"`
	Scenarios              []CDPScenarioResult    `json:"scenarios,omitempty"`
	ProxiedEndpoint        *CDPEndpointResults    `json:"proxied_endpoint,omitempty"`
	DirectEndpoint         *CDPEndpointResults    `json:"direct_endpoint,omitempty"`
	ProxyOverheadPercent   float64                `json:"proxy_overhead_percent,omitempty"`
}

// CDPEndpointResults contains results for a specific CDP endpoint (proxied or direct)
type CDPEndpointResults struct {
	EndpointURL          string                 `json:"endpoint_url"`
	ThroughputMsgsPerSec float64                `json:"throughput_msgs_per_sec"`
	LatencyMS            LatencyMetrics         `json:"latency_ms"`
	Scenarios            []CDPScenarioResult    `json:"scenarios,omitempty"`
}

// CDPScenarioResult contains results for a specific CDP scenario
type CDPScenarioResult struct {
	Name                 string           `json:"name"`
	Description          string           `json:"description"`
	Category             string           `json:"category"`
	OperationCount       int64            `json:"operation_count"`
	ThroughputOpsPerSec  float64          `json:"throughput_ops_per_sec"`
	LatencyMS            LatencyMetrics   `json:"latency_ms"`
	SuccessRate          float64          `json:"success_rate"`
}

// WebRTCLiveViewResults contains WebRTC live view benchmark results
type WebRTCLiveViewResults struct {
	FrameRateFPS          FrameRateMetrics       `json:"frame_rate_fps"`
	FrameLatencyMS        LatencyMetrics         `json:"frame_latency_ms"`
	BitrateKbps           BitrateMetrics         `json:"bitrate_kbps"`
	ConnectionSetupMS     float64                `json:"connection_setup_ms"`
	ConcurrentViewers     int                    `json:"concurrent_viewers"`
	CPUUsagePercent       float64                `json:"cpu_usage_percent"`
	MemoryMB              MemoryMetrics          `json:"memory_mb"`
}

// RecordingResults contains recording benchmark results
type RecordingResults struct {
	CPUOverheadPercent    float64                       `json:"cpu_overhead_percent"`
	MemoryOverheadMB      float64                       `json:"memory_overhead_mb"`
	FramesCaptured        int64                         `json:"frames_captured"`
	FramesDropped         int64                         `json:"frames_dropped"`
	AvgEncodingLagMS      float64                       `json:"avg_encoding_lag_ms"`
	DiskWriteMBPS         float64                       `json:"disk_write_mbps"`
	ConcurrentRecordings  int                           `json:"concurrent_recordings"`
	FrameRateImpact       *RecordingFrameRateImpact     `json:"frame_rate_impact,omitempty"`
}

// RecordingFrameRateImpact shows how recording affects live view frame rate
type RecordingFrameRateImpact struct {
	BeforeRecordingFPS float64 `json:"before_recording_fps"`
	DuringRecordingFPS float64 `json:"during_recording_fps"`
	ImpactPercent      float64 `json:"impact_percent"`
}

// LatencyMetrics contains latency percentiles
type LatencyMetrics struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

// FrameRateMetrics contains frame rate statistics
type FrameRateMetrics struct {
	Target   float64 `json:"target"`
	Achieved float64 `json:"achieved"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
}

// BitrateMetrics contains bitrate statistics
type BitrateMetrics struct {
	Target float64 `json:"target"`
	Actual float64 `json:"actual"`
}

// MemoryMetrics contains memory usage statistics
type MemoryMetrics struct {
	Baseline      float64 `json:"baseline"`
	PerConnection float64 `json:"per_connection,omitempty"`
	PerViewer     float64 `json:"per_viewer,omitempty"`
}

// MessageSizeMetrics contains message size statistics
type MessageSizeMetrics struct {
	Min int `json:"min"`
	Max int `json:"max"`
	Avg int `json:"avg"`
}

// BenchmarkComponent represents which component to benchmark
type BenchmarkComponent string

const (
	ComponentCDP        BenchmarkComponent = "cdp"
	ComponentWebRTC     BenchmarkComponent = "webrtc"
	ComponentRecording  BenchmarkComponent = "recording"
	ComponentScreenshot BenchmarkComponent = "screenshot"
	ComponentAll        BenchmarkComponent = "all"
)

// BenchmarkConfig contains configuration for running benchmarks
type BenchmarkConfig struct {
	Components []BenchmarkComponent
	Duration   time.Duration
}
