package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"ugreen-nas-mcp/config"
)

type SSESession struct {
	ID        string
	MsgChan   chan string
	CreatedAt time.Time
}

type SSEServer struct {
	mu       sync.RWMutex
	sessions map[string]*SSESession
	registry *ToolRegistry
}

func NewSSEServer(registry *ToolRegistry) *SSEServer {
	return &SSEServer{
		sessions: make(map[string]*SSESession),
		registry: registry,
	}
}

// AuthMiddleware verifies the Bearer Token
func (s *SSEServer) AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := config.GetConfig()
		if cfg.AuthToken != "" {
			authHeader := r.Header.Get("Authorization")
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			if token != cfg.AuthToken {
				http.Error(w, "Unauthorized: Invalid or missing API Key", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// HandleSSE handles /sse endpoint for MCP stream
func (s *SSEServer) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sessionID := fmt.Sprintf("session_%d", time.Now().UnixNano())
	session := &SSESession{
		ID:        sessionID,
		MsgChan:   make(chan string, 64),
		CreatedAt: time.Now(),
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.sessions, sessionID)
		s.mu.Unlock()
	}()

	// Send endpoint event with session URI per MCP spec
	endpointURL := fmt.Sprintf("/message?sessionId=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-session.MsgChan:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-time.After(15 * time.Second):
			// Keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// HandleMessage receives JSON-RPC client messages via POST
func (s *SSEServer) HandleMessage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	s.mu.RLock()
	session, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := s.processRequest(r.Context(), &req)
	respBytes, _ := json.Marshal(resp)

	if exists && session != nil {
		// Send response via SSE stream
		select {
		case session.MsgChan <- string(respBytes):
		default:
		}
	}

	// Also reply to HTTP POST
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func (s *SSEServer) processRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": true,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    "ugreen-nas-mcp",
				"version": "1.0.0",
			},
		}

	case "tools/list":
		tools := s.registry.ListTools()
		resp.Result = map[string]interface{}{
			"tools": tools,
		}

	case "tools/call":
		var callParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			resp.Error = &JSONRPCError{Code: -32602, Message: "Invalid params: " + err.Error()}
			return resp
		}

		rawArgs, _ := json.Marshal(callParams.Arguments)
		toolRes, _ := s.registry.CallTool(ctx, callParams.Name, rawArgs)
		resp.Result = toolRes

	default:
		resp.Error = &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method '%s' not found", req.Method)}
	}

	return resp
}
