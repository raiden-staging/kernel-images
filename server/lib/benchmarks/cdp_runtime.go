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
	b.logger.Info("starting CDP benchmark", "concurrency", b.concurrency)

	// Get baseline memory
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Endpoints to test
	proxiedURL := b.proxyURL         // http://localhost:9222
	directURL := "http://localhost:9223" // Direct Chrome

	// Benchmark proxied endpoint
	b.logger.Info("benchmarking proxied CDP endpoint", "url", proxiedURL)
	proxiedResults, err := b.benchmarkEndpoint(ctx, proxiedURL)
	if err != nil {
		return nil, fmt.Errorf("proxied endpoint failed: %w", err)
	}

	// Benchmark direct endpoint
	b.logger.Info("benchmarking direct CDP endpoint", "url", directURL)
	directResults, err := b.benchmarkEndpoint(ctx, directURL)
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

// benchmarkEndpoint benchmarks a single CDP endpoint
func (b *CDPRuntimeBenchmark) benchmarkEndpoint(ctx context.Context, baseURL string) (*CDPEndpointResults, error) {
	// Fetch WebSocket URL
	wsURL, err := fetchBrowserWebSocketURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get WebSocket URL: %w", err)
	}
	b.logger.Info("resolved WebSocket URL", "url", wsURL)

	// Define scenarios to benchmark (run each independently for distinct metrics)
	scenarios := []scenarioDef{
		{
			Name:        "Browser.getVersion",
			Category:    "Browser",
			Description: "Get browser version information (baseline latency)",
			Method:      "Browser.getVersion",
			Params:      nil,
			Duration:    500 * time.Millisecond,
			RequiresPage: false, // Browser-level command
		},
		{
			Name:        "Runtime.evaluate-simple",
			Category:    "Runtime",
			Description: "Evaluate simple JavaScript expression",
			Method:      "Runtime.evaluate",
			Params:      map[string]interface{}{"expression": "1+1"},
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "Runtime.evaluate-complex",
			Category:    "Runtime",
			Description: "Evaluate complex JavaScript with DOM access",
			Method:      "Runtime.evaluate",
			Params:      map[string]interface{}{"expression": "document.querySelector('body').children.length"},
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "DOM.getDocument",
			Category:    "DOM",
			Description: "Retrieve the root DOM document structure",
			Method:      "DOM.getDocument",
			Params:      map[string]interface{}{"depth": 1},
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "Page.getNavigationHistory",
			Category:    "Page",
			Description: "Retrieve page navigation history",
			Method:      "Page.getNavigationHistory",
			Params:      nil,
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "Page.captureScreenshot",
			Category:    "Page",
			Description: "Capture page screenshot (heavy I/O operation)",
			Method:      "Page.captureScreenshot",
			Params:      map[string]interface{}{"format": "png", "quality": 80},
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "Network.getCookies",
			Category:    "Network",
			Description: "Retrieve browser cookies",
			Method:      "Network.getCookies",
			Params:      nil,
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
		{
			Name:        "Performance.getMetrics",
			Category:    "Performance",
			Description: "Get runtime performance metrics",
			Method:      "Performance.getMetrics",
			Params:      nil,
			Duration:    500 * time.Millisecond,
			RequiresPage: true,
		},
	}

	// Connect to browser WebSocket
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Get or create a page target and attach to it
	sessionID, err := b.attachToPageTarget(ctx, conn)
	if err != nil {
		b.logger.Warn("failed to attach to page target, some scenarios may fail", "err", err)
		sessionID = "" // Continue without session - browser-level commands will still work
	} else {
		b.logger.Info("attached to page target", "sessionId", sessionID)
	}

	// Run each scenario independently
	scenarioResults := make([]CDPScenarioResult, 0, len(scenarios))
	totalOps := int64(0)
	totalDuration := 0.0

	for _, scenario := range scenarios {
		b.logger.Info("running scenario", "name", scenario.Name, "duration", scenario.Duration)

		result := b.runScenarioIndependently(ctx, conn, sessionID, scenario)
		scenarioResults = append(scenarioResults, result)
		totalOps += result.OperationCount
		totalDuration += scenario.Duration.Seconds()
	}

	// Calculate overall throughput
	totalThroughput := float64(totalOps) / totalDuration

	return &CDPEndpointResults{
		EndpointURL:               baseURL,
		TotalThroughputOpsPerSec:  totalThroughput,
		Scenarios:                 scenarioResults,
	}, nil
}

// scenarioDef defines a CDP scenario to benchmark
type scenarioDef struct {
	Name         string
	Category     string
	Description  string
	Method       string
	Params       map[string]interface{}
	Duration     time.Duration
	RequiresPage bool // If true, requires page target attachment
}

