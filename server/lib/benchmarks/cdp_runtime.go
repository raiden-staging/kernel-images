package benchmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
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

// Run executes the CDP benchmark
func (b *CDPRuntimeBenchmark) Run(ctx context.Context, duration time.Duration) (*CDPProxyResults, error) {
	const benchmarkDuration = 5 * time.Second
	b.logger.Info("starting CDP benchmark", "duration", benchmarkDuration, "concurrency", b.concurrency)

	// Get baseline memory
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Benchmark proxied endpoint
	proxiedURL := b.proxyURL
	b.logger.Info("benchmarking proxied CDP endpoint", "url", proxiedURL)
	proxiedResults, err := b.benchmarkEndpoint(ctx, proxiedURL, benchmarkDuration)
	if err != nil {
		return nil, fmt.Errorf("proxied endpoint failed: %w", err)
	}

	// Benchmark direct endpoint
	directURL := "http://localhost:9223"
	b.logger.Info("benchmarking direct CDP endpoint", "url", directURL)
	directResults, err := b.benchmarkEndpoint(ctx, directURL, benchmarkDuration)
	if err != nil {
		return nil, fmt.Errorf("direct endpoint failed: %w", err)
	}

	// Get final memory
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	// Calculate memory metrics
	baselineMemMB := float64(memStatsBefore.Alloc) / 1024 / 1024
	finalMemMB := float64(memStatsAfter.Alloc) / 1024 / 1024
	perConnectionMemMB := (finalMemMB - baselineMemMB) / float64(b.concurrency)

	// Calculate proxy overhead
	proxyOverhead := 0.0
	if directResults.TotalThroughputOpsPerSec > 0 {
		proxyOverhead = ((directResults.TotalThroughputOpsPerSec - proxiedResults.TotalThroughputOpsPerSec) / directResults.TotalThroughputOpsPerSec) * 100.0
	}

	return &CDPProxyResults{
		ConcurrentConnections: b.concurrency,
		MemoryMB: MemoryMetrics{
			Baseline:      baselineMemMB,
			PerConnection: perConnectionMemMB,
		},
		ProxiedEndpoint:      proxiedResults,
		DirectEndpoint:       directResults,
		ProxyOverheadPercent: proxyOverhead,
	}, nil
}

// scenarioDef defines a CDP scenario to benchmark
type scenarioDef struct {
	Name        string
	Category    string
	Description string
	Method      string
	Params      map[string]interface{}
}

// benchmarkEndpoint benchmarks a single CDP endpoint
func (b *CDPRuntimeBenchmark) benchmarkEndpoint(ctx context.Context, baseURL string, duration time.Duration) (*CDPEndpointResults, error) {
	// Fetch WebSocket URL
	wsURL, err := fetchBrowserWebSocketURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get WebSocket URL: %w", err)
	}
	b.logger.Info("resolved WebSocket URL", "url", wsURL)

	// Connect to browser WebSocket
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	msgID := 100000

	// Just enable domains directly without creating/attaching to targets
	// The browser WebSocket URL already points to a specific target
	b.logger.Info("enabling CDP domains")
	domains := []string{"Runtime", "DOM", "Page", "Network", "Performance"}
	for _, domain := range domains {
		method := domain + ".enable"
		_, err := sendCDPCommandSimple(ctx, conn, msgID, method, nil)
		if err != nil {
			b.logger.Warn("failed to enable domain", "domain", domain, "err", err)
		} else {
			b.logger.Debug("enabled domain", "domain", domain)
		}
		msgID++
	}

	// Define scenarios to benchmark
	scenarios := []scenarioDef{
		{
			Name:        "Browser.getVersion",
			Category:    "Browser",
			Description: "Get browser version information",
			Method:      "Browser.getVersion",
			Params:      nil,
		},
		{
			Name:        "Runtime.evaluate",
			Category:    "Runtime",
			Description: "Evaluate simple JavaScript expression",
			Method:      "Runtime.evaluate",
			Params:      map[string]interface{}{"expression": "1+1"},
		},
		{
			Name:        "Runtime.evaluate-dom",
			Category:    "Runtime",
			Description: "Evaluate JavaScript with DOM access",
			Method:      "Runtime.evaluate",
			Params:      map[string]interface{}{"expression": "document.title"},
		},
		{
			Name:        "DOM.getDocument",
			Category:    "DOM",
			Description: "Get DOM document tree",
			Method:      "DOM.getDocument",
			Params:      map[string]interface{}{"depth": 0},
		},
		{
			Name:        "Page.getNavigationHistory",
			Category:    "Page",
			Description: "Get page navigation history",
			Method:      "Page.getNavigationHistory",
			Params:      nil,
		},
		{
			Name:        "Network.getCookies",
			Category:    "Network",
			Description: "Get browser cookies",
			Method:      "Network.getCookies",
			Params:      nil,
		},
		{
			Name:        "Performance.getMetrics",
			Category:    "Performance",
			Description: "Get performance metrics",
			Method:      "Performance.getMetrics",
			Params:      nil,
		},
	}

	// Run mixed workload benchmark
	results := b.runMixedWorkload(ctx, conn, scenarios, duration)
	results.EndpointURL = baseURL

	return results, nil
}

