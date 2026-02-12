package webmcp

import "encoding/json"

// --- Client -> Server messages ---

// ClientMessage is a message sent from the WebSocket client to the server.
type ClientMessage struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Client message types
const (
	MsgSubscribe   = "subscribe"
	MsgUnsubscribe = "unsubscribe"
	MsgListTools   = "list_tools"
	MsgCallTool    = "call_tool"
)

// --- Server -> Client messages ---

// ServerMessage is a message sent from the server to the WebSocket client.
type ServerMessage struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Available *bool           `json:"available,omitempty"`
	Tools     []Tool          `json:"tools,omitempty"`
	URL       string          `json:"url,omitempty"`
	Title     string          `json:"title,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Server message types
const (
	MsgWebMCPAvailable = "webmcp_available"
	MsgToolsChanged    = "tools_changed"
	MsgTabChanged      = "tab_changed"
	MsgToolResult      = "tool_result"
	MsgToolError       = "tool_error"
	MsgError           = "error"
)

// --- WebMCP domain types ---

// Tool represents a WebMCP tool registered on a page.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// ToolCallResult represents the result of calling a WebMCP tool.
type ToolCallResult struct {
	Content []ToolContent `json:"content,omitempty"`
}

// ToolContent represents a single content item in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TabInfo holds information about the active browser tab.
type TabInfo struct {
	TargetID string `json:"targetId"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

// Helper constructors for ServerMessage

func NewAvailableMsg(available bool) ServerMessage {
	return ServerMessage{Type: MsgWebMCPAvailable, Available: &available}
}

func NewToolsChangedMsg(tools []Tool) ServerMessage {
	return ServerMessage{Type: MsgToolsChanged, Tools: tools}
}

func NewTabChangedMsg(url, title string) ServerMessage {
	return ServerMessage{Type: MsgTabChanged, URL: url, Title: title}
}

func NewToolResultMsg(id string, result json.RawMessage) ServerMessage {
	return ServerMessage{Type: MsgToolResult, ID: id, Result: result}
}

func NewToolErrorMsg(id, errMsg string) ServerMessage {
	return ServerMessage{Type: MsgToolError, ID: id, Error: errMsg}
}

func NewErrorMsg(errMsg string) ServerMessage {
	return ServerMessage{Type: MsgError, Error: errMsg}
}
