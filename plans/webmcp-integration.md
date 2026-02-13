# WebMCP integration

## context

WebMCP :
- early-preview browser api (`navigator.modelContext` / `navigator.modelContextTesting`)
- available in Chrome 146+ via `--enable-features=WebMCPTesting` flag
- exposes MCP tool registration and execution directly from web pages
- enables apps to expose structured tools AI agents can discover & use via chrome directly

### current state

```
Client ──API──> kernel-images API ──CDP──> Chromium (stable)
                                            │
                                            └── No WebMCP support
                                            └── No tool discovery
                                            └── No tool execution via browser
```

### intent

- run chrome canary with webmcp early preview (in headful - headless currently stated as non-goal by the chromium team)
- exposes REST endpoint to query WebMCP tool availability on active tab
- provide websocket endpoint for bidirectional realtime webmcp communication:
  - subscribe/unsubscribe to webmcp tool registering/unregistering and webmcp events monitoring
  - list registered webmcp tools on active page
  - call webmcp tools and receive responses
  - track tab changes, recheck webmcp tools availability

## proposed design

```
External Client ──WebSocket──> /webmcp ──> WebMCP Handler
                                              │
                                              ▼
                                         Bridge (per session)
                                              │
                                        ┌─────┘
                                        │
                                        ▼
                                  Chrome CDP (browser-level WS)
                                        │
                                        ├── Target.setDiscoverTargets
                                        ├── Target.attachToTarget (page)
                                        ├── Runtime.evaluate
                                        │     └── navigator.modelContextTesting.listTools()
                                        │     └── navigator.modelContextTesting.executeTool()
                                        └── Target.targetInfoChanged (page navigation events)
```

### server/ api endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/webmcp/status` | REST: query WebMCP availability and tools on active tab |
| `GET` | `/webmcp` | WebSocket: bidirectional real-time WebMCP session |

### websockets procotol

#### client → server messages

```json
// subscribe to real-time tool monitoring
{"type": "subscribe"}

// unsubscribe from monitoring
{"type": "unsubscribe"}

// list tools on active page
{"type": "list_tools"}

// call webmcp registered tool
{"type": "call_tool", "id": "req-1", "tool_name": "getWeather", "arguments": {"city": "SF"}}
```

#### server → client messages

```json
// webmcp api availability changed
{"type": "webmcp_available", "available": true}

// tool list changed (emitted on page change or tool registration)
{"type": "tools_changed", "tools": [{"name": "getWeather", "description": "...", "inputSchema": {...}}]}

// active tab changed
{"type": "tab_changed", "url": "https://example.com", "title": "Example"}

// tool execution response
{"type": "tool_result", "id": "req-1", "result": {...}}

// tool execution error
{"type": "tool_error", "id": "req-1", "error": "tool not found"}

// * error
{"type": "error", "error": "unknown message type"}
```

### API responses

```json
{
  "available": true,
  "tools": [
    {
      "name": "getWeather",
      "description": "Get weather for a city",
      "inputSchema": {"type": "object", "properties": {"city": {"type": "string"}}},
      "annotations": {"readOnlyHint": true}
    }
  ],
  "active_tab": {
    "url": "https://example.com",
    "title": "Example"
  }
}
```

## implementation

current tested working implementation (dev)

### 1. chrome canary , webmcp chromium flag

- Replace Chromium stable with Google Chrome Canary (147+) in Dockerfile
- Add Google Chrome APT repository and signing key
- Create symlink `chromium` → `google-chrome-canary` for backward compatibility
- Set up policy directories for both `/etc/chromium/policies/` and `/etc/opt/chrome/policies/`

- Add `--enable-features=WebMCPTesting` to default Chromium flags in `run-docker.sh`
- Update `chromium-launcher` default binary path to `google-chrome-canary`
- Update `wrapper.sh` for Chrome Canary config directories and window title

### 2. webmcp bridge <> cdp

- `server/lib/webmcp/bridge.go`: `Bridge` struct
  - Connects to Chrome's browser-level CDP WebSocket
  - Enables target discovery (`Target.setDiscoverTargets`)
  - Attaches to the active page target (`Target.attachToTarget` with `flatten: true`)
  - Enables `Runtime` domain for JavaScript evaluation
  - Evaluates `navigator.modelContextTesting.listTools()` to discover tools
  - Evaluates `navigator.modelContextTesting.executeTool()` to invoke tools
  - Monitors `Target.targetInfoChanged` for page navigations
  - Polls for tool changes every 3 seconds when subscribed
  - Subscribe/Unsubscribe model for event emission

### 3. websockets handler

- `server/lib/webmcp/handler.go`: `Handler` struct
  - Accepts WebSocket connections at `/webmcp`
  - Creates per-session `Bridge` instance
  - Routes client messages: `subscribe`, `unsubscribe`, `list_tools`, `call_tool`
  - Forwards bridge events to client WebSocket
  - Async tool execution (non-blocking)

### 4. server/ api endpoints & types

- `server/cmd/api/api/webmcp.go`: `GetWebMCPStatus` handler
  - Creates ephemeral `Bridge` to query current tool state
  - Returns `WebMCPStatus` with availability, tools, and active tab info
  - Registered in OpenAPI spec and generated client code
- `server/lib/webmcp/types.go`: Protocol types
  - `ClientMessage`: subscribe, unsubscribe, list_tools, call_tool
  - `ServerMessage`: webmcp_available, tools_changed, tab_changed, tool_result, tool_error
  - `Tool`: name, description, inputSchema, annotations
  - Helper constructors for message creation
- `server/openapi.yaml`: `WebMCPStatus` and `WebMCPTool` schemas, `/webmcp/status` endpoint
- `server/lib/oapi/oapi.go`: Generated client/server code

## design decisions

### per-sesion Bridge

- each websockets client gets its own `Bridge` instance with a dedicated cdp connection
- avoids shared state between clients, simplifies lifecycle management
- client disconnects -> bridge torn down

### subscribe/unsubscribe model

- tool monitoring (polling + event emission) only runs when a client has explicitly subscribed
- api rest endpoint uses ephemeral bridge that doesn't subscribe, queries once and disconnects

### navigator.modelContextTesting

- implementation uses `navigator.modelContextTesting` (the testing/preview API) rather than `navigator.modelContext` (the production API), since webmcp is still in early preview
- to be updated on chromium (or custom chromium build) update

## risk assessment

- `WebMCPTesting` feature flag subject to change, but api surface likely to stay consistent with MCP protocol standards in the future
- 30-second timeout on cdp calls to be assessed in deployment if not enough

## open questions

1. tool execution configurable timeout

2. experimental feature to enable webmcp when not supported by website, via `<form>` DOM injection, in format described by webmcp

```html
<!-- webmcp accepts toolname|tooldescription in form fields as means to register tools -->
<!-- example here : https://github.com/GoogleChromeLabs/webmcp-tools/blob/main/demos/french-bistro/index.html -->
<form id="reservationForm" toolname="book_table" tooldescription="Creates a confirmed dining reservation... Accepts customer details, timing, and seating preferences." novalidate >
	<div class="form-group">
		<label for="name">Full Name</label>
		<input type="text" id="name" name="name" placeholder="e.g. Alexander Hamilton" required minlength="2" toolparamdescription="Customer's full name (min 2 chars)" />
		<span class="error-msg">Please enter a valid name (at least 2 characters).</span>
	</div>
</form>
```

3. caching tool discovery results during session / across sessions
