package benchmarks

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ScreenshotLatencyBenchmark measures screenshot capture performance
type ScreenshotLatencyBenchmark struct {
	logger     *slog.Logger
	apiBaseURL string
}

// NewScreenshotLatencyBenchmark creates a new screenshot latency benchmark
func NewScreenshotLatencyBenchmark(logger *slog.Logger, apiBaseURL string) *ScreenshotLatencyBenchmark {
	return &ScreenshotLatencyBenchmark{
		logger:     logger,
		apiBaseURL: apiBaseURL,
	}
}

// ScreenshotLatencyResults contains screenshot benchmark results
type ScreenshotLatencyResults struct {
	TotalScreenshots     int            `json:"total_screenshots"`
	SuccessfulCaptures   int            `json:"successful_captures"`
	FailedCaptures       int            `json:"failed_captures"`
	SuccessRate          float64        `json:"success_rate"`
	LatencyMS            LatencyMetrics `json:"latency_ms"`
	AvgImageSizeBytes    int64          `json:"avg_image_size_bytes"`
	ThroughputPerSec     float64        `json:"throughput_per_sec"`
}

// Run executes the screenshot latency benchmark
func (b *ScreenshotLatencyBenchmark) Run(ctx context.Context, duration time.Duration) (*ScreenshotLatencyResults, error) {
	b.logger.Info("starting screenshot latency benchmark", "duration", duration)

	benchCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var (
		totalScreenshots   int
		successfulCaptures int
		failedCaptures     int
		totalImageSize     int64
		latencies          []float64
	)

	startTime := time.Now()
	client := &http.Client{Timeout: 10 * time.Second}
	screenshotURL := fmt.Sprintf("%s/computer/screenshot", b.apiBaseURL)

	for {
		select {
		case <-benchCtx.Done():
			goto Done
		default:
		}

		totalScreenshots++
		start := time.Now()

		req, err := http.NewRequestWithContext(benchCtx, "POST", screenshotURL, nil)
		if err != nil {
			b.logger.Error("failed to create screenshot request", "err", err)
			failedCaptures++
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			if benchCtx.Err() != nil {
				goto Done
			}
			b.logger.Error("screenshot request failed", "err", err)
			failedCaptures++
			continue
		}

		if resp.StatusCode != http.StatusOK {
			b.logger.Error("screenshot returned non-200 status", "status", resp.StatusCode)
			resp.Body.Close()
			failedCaptures++
			continue
		}

		// Read response body to measure image size
		imageSize := resp.ContentLength
		resp.Body.Close()

		latency := time.Since(start)
		successfulCaptures++
		totalImageSize += imageSize
		latencies = append(latencies, float64(latency.Milliseconds()))

		// Small delay between captures to avoid overwhelming the system
		time.Sleep(100 * time.Millisecond)
	}

Done:
	elapsed := time.Since(startTime)

	// Calculate metrics
	successRate := 0.0
	if totalScreenshots > 0 {
		successRate = (float64(successfulCaptures) / float64(totalScreenshots)) * 100.0
	}

	avgImageSize := int64(0)
	if successfulCaptures > 0 {
		avgImageSize = totalImageSize / int64(successfulCaptures)
	}

	latencyMetrics := calculatePercentiles(latencies)
	throughput := float64(successfulCaptures) / elapsed.Seconds()

	b.logger.Info("screenshot latency benchmark completed",
		"total", totalScreenshots,
		"successful", successfulCaptures,
		"failed", failedCaptures,
		"success_rate", successRate,
		"avg_image_size_kb", avgImageSize/1024)

	return &ScreenshotLatencyResults{
		TotalScreenshots:   totalScreenshots,
		SuccessfulCaptures: successfulCaptures,
		FailedCaptures:     failedCaptures,
		SuccessRate:        successRate,
		LatencyMS:          latencyMetrics,
		AvgImageSizeBytes:  avgImageSize,
		ThroughputPerSec:   throughput,
	}, nil
}
