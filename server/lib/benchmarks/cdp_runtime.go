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
	"strings"
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

// Run executes the CDP benchmark for a fixed 5-second duration
// Tests both proxied (9222) and direct (9223) CDP endpoints
func (b *CDPRuntimeBenchmark) Run(ctx context.Context, duration time.Duration) (*CDPProxyResults, error) {
	// Fixed 5-second duration for CDP benchmarks (measures req/sec throughput)
	const cdpDuration = 5 * time.Second
	b.logger.Info("starting CDP benchmark (proxied + direct)", "duration", cdpDuration, "concurrency", b.concurrency)

	// Get baseline memory
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Parse proxy URL and derive direct URL
	proxiedURL := b.proxyURL
	directURL := "http://localhost:9223" // Direct Chrome CDP endpoint

	// Fetch WebSocket URLs from /json/version endpoints
	proxiedWSURL, err := fetchCDPWebSocketURL(proxiedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxied CDP WebSocket URL: %w", err)
	}
	b.logger.Info("resolved proxied WebSocket URL", "url", proxiedWSURL)

	directWSURL, err := fetchCDPWebSocketURL(directURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get direct CDP WebSocket URL: %w", err)
	}
	b.logger.Info("resolved direct WebSocket URL", "url", directWSURL)

	// Benchmark proxied endpoint (kernel-images proxy on port 9222)
	b.logger.Info("benchmarking proxied CDP endpoint", "url", proxiedURL)
	proxiedResults := b.runWorkers(ctx, proxiedWSURL, cdpDuration)

	// Benchmark direct endpoint (Chrome CDP on port 9223)
	b.logger.Info("benchmarking direct CDP endpoint", "url", directURL)
	directResults := b.runWorkers(ctx, directWSURL, cdpDuration)

	// Get final memory
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)

	// Calculate memory metrics
	baselineMemMB := float64(memStatsBefore.Alloc) / 1024 / 1024
	finalMemMB := float64(memStatsAfter.Alloc) / 1024 / 1024
	perConnectionMemMB := (finalMemMB - baselineMemMB) / float64(b.concurrency)

	// Calculate proxy overhead
	proxyOverhead := 0.0
	if directResults.ThroughputMsgsPerSec > 0 {
		proxyOverhead = ((directResults.ThroughputMsgsPerSec - proxiedResults.ThroughputMsgsPerSec) / directResults.ThroughputMsgsPerSec) * 100.0
	}

	// Return results with both direct and proxied endpoint metrics
	// Overall metrics use proxied results for backward compatibility
	return &CDPProxyResults{
		ThroughputMsgsPerSec:  proxiedResults.ThroughputMsgsPerSec,
		LatencyMS:             proxiedResults.LatencyMS,
		ConcurrentConnections: b.concurrency,
		MemoryMB: MemoryMetrics{
			Baseline:      baselineMemMB,
			PerConnection: perConnectionMemMB,
		},
		MessageSizeBytes: proxiedResults.MessageSizeBytes,
		// No root-level Scenarios - they're in ProxiedEndpoint and DirectEndpoint
		ProxiedEndpoint: &CDPEndpointResults{
			EndpointURL:          proxiedURL,
			ThroughputMsgsPerSec: proxiedResults.ThroughputMsgsPerSec,
			LatencyMS:            proxiedResults.LatencyMS,
			Scenarios:            proxiedResults.Scenarios,
		},
		DirectEndpoint: &CDPEndpointResults{
			EndpointURL:          directURL,
			ThroughputMsgsPerSec: directResults.ThroughputMsgsPerSec,
			LatencyMS:            directResults.LatencyMS,
			Scenarios:            directResults.Scenarios,
		},
		ProxyOverheadPercent: proxyOverhead,
	}, nil
}

