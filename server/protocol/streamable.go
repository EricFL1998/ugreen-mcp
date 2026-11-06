package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

var httpSessionCounter uint64

func newHTTPSessionID() string {
	return fmt.Sprintf("mcp_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&httpSessionCounter, 1))
}

// HandleStreamableHTTP implements the MCP Streamable HTTP transport (POST /mcp).
// Newer MCP clients (Claude Desktop, Hermes, Cursor, ...) use this transport
// instead of the legacy two-endpoint SSE transport.
func (s *SSEServer) HandleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, Accept, Last-Event-ID")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handleStreamablePost(w, r)
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (s *SSEServer) handleStreamablePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// JSON-RPC batch support
	if trimmed[0] == '[' {
		var reqs []JSONRPCRequest
		if err := json.Unmarshal(trimmed, &reqs); err != nil {
			writeRPCError(w, nil, -32700, "Parse error: "+err.Error())
			return
		}
		responses := make([]*JSONRPCResponse, 0, len(reqs))
		for i := range reqs {
			if reqs[i].Method == "initialize" {
				w.Header().Set("Mcp-Session-Id", newHTTPSessionID())
			}
			resp := s.processRequest(r.Context(), &reqs[i])
			if reqs[i].ID != nil && resp != nil {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(responses)
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		writeRPCError(w, nil, -32700, "Parse error: "+err.Error())
		return
	}

	if req.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", newHTTPSessionID())
	}

	resp := s.processRequest(r.Context(), &req)
	if req.ID == nil || resp == nil {
		// JSON-RPC notification: acknowledge without a body
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeRPCError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	})
}