// attachToPageTarget finds a page target and attaches to it, returning the sessionId
func (b *CDPRuntimeBenchmark) attachToPageTarget(ctx context.Context, conn *websocket.Conn) (string, error) {
	// Get list of targets
	response, err := sendCDPCommand(ctx, conn, "", 100000, "Target.getTargets", nil)
	if err != nil {
		return "", fmt.Errorf("Target.getTargets failed: %w", err)
	}

	// Find a page target
	targetInfos, ok := response.Result["targetInfos"].([]interface{})
	if !ok || len(targetInfos) == 0 {
		return "", fmt.Errorf("no targets found")
	}

	var pageTargetID string
	for _, info := range targetInfos {
		targetInfo, ok := info.(map[string]interface{})
		if !ok {
			continue
		}
		targetType, _ := targetInfo["type"].(string)
		if targetType == "page" {
			pageTargetID, _ = targetInfo["targetId"].(string)
			break
		}
	}

	if pageTargetID == "" {
		return "", fmt.Errorf("no page target found")
	}

	// Attach to the page target
	response, err = sendCDPCommand(ctx, conn, "", 100001, "Target.attachToTarget", map[string]interface{}{
		"targetId": pageTargetID,
		"flatten":  true, // Use flattened protocol
	})
	if err != nil {
		return "", fmt.Errorf("Target.attachToTarget failed: %w", err)
	}

	sessionID, ok := response.Result["sessionId"].(string)
	if !ok {
		return "", fmt.Errorf("no sessionId in attach response")
	}

	// Enable required domains for the session
	domains := []string{"Runtime", "DOM", "Page", "Network", "Performance"}
	msgID := 100002
	for _, domain := range domains {
		_, err := sendCDPCommand(ctx, conn, sessionID, msgID, domain+".enable", nil)
		if err != nil {
			b.logger.Warn("failed to enable domain", "domain", domain, "err", err)
		}
		msgID++
	}

	return sessionID, nil
}

// runScenarioIndependently runs a single scenario independently and returns its metrics
func (b *CDPRuntimeBenchmark) runScenarioIndependently(ctx context.Context, conn *websocket.Conn, sessionID string, scenario scenarioDef) CDPScenarioResult {
	scenarioCtx, cancel := context.WithTimeout(ctx, scenario.Duration)
	defer cancel()

	var (
		operations  atomic.Int64
		failures    atomic.Int64
		latencies   []float64
		latenciesMu sync.Mutex
		wg          sync.WaitGroup
	)

	startTime := time.Now()

	// Run concurrent workers for this scenario
	for i := 0; i < b.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			msgID := workerID * 1000000 // Unique message ID space per worker

			for {
				select {
				case <-scenarioCtx.Done():
					return
				default:
				}

				msgID++
				start := time.Now()

				// If scenario requires page session but we don't have one, skip
				effectiveSessionID := sessionID
				if scenario.RequiresPage && sessionID == "" {
					// Fail immediately
					failures.Add(1)
					operations.Add(1)
					continue
				}

				response, err := sendCDPCommand(scenarioCtx, conn, effectiveSessionID, msgID, scenario.Method, scenario.Params)
				latency := time.Since(start)
				latencyMs := float64(latency.Microseconds()) / 1000.0

				operations.Add(1)

				if err != nil {
					failures.Add(1)
					if response == nil || response.Error == nil {
						// Connection error, bail out
						if scenarioCtx.Err() == nil {
							b.logger.Error("connection error in scenario", "scenario", scenario.Name, "worker", workerID, "err", err)
						}
						return
					}
					// CDP error - log and continue
					if response.Error != nil {
						b.logger.Debug("CDP error", "scenario", scenario.Name, "code", response.Error.Code, "msg", response.Error.Message)
					}
				}

				latenciesMu.Lock()
				latencies = append(latencies, latencyMs)
				latenciesMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	ops := operations.Load()
	fails := failures.Load()

	// Calculate success rate
	successRate := 100.0
	if ops > 0 {
		successRate = (float64(ops-fails) / float64(ops)) * 100.0
	}

	// Calculate throughput
	throughput := float64(ops) / elapsed.Seconds()

	// Calculate latency percentiles (only from successful operations if possible)
	latencyMetrics := calculatePercentiles(latencies)

	return CDPScenarioResult{
		Name:                scenario.Name,
		Description:         scenario.Description,
		Category:            scenario.Category,
		OperationCount:      ops,
		ThroughputOpsPerSec: throughput,
		LatencyMS:           latencyMetrics,
		SuccessRate:         successRate,
	}
}

// fetchBrowserWebSocketURL fetches the browser WebSocket URL from /json/version
func fetchBrowserWebSocketURL(baseURL string) (string, error) {
	// Ensure baseURL has a scheme
	if u, err := url.Parse(baseURL); err == nil && u.Scheme == "" {
		baseURL = "http://" + baseURL
	}

	// Construct /json/version URL
	versionURL := baseURL + "/json/version"

	// Make HTTP request
	resp, err := http.Get(versionURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", versionURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, versionURL, string(body))
	}

	// Parse JSON response
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

// sendCDPCommand sends a CDP command and waits for the response
// sessionID can be empty string for browser-level commands
func sendCDPCommand(ctx context.Context, conn *websocket.Conn, sessionID string, id int, method string, params map[string]interface{}) (*CDPMessage, error) {
	request := CDPMessage{
		ID:     id,
		Method: method,
		Params: params,
	}

	// If sessionId is provided, add it to the request
	if sessionID != "" {
		request.SessionID = sessionID
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

// calculatePercentiles calculates latency percentiles from a slice of values
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
	ID        int                    `json:"id"`
	SessionID string                 `json:"sessionId,omitempty"`
	Method    string                 `json:"method,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"`
	Result    map[string]interface{} `json:"result,omitempty"`
	Error     *CDPError              `json:"error,omitempty"`
}

// CDPError represents a CDP error response
type CDPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
