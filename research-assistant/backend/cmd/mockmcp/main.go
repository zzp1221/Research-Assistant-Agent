// Command mockmcp is a deterministic MCP server used by the container test
// suite. It implements the small subset of JSON-RPC that the client needs:
// initialize, notifications/initialized, tools/list and tools/call.
//
// Usage:
//
//	mockmcp -stdio
//	mockmcp -http :9100
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "echo",
			"description": "Return the message unchanged.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"message": map[string]any{"type": "string"}},
				"required":   []string{"message"},
			},
		},
		{
			"name":        "slow",
			"description": "Sleep for the requested milliseconds.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"ms": map[string]any{"type": "integer"}},
				"required":   []string{"ms"},
			},
		},
		{
			"name":        "big",
			"description": "Return N bytes of text.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"bytes": map[string]any{"type": "integer"}},
				"required":   []string{"bytes"},
			},
		},
		{
			"name":        "fail",
			"description": "Return an MCP tool error.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"message": map[string]any{"type": "string"}},
			},
		},
		{
			"name":        "crash",
			"description": "Exit the server without responding.",
			"inputSchema": map[string]any{"type": "object"},
		},
	}
}

func handle(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mockmcp", "version": "1.0.0"},
		}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": toolDefinitions()}, nil
	case "tools/call":
		var payload struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		switch payload.Name {
		case "echo":
			message, _ := payload.Arguments["message"].(string)
			return map[string]any{"content": []map[string]any{{"type": "text", "text": message}}, "isError": false}, nil
		case "slow":
			ms := int(number(payload.Arguments["ms"], 1000))
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("slept %dms", ms)}}, "isError": false}, nil
		case "big":
			n := int(number(payload.Arguments["bytes"], 1024))
			if n > 4<<20 {
				n = 4 << 20
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": strings.Repeat("y", n)}}, "isError": false}, nil
		case "fail":
			message, _ := payload.Arguments["message"].(string)
			if message == "" {
				message = "mock mcp failure"
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": message}}, "isError": true}, nil
		case "crash":
			os.Exit(1)
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool " + payload.Name}
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}

func number(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return fallback
	}
}

func serveStdio() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var request rpcRequest
		if err := json.Unmarshal(line, &request); err != nil {
			continue
		}
		result, rpcErr := handle(request.Method, request.Params)
		if len(request.ID) == 0 {
			continue
		}
		response := rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr}
		encoded, _ := json.Marshal(response)
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	}
}

func serveHTTP(address string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request rpcRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		result, rpcErr := handle(request.Method, request.Params)
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "mock-session")
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr})
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("mockmcp http listening on %s", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func main() {
	httpAddr := flag.String("http", "", "HTTP listen address (empty means stdio)")
	flag.Parse()
	if strings.TrimSpace(*httpAddr) != "" {
		serveHTTP(*httpAddr)
		return
	}
	serveStdio()
}