// scenarioStats tracks per-scenario statistics
type scenarioStats struct {
	Name        string
	Description string
	Category    string
	Operations  atomic.Int64
	Successes   atomic.Int64
	Failures    atomic.Int64
	Latencies   []float64
	LatenciesMu sync.Mutex
}

// runMixedWorkload runs a mixed workload benchmark
func (b *CDPRuntimeBenchmark) runMixedWorkload(ctx context.Context, conn *websocket.Conn, scenarios []scenarioDef, duration time.Duration) *CDPEndpointResults {
	benchCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	// Initialize scenario tracking
	scenarioStatsMap := make(map[string]*scenarioStats)
	for _, scenario := range scenarios {
		scenarioStatsMap[scenario.Name] = &scenarioStats{
			Name:        scenario.Name,
			Description: scenario.Description,
			Category:    scenario.Category,
			Latencies:   make([]float64, 0, 10000),
		}
	}

	var (
		totalOps atomic.Int64
		wg       sync.WaitGroup
	)

	startTime := time.Now()

	// Start concurrent workers
	for i := 0; i < b.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			msgID := (workerID + 1) * 1000000
			scenarioIdx := 0

			for {
				select {
				case <-benchCtx.Done():
					return
				default:
				}

				// Round-robin through scenarios
				scenario := scenarios[scenarioIdx%len(scenarios)]
				stats := scenarioStatsMap[scenario.Name]
				scenarioIdx++

				msgID++
				start := time.Now()

				// Send CDP command (no sessionID)
				response, err := sendCDPCommandSimple(benchCtx, conn, msgID, scenario.Method, scenario.Params)

				latency := time.Since(start)
				latencyMs := float64(latency.Microseconds()) / 1000.0

				stats.Operations.Add(1)
				totalOps.Add(1)

				// Record latency
				stats.LatenciesMu.Lock()
				stats.Latencies = append(stats.Latencies, latencyMs)
				stats.LatenciesMu.Unlock()

				// Check for errors
				if err != nil {
					// Check if it's a CDP error vs connection error
					if response != nil && response.Error != nil {
						// CDP error - log and CONTINUE (don't exit)
						stats.Failures.Add(1)
						b.logger.Debug("CDP error", "scenario", scenario.Name, "code", response.Error.Code, "msg", response.Error.Message)
						continue
					}
					// Connection error - exit worker
					if benchCtx.Err() == nil {
						b.logger.Error("connection error", "worker", workerID, "scenario", scenario.Name, "err", err)
					}
					return
				}

				// Success
				stats.Successes.Add(1)
			}
		}(i)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	totalOpsCount := totalOps.Load()

	b.logger.Info("benchmark completed", "duration", elapsed, "total_ops", totalOpsCount)

	// Calculate overall throughput
	totalThroughput := float64(totalOpsCount) / elapsed.Seconds()

	// Calculate per-scenario results
	scenarioResults := make([]CDPScenarioResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		stats := scenarioStatsMap[scenario.Name]
		ops := stats.Operations.Load()
		successes := stats.Successes.Load()

		// Calculate success rate
		successRate := 0.0
		if ops > 0 {
			successRate = (float64(successes) / float64(ops)) * 100.0
		}

		// Calculate throughput
		throughput := float64(ops) / elapsed.Seconds()

		// Calculate latency percentiles
		latencyMetrics := calculatePercentiles(stats.Latencies)

		scenarioResults = append(scenarioResults, CDPScenarioResult{
			Name:                scenario.Name,
			Description:         scenario.Description,
			Category:            scenario.Category,
			OperationCount:      ops,
			ThroughputOpsPerSec: throughput,
			LatencyMS:           latencyMetrics,
			SuccessRate:         successRate,
		})
	}

	return &CDPEndpointResults{
		EndpointURL:              "",
		TotalThroughputOpsPerSec: totalThroughput,
		Scenarios:                scenarioResults,
	}
}

// fetchBrowserWebSocketURL fetches the browser WebSocket URL from /json/version
func fetchBrowserWebSocketURL(baseURL string) (string, error) {
	if u, err := url.Parse(baseURL); err == nil && u.Scheme == "" {
		baseURL = "http://" + baseURL
	}

	versionURL := baseURL + "/json/version"

	resp, err := http.Get(versionURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", versionURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, versionURL, string(body))
	}

	var versionInfo struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil {
		return "", fmt.Errorf("failed to decode JSON from %s: %w", versionURL, err)
	}

	if versionInfo.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("webSocketDebuggerUrl not found in response from %s", versionURL)
	}

	return versionInfo.WebSocketDebuggerURL, nil
}

// sendCDPCommandSimple sends a CDP command without sessionID
func sendCDPCommandSimple(ctx context.Context, conn *websocket.Conn, id int, method string, params map[string]interface{}) (*CDPMessage, error) {
	request := CDPMessage{
		ID:     id,
		Method: method,
		Params: params,
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, requestBytes); err != nil {
		return nil, fmt.Errorf("failed to write: %w", err)
	}

	_, responseBytes, err := conn.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	var response CDPMessage
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if response.Error != nil {
		return &response, fmt.Errorf("CDP error: %s (code %d)", response.Error.Message, response.Error.Code)
	}

	return &response, nil
}

// calculatePercentiles calculates latency percentiles
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