// fetchCDPWebSocketURL fetches the WebSocket debugger URL from a CDP endpoint
// by querying the /json/version endpoint, following the standard CDP protocol
func fetchCDPWebSocketURL(baseURL string) (string, error) {
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

type workerResults struct {
	ThroughputMsgsPerSec float64
	LatencyMS            LatencyMetrics
	MessageSizeBytes     MessageSizeMetrics
	Scenarios            []CDPScenarioResult
}

type scenarioStats struct {
	Name        string
	Description string
	Category    string
	Operations  atomic.Int64
	Failures    atomic.Int64
	Latencies   []float64
	LatenciesMu sync.Mutex
}

func (b *CDPRuntimeBenchmark) runWorkers(ctx context.Context, wsURL string, duration time.Duration) workerResults {
	benchCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	// Define test scenario generators that will use session context
	type testScenarioGen struct {
		Name        string
		Description string
		Category    string
		GenFunc     func(*cdpSessionContext) (string, map[string]interface{}) // returns method, params
	}

	scenarioGens := []testScenarioGen{
		// Runtime operations - JavaScript evaluation and object inspection
		{
			Name:        "Runtime.evaluate",
			Description: "Evaluate simple JavaScript expression",
			Category:    "Runtime",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Runtime.evaluate", map[string]interface{}{"expression": "1+1"}
			},
		},
		{
			Name:        "Runtime.evaluate-complex",
			Description: "Evaluate complex JavaScript with DOM access",
			Category:    "Runtime",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Runtime.evaluate", map[string]interface{}{"expression": "document.querySelector('body').children.length"}
			},
		},
		{
			Name:        "Runtime.getProperties",
			Description: "Get runtime object properties",
			Category:    "Runtime",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Runtime.getProperties", map[string]interface{}{"objectId": sess.objectID}
			},
		},
		{
			Name:        "Runtime.callFunctionOn",
			Description: "Call function on remote object",
			Category:    "Runtime",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Runtime.callFunctionOn", map[string]interface{}{
					"objectId":            sess.objectID,
					"functionDeclaration": "function(){return this;}",
				}
			},
		},

		// DOM operations - Document structure and element queries
		{
			Name:        "DOM.getDocument",
			Description: "Retrieve the root DOM document structure",
			Category:    "DOM",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "DOM.getDocument", map[string]interface{}{}
			},
		},
		{
			Name:        "DOM.querySelector",
			Description: "Query DOM elements by CSS selector",
			Category:    "DOM",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "DOM.querySelector", map[string]interface{}{
					"nodeId":   sess.documentNode,
					"selector": "body",
				}
			},
		},
		{
			Name:        "DOM.getAttributes",
			Description: "Get attributes of a DOM node",
			Category:    "DOM",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "DOM.getAttributes", map[string]interface{}{"nodeId": sess.documentNode}
			},
		},
		{
			Name:        "DOM.getOuterHTML",
			Description: "Get outer HTML of a node",
			Category:    "DOM",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "DOM.getOuterHTML", map[string]interface{}{"nodeId": sess.documentNode}
			},
		},

		// Page operations - Navigation, resources, and page state
		{
			Name:        "Page.getNavigationHistory",
			Description: "Retrieve page navigation history",
			Category:    "Page",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Page.getNavigationHistory", map[string]interface{}{}
			},
		},
		{
			Name:        "Page.getResourceTree",
			Description: "Get page resource tree structure",
			Category:    "Page",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Page.getResourceTree", map[string]interface{}{}
			},
		},
		{
			Name:        "Page.getFrameTree",
			Description: "Get frame tree structure",
			Category:    "Page",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Page.getFrameTree", map[string]interface{}{}
			},
		},
		{
			Name:        "Page.captureScreenshot",
			Description: "Capture page screenshot via CDP",
			Category:    "Page",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Page.captureScreenshot", map[string]interface{}{"format": "png"}
			},
		},

		// Network operations - Request/response inspection
		{
			Name:        "Network.getCookies",
			Description: "Retrieve browser cookies",
			Category:    "Network",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Network.getCookies", map[string]interface{}{}
			},
		},
		{
			Name:        "Network.getAllCookies",
			Description: "Get all cookies from all contexts",
			Category:    "Network",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Network.getAllCookies", map[string]interface{}{}
			},
		},

		// Performance operations - Metrics and profiling
		{
			Name:        "Performance.getMetrics",
			Description: "Get runtime performance metrics",
			Category:    "Performance",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Performance.getMetrics", map[string]interface{}{}
			},
		},

		// Target operations - Tab and context management
		{
			Name:        "Target.getTargets",
			Description: "List all available targets",
			Category:    "Target",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Target.getTargets", map[string]interface{}{}
			},
		},
		{
			Name:        "Target.getTargetInfo",
			Description: "Get information about specific target",
			Category:    "Target",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				params := map[string]interface{}{}
				if sess.targetID != "" {
					params["targetId"] = sess.targetID
				}
				return "Target.getTargetInfo", params
			},
		},

		// Browser operations - Browser-level information
		{
			Name:        "Browser.getVersion",
			Description: "Get browser version information",
			Category:    "Browser",
			GenFunc: func(sess *cdpSessionContext) (string, map[string]interface{}) {
				return "Browser.getVersion", map[string]interface{}{}
			},
		},
	}

	// Initialize scenario tracking
	scenarioStatsMap := make([]*scenarioStats, len(scenarioGens))
	for i, scenarioGen := range scenarioGens {
		scenarioStatsMap[i] = &scenarioStats{
			Name:        scenarioGen.Name,
			Description: scenarioGen.Description,
			Category:    scenarioGen.Category,
			Latencies:   make([]float64, 0, 1000),
		}
	}

	var (
		totalOps     atomic.Int64
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

			// Initialize CDP session with proper domains and context
			sessionCtx, err := initializeCDPSession(benchCtx, conn, b.logger)
			if err != nil {
				b.logger.Error("failed to initialize CDP session", "worker", workerID, "err", err)
				return
			}

			msgIdx := 0
			for {
				select {
				case <-benchCtx.Done():
					return
				default:
				}

				scenarioIdx := msgIdx % len(scenarioGens)
				scenarioGen := scenarioGens[scenarioIdx]
				stats := scenarioStatsMap[scenarioIdx]
				msgIdx++

				// Generate method and params using session context
				method, params := scenarioGen.GenFunc(sessionCtx)

				// Use unique message ID based on worker and message index
				msgID := workerID*1000000 + msgIdx

				start := time.Now()
				response, err := sendCDPCommand(benchCtx, conn, msgID, method, params)
				latency := time.Since(start)
				latencyMs := float64(latency.Microseconds()) / 1000.0

				if err != nil {
					// Check if it's a CDP error (response received but with error) vs connection error
					if response != nil && response.Error != nil {
						// CDP returned an error response - this is a failure
						stats.Failures.Add(1)
						if !strings.Contains(err.Error(), "Cannot find context") {
							b.logger.Debug("CDP error", "worker", workerID, "scenario", stats.Name, "err", err)
						}
					} else {
						// Connection error - bail out
						if benchCtx.Err() != nil {
							return
						}
						b.logger.Error("connection error", "worker", workerID, "scenario", stats.Name, "err", err)
						return
					}
				}

				// Track overall stats
				totalOps.Add(1)
				latenciesMu.Lock()
				latencies = append(latencies, latencyMs)
				latenciesMu.Unlock()

				// Track scenario-specific stats
				stats.Operations.Add(1)
				stats.LatenciesMu.Lock()
				stats.Latencies = append(stats.Latencies, latencyMs)
				stats.LatenciesMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	ops := totalOps.Load()

	// Calculate overall throughput
	throughput := float64(ops) / elapsed.Seconds()

	// Calculate overall latency percentiles
	latencyMetrics := calculatePercentiles(latencies)

	// Calculate per-scenario results
	scenarioResults := make([]CDPScenarioResult, len(scenarioStatsMap))
	for i, stats := range scenarioStatsMap {
		operations := stats.Operations.Load()
		failures := stats.Failures.Load()
		successRate := 100.0
		if operations > 0 {
			successRate = (float64(operations-failures) / float64(operations)) * 100.0
		}

		scenarioResults[i] = CDPScenarioResult{
			Name:                stats.Name,
			Description:         stats.Description,
			Category:            stats.Category,
			OperationCount:      operations,
			ThroughputOpsPerSec: float64(operations) / elapsed.Seconds(),
			LatencyMS:           calculatePercentiles(stats.Latencies),
			SuccessRate:         successRate,
		}
	}

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
		Scenarios:            scenarioResults,
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

// cdpSessionContext holds the initialized CDP session state
type cdpSessionContext struct {
	sessionID    string
	targetID     string
	documentNode int
	objectID     string // A valid runtime object ID
}

// sendCDPCommand sends a CDP command and waits for the response
func sendCDPCommand(ctx context.Context, conn *websocket.Conn, id int, method string, params map[string]interface{}) (*CDPMessage, error) {
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

// initializeCDPSession sets up a proper CDP session with enabled domains and context
func initializeCDPSession(ctx context.Context, conn *websocket.Conn, logger *slog.Logger) (*cdpSessionContext, error) {
	msgID := 1000000 // Use high IDs to avoid collision with benchmark messages

	// Enable Runtime domain
	if _, err := sendCDPCommand(ctx, conn, msgID, "Runtime.enable", nil); err != nil {
		logger.Warn("Runtime.enable failed", "err", err)
	}
	msgID++

	// Enable DOM domain
	if _, err := sendCDPCommand(ctx, conn, msgID, "DOM.enable", nil); err != nil {
		logger.Warn("DOM.enable failed", "err", err)
	}
	msgID++

	// Enable Page domain
	if _, err := sendCDPCommand(ctx, conn, msgID, "Page.enable", nil); err != nil {
		logger.Warn("Page.enable failed", "err", err)
	}
	msgID++

	// Enable Network domain
	if _, err := sendCDPCommand(ctx, conn, msgID, "Network.enable", nil); err != nil {
		logger.Warn("Network.enable failed", "err", err)
	}
	msgID++

	// Get the current target ID
	targetResp, err := sendCDPCommand(ctx, conn, msgID, "Target.getTargets", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get targets: %w", err)
	}
	msgID++

	// Extract first page target
	sessionCtx := &cdpSessionContext{}
	if targetInfos, ok := targetResp.Result["targetInfos"].([]interface{}); ok && len(targetInfos) > 0 {
		if targetInfo, ok := targetInfos[0].(map[string]interface{}); ok {
			if targetID, ok := targetInfo["targetId"].(string); ok {
				sessionCtx.targetID = targetID
			}
		}
	}

	// Get document node
	docResp, err := sendCDPCommand(ctx, conn, msgID, "DOM.getDocument", nil)
	if err != nil {
		logger.Warn("DOM.getDocument failed during init", "err", err)
	} else {
		if root, ok := docResp.Result["root"].(map[string]interface{}); ok {
			if nodeID, ok := root["nodeId"].(float64); ok {
				sessionCtx.documentNode = int(nodeID)
			}
		}
	}
	msgID++

	// Get a valid object ID by evaluating a simple expression
	objResp, err := sendCDPCommand(ctx, conn, msgID, "Runtime.evaluate", map[string]interface{}{
		"expression": "({test: 123})",
	})
	if err != nil {
		logger.Warn("Runtime.evaluate failed during init", "err", err)
	} else {
		if result, ok := objResp.Result["result"].(map[string]interface{}); ok {
			if objectID, ok := result["objectId"].(string); ok {
				sessionCtx.objectID = objectID
			}
		}
	}

	return sessionCtx, nil
}
