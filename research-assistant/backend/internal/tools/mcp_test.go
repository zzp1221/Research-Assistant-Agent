package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

// TestMockMCPHelper is not a real test: when invoked as a subprocess it acts as
// a minimal MCP stdio server. This keeps the stdio client covered without an
// external binary in the unit-test container.
func TestMockMCPHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MOCKMCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "echo",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}, "required": []string{"message"}},
			}}}
		case "tools/call":
			var payload struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(request.Params, &payload)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprint(payload.Arguments["message"])}}}
		}
		if len(request.ID) == 0 {
			continue
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		_, _ = writer.Write(append(response, '\n'))
		_ = writer.Flush()
	}
	os.Exit(0)
}

func TestMCPStdioDiscoverAndInvoke(t *testing.T) {
	t.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", "unit-test-key")
	t.Setenv("MCP_STDIO_ALLOWED_COMMANDS", os.Args[0])
	sealed, err := sealSecret("1", "mcp:mcp:stdio", "")
	if err != nil {
		t.Fatal(err)
	}
	registry := newTestRegistry(t)
	server := models.MCPServer{
		ID: "mcp:stdio", Transport: models.ToolTransportMCPStdio,
		Command: os.Args[0], Args: []string{"-test.run=TestMockMCPHelper"},
		EnvSealed: map[string]string{"GO_WANT_MOCKMCP_HELPER": sealed},
		Status:    "ready", TimeoutSec: 10,
	}
	if err := registry.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	client := &MCPClient{Registry: registry}
	specs, fingerprint, err := client.Discover(context.Background(), server)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(specs) != 1 || specs[0].Name != "echo" || fingerprint == "" {
		t.Fatalf("unexpected specs: %+v fingerprint=%q", specs, fingerprint)
	}
	tool := models.ToolDefinition{ID: "mcp:stdio/echo", ServerID: "mcp:stdio", RemoteName: "echo", Transport: models.ToolTransportMCPStdio, TimeoutSec: 10}
	result, err := client.Invoke(context.Background(), tool, map[string]any{"message": "hello-mcp"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if result.Text != "hello-mcp" {
		t.Fatalf("unexpected text %q", result.Text)
	}
}

func TestMCPStdioCommandAllowlist(t *testing.T) {
	t.Setenv("MCP_STDIO_ALLOWED_COMMANDS", "/app/mockmcp")
	server := models.MCPServer{ID: "mcp:x", Transport: models.ToolTransportMCPStdio, Command: "/bin/sh", Status: "ready"}
	client := &MCPClient{Registry: newTestRegistry(t)}
	if _, _, err := client.Discover(context.Background(), server); err == nil {
		t.Fatal("expected command allowlist rejection")
	}
}

func TestMCPHTTPDiscoverInvokeAndSSRF(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			var payload struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(request.Params, &payload)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprint(payload.Arguments["message"])}}}
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registry := newTestRegistry(t)
	mcpServer := models.MCPServer{ID: "mcp:http", Transport: models.ToolTransportMCPHTTP, URL: server.URL + "/mcp", AllowPrivateHosts: true, Status: "ready", TimeoutSec: 10}
	if err := registry.SaveServer(mcpServer); err != nil {
		t.Fatal(err)
	}
	client := &MCPClient{Registry: registry}
	specs, _, err := client.Discover(context.Background(), mcpServer)
	if err != nil || len(specs) != 1 {
		t.Fatalf("discover: %v specs=%+v", err, specs)
	}
	tool := models.ToolDefinition{ID: "mcp:http/echo", ServerID: "mcp:http", RemoteName: "echo", Transport: models.ToolTransportMCPHTTP}
	result, err := client.Invoke(context.Background(), tool, map[string]any{"message": "http-mcp"})
	if err != nil || result.Text != "http-mcp" {
		t.Fatalf("invoke: %v text=%q", err, result.Text)
	}

	// The same private address must be rejected when not explicitly allowed.
	blocked := models.MCPServer{ID: "mcp:blocked", Transport: models.ToolTransportMCPHTTP, URL: server.URL + "/mcp", Status: "ready"}
	if _, _, err := client.Discover(context.Background(), blocked); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected SSRF rejection, got %v", err)
	}
}

func TestMCPFingerprintStable(t *testing.T) {
	a := []MCPToolSpec{{Name: "b", InputSchema: `{"type":"object"}`}, {Name: "a", InputSchema: `{"type":"object"}`}}
	b := []MCPToolSpec{{Name: "a", InputSchema: `{"type":"object"}`}, {Name: "b", InputSchema: `{"type":"object"}`}}
	if MCPFingerprint(a) != MCPFingerprint(b) {
		t.Fatal("fingerprint must be order independent")
	}
	c := []MCPToolSpec{{Name: "a", InputSchema: `{"type":"object","required":["x"]}`}, {Name: "b", InputSchema: `{"type":"object"}`}}
	if MCPFingerprint(a) == MCPFingerprint(c) {
		t.Fatal("schema change must change fingerprint")
	}
}
