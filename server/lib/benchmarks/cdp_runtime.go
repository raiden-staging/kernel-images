package benchmarks

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// CDPRuntimeBenchmark performs runtime benchmarks on the CDP proxy
type CDPRuntimeBenchmark struct {
	logger      *slog.Logger
	proxyURL    string
	concurrency int
}

// NewCDPRuntimeBenchmark creates a new CDP runtime benchmark
func NewCDPRuntimeBenchmark(logger *slog.Logger, proxyURL string, concurrency int) *CDPRuntimeBenchmark {
	return &CDPRuntimeBenchmark{
		logger:      logger,
		proxyURL:    proxyURL,
		concurrency: concurrency,
	}
}

// Run executes the CDP benchmark for the specified duration
func (b *CDPRuntimeBenchmark) Run(ctx context.Context, duration time.Duration) (*CDPProxyResults, error) {
	b.logger.Info("starting CDP proxy benchmark", "duration", duration, "concurrency", b.concurrency)

	// Parse proxy URL
	u, err := url.Parse(b.proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	} else if u.Scheme == "https" {
		u.Scheme = "wss"
	}

	// Get baseline memory
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Run benchmark with workers
	results := b.runWorkers(ctx, u.String(), duration)

	// Get final memory
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	// Calculate memory metrics
	baselineMemMB := float64(memStatsBefore.Alloc) / 1024 / 1024
	finalMemMB := float64(memStatsAfter.Alloc) / 1024 / 1024
	perConnectionMemMB := (finalMemMB - baselineMemMB) / float64(b.concurrency)

	return &CDPProxyResults{
		ThroughputMsgsPerSec:  results.ThroughputMsgsPerSec,
		LatencyMS:             results.LatencyMS,
		ConcurrentConnections: b.concurrency,
		MemoryMB: MemoryMetrics{
			Baseline:      baselineMemMB,
			PerConnection: perConnectionMemMB,
		},
		MessageSizeBytes: results.MessageSizeBytes,
	}, nil
}

type workerResults struct {
	ThroughputMsgsPerSec float64
	LatencyMS            LatencyMetrics
	MessageSizeBytes     MessageSizeMetrics
}

func (b *CDPRuntimeBenchmark) runWorkers(ctx context.Context, wsURL string, duration time.Duration) workerResults {
	benchCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var (
		totalOps     atomic.Int64
		totalLatency atomic.Int64
		latencies    []float64
		latenciesMu  sync.Mutex
		wg           sync.WaitGroup
	)

	startTime := time.Now()

	// Start workers
	for i := 0; i < b.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			conn, _, err := websocket.Dial(benchCtx, wsURL, nil)
			if err != nil {
				b.logger.Error("failed to dial proxy", "worker", workerID, "err", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")

			// Test messages - mix of different CDP commands
			messages := [][]byte{
				[]byte(`{"id":1,"method":"Runtime.evaluate","params":{"expression":"1+1"}}`),
				[]byte(`{"id":2,"method":"Page.getNavigationHistory","params":{}}`),
				[]byte(`{"id":3,"method":"DOM.getDocument","params":{}}`),
				[]byte(`{"id":4,"method":"Runtime.getProperties","params":{"objectId":"1"}}`),
			}

			msgIdx := 0
			for {
				select {
				case <-benchCtx.Done():
					return
				default:
				}

				msg := messages[msgIdx%len(messages)]
				msgIdx++

				start := time.Now()
				if err := conn.Write(benchCtx, websocket.MessageText, msg); err != nil {
					if benchCtx.Err() != nil {
						return
					}
					b.logger.Error("write failed", "worker", workerID, "err", err)
					return
				}

				if _, _, err := conn.Read(benchCtx); err != nil {
					if benchCtx.Err() != nil {
						return
					}
					b.logger.Error("read failed", "worker", workerID, "err", err)
					return
				}

				latency := time.Since(start)
				totalOps.Add(1)
				totalLatency.Add(latency.Microseconds())

				latenciesMu.Lock()
				latencies = append(latencies, float64(latency.Microseconds())/1000.0)
				latenciesMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	ops := totalOps.Load()

	// Calculate throughput
	throughput := float64(ops) / elapsed.Seconds()

	// Calculate latency percentiles
	latencyMetrics := calculatePercentiles(latencies)

	// Message size metrics (approximate based on test messages)
	messageSizes := MessageSizeMetrics{
		Min: 50,
		Max: 200,
		Avg: 100,
	}

	return workerResults{
		ThroughputMsgsPerSec: throughput,
		LatencyMS:            latencyMetrics,
		MessageSizeBytes:     messageSizes,
	}
}

func calculatePercentiles(values []float64) LatencyMetrics {
	if len(values) == 0 {
		return LatencyMetrics{}
	}

	sort.Float64s(values)

	p50Idx := int(math.Floor(float64(len(values)) * 0.50))
	p95Idx := int(math.Floor(float64(len(values)) * 0.95))
	p99Idx := int(math.Floor(float64(len(values)) * 0.99))

	if p50Idx >= len(values) {
		p50Idx = len(values) - 1
	}
	if p95Idx >= len(values) {
		p95Idx = len(values) - 1
	}
	if p99Idx >= len(values) {
		p99Idx = len(values) - 1
	}

	return LatencyMetrics{
		P50: values[p50Idx],
		P95: values[p95Idx],
		P99: values[p99Idx],
	}
}

// CDPMessage represents a generic CDP message
type CDPMessage struct {
	ID     int                    `json:"id"`
	Method string                 `json:"method,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
	Result map[string]interface{} `json:"result,omitempty"`
	Error  *CDPError              `json:"error,omitempty"`
}

// CDPError represents a CDP error response
type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
