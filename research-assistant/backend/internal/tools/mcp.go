package tools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"research-assistant-backend/internal/models"
)

// MCPToolSpec is one tool returned by an MCP server's tools/list.
type MCPToolSpec struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	InputSchema  string `json:"input_schema,omitempty"`
	OutputSchema string `json:"output_schema,omitempty"`
}

// MCPClient speaks the Model Context Protocol over stdio and Streamable HTTP.
type MCPClient struct {
	Registry   *Registry
	HTTPClient *http.Client
}

// Discover connects to a server, performs initialize and tools/list, and
// returns the discovered tools plus a schema fingerprint.
func (c *MCPClient) Discover(ctx context.Context, server models.MCPServer) ([]MCPToolSpec, string, error) {
	var specs []MCPToolSpec
	var err error
	switch server.Transport {
	case models.ToolTransportMCPStdio:
		specs, err = c.discoverStdio(ctx, server)
	case models.ToolTransportMCPHTTP:
		specs, err = c.discoverHTTP(ctx, server)
	default:
		return nil, "", fmt.Errorf("unsupported mcp transport %q", server.Transport)
	}
	if err != nil {
		return nil, "", err
	}
	return specs, MCPFingerprint(specs), nil
}

// MCPFingerprint is a stable digest of the discovered tool schemas.
func MCPFingerprint(specs []MCPToolSpec) string {
	sorted := append([]MCPToolSpec(nil), specs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	encoded, _ := json.Marshal(sorted)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Invoke calls tools/call on the tool's server.
func (c *MCPClient) Invoke(ctx context.Context, tool models.ToolDefinition, arguments map[string]any) (*NativeResult, error) {
	server, err := c.Registry.GetServer(tool.ServerID)
	if err != nil {
		return nil, &NativeError{Code: "tool_unavailable", Message: "mcp server not found: " + tool.ServerID}
	}
	if server.Status == "stale" {
		return nil, &NativeError{Code: "tool_schema_stale", Message: "mcp server schema changed; re-register and review it"}
	}
	params := map[string]any{"name": tool.RemoteName, "arguments": arguments}
	var raw json.RawMessage
	switch server.Transport {
	case models.ToolTransportMCPStdio:
		raw, err = c.callStdio(ctx, server, "tools/call", params)
	case models.ToolTransportMCPHTTP:
		raw, err = c.callHTTP(ctx, server, "tools/call", params)
	default:
		return nil, &NativeError{Code: "tool_unavailable", Message: "unsupported mcp transport"}
	}
	if err != nil {
		return nil, err
	}
	return parseMCPCallResult(raw)
}

type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpCallContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type mcpCallResult struct {
	Content           []mcpCallContent `json:"content"`
	StructuredContent map[string]any   `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
}

func parseMCPCallResult(raw json.RawMessage) (*NativeResult, error) {
	var result mcpCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &NativeError{Code: "tool_execution_failed", Message: "decode mcp result: " + err.Error()}
	}
	texts := make([]string, 0, len(result.Content))
	jsonValue := map[string]any{}
	if result.StructuredContent != nil {
		for key, value := range result.StructuredContent {
			jsonValue[key] = value
		}
	}
	for _, content := range result.Content {
		switch strings.ToLower(content.Type) {
		case "text":
			texts = append(texts, content.Text)
		case "image":
			if content.Data != "" {
				jsonValue["image_base64"] = content.Data
				jsonValue["image_mime_type"] = content.MimeType
			}
		case "resource":
			if content.Text != "" {
				texts = append(texts, content.Text)
			}
		}
	}
	text := strings.Join(texts, "\n")
	if result.IsError {
		if text == "" {
			text = "mcp tool returned isError=true"
		}
		return nil, &NativeError{Code: "tool_execution_failed", Message: text}
	}
	if len(jsonValue) == 0 {
		jsonValue = nil
	}
	return &NativeResult{Text: text, JSON: jsonValue, Bytes: int64(len(text))}, nil
}

// ---------- stdio transport ----------

type stdioSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	errCh  chan error
	stderr *bytes.Buffer
	nextID int64
	mu     sync.Mutex
	closed atomic.Bool
}

func startStdioSession(ctx context.Context, server models.MCPServer) (*stdioSession, error) {
	if err := validateMCPCommand(server.Command); err != nil {
		return nil, err
	}
	cmd := exec.Command(server.Command, server.Args...)
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "LANG=C.UTF-8"}
	for key, sealed := range server.EnvSealed {
		value, err := openSecretValue(sealed, "mcp:"+server.ID, server.OwnerID)
		if err != nil {
			return nil, fmt.Errorf("open env %s: %w", key, err)
		}
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	if strings.TrimSpace(server.WorkingDir) != "" {
		cmd.Dir = server.WorkingDir
	} else {
		cmd.Dir = os.TempDir()
	}
	configureProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp server: %w", err)
	}
	session := &stdioSession{
		cmd:    cmd,
		stdin:  stdin,
		lines:  make(chan []byte, 16),
		errCh:  make(chan error, 1),
		stderr: stderr,
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			session.lines <- line
		}
		close(session.lines)
		session.errCh <- scanner.Err()
	}()
	go func() {
		// Reap the process so a crashed server does not become a zombie.
		_ = cmd.Wait()
	}()
	go func() {
		<-ctx.Done()
		session.Close()
	}()
	return session, nil
}

func validateMCPCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("mcp stdio command is required")
	}
	if !filepath.IsAbs(command) {
		return fmt.Errorf("mcp stdio command must be an absolute path")
	}
	allowed := strings.TrimSpace(os.Getenv("MCP_STDIO_ALLOWED_COMMANDS"))
	if allowed == "" {
		allowed = "/app/mockmcp"
	}
	for _, candidate := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), command) {
			return nil
		}
	}
	return fmt.Errorf("mcp stdio command %s is not in MCP_STDIO_ALLOWED_COMMANDS", command)
}

func (s *stdioSession) Close() {
	if s == nil || s.closed.Swap(true) {
		return
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	killProcessGroup(s.cmd)
}

func (s *stdioSession) Notify(method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.stdin.Write(append(encoded, '\n'))
	return err
}

func (s *stdioSession) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&s.nextID, 1)
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	_, err = s.stdin.Write(append(encoded, '\n'))
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write mcp request: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case line, ok := <-s.lines:
			if !ok {
				return nil, fmt.Errorf("mcp server exited: %s", preview(s.stderr.String(), 300))
			}
			var envelope mcpEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				continue
			}
			if len(envelope.ID) == 0 {
				continue // notification
			}
			var responseID int64
			if err := json.Unmarshal(envelope.ID, &responseID); err != nil || responseID != id {
				continue
			}
			if envelope.Error != nil {
				return nil, fmt.Errorf("mcp error %d: %s", envelope.Error.Code, envelope.Error.Message)
			}
			return envelope.Result, nil
		case <-time.After(100 * time.Millisecond):
			// Fall through to re-check ctx; keeps the loop responsive when a
			// server writes nothing and the deadline expires.
			continue
		}
	}
}

func (c *MCPClient) discoverStdio(ctx context.Context, server models.MCPServer) ([]MCPToolSpec, error) {
	session, err := startStdioSession(ctx, server)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if _, err := session.Request(ctx, "initialize", mcpInitializeParams()); err != nil {
		return nil, err
	}
	_ = session.Notify("notifications/initialized", map[string]any{})
	raw, err := session.Request(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseMCPToolList(raw)
}

func (c *MCPClient) callStdio(ctx context.Context, server models.MCPServer, method string, params any) (json.RawMessage, error) {
	session, err := startStdioSession(ctx, server)
	if err != nil {
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	defer session.Close()
	if _, err := session.Request(ctx, "initialize", mcpInitializeParams()); err != nil {
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	_ = session.Notify("notifications/initialized", map[string]any{})
	raw, err := session.Request(ctx, method, params)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &NativeError{Code: "tool_timeout", Message: "mcp stdio call timed out"}
		}
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	return raw, nil
}

func mcpInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "research-assistant", "version": "1.0.0"},
	}
}

func parseMCPToolList(raw json.RawMessage) ([]MCPToolSpec, error) {
	var payload struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema any    `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	specs := make([]MCPToolSpec, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		schema := ""
		if tool.InputSchema != nil {
			encoded, err := json.Marshal(tool.InputSchema)
			if err == nil {
				schema = string(encoded)
			}
		}
		specs = append(specs, MCPToolSpec{Name: tool.Name, Description: tool.Description, InputSchema: schema})
	}
	return specs, nil
}

// ---------- HTTP transport ----------

func (c *MCPClient) discoverHTTP(ctx context.Context, server models.MCPServer) ([]MCPToolSpec, error) {
	sessionID := ""
	raw, newSession, err := c.httpRequest(ctx, server, 1, "initialize", mcpInitializeParams(), sessionID)
	if err != nil {
		return nil, err
	}
	_ = raw
	if newSession != "" {
		sessionID = newSession
	}
	_, _, _ = c.httpRequest(ctx, server, 2, "notifications/initialized", map[string]any{}, sessionID)
	listRaw, _, err := c.httpRequest(ctx, server, 3, "tools/list", map[string]any{}, sessionID)
	if err != nil {
		return nil, err
	}
	return parseMCPToolList(listRaw)
}

func (c *MCPClient) callHTTP(ctx context.Context, server models.MCPServer, method string, params any) (json.RawMessage, error) {
	sessionID := ""
	_, newSession, err := c.httpRequest(ctx, server, 1, "initialize", mcpInitializeParams(), sessionID)
	if err != nil {
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	if newSession != "" {
		sessionID = newSession
	}
	raw, _, err := c.httpRequest(ctx, server, 2, method, params, sessionID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &NativeError{Code: "tool_timeout", Message: "mcp http call timed out"}
		}
		return nil, &NativeError{Code: "tool_unavailable", Message: err.Error()}
	}
	return raw, nil
}

func (c *MCPClient) httpRequest(ctx context.Context, server models.MCPServer, id int64, method string, params any, sessionID string) (json.RawMessage, string, error) {
	if err := ValidateOutboundURL(server.URL, server.AllowPrivateHosts); err != nil {
		return nil, "", &NativeError{Code: "tool_ssrf_blocked", Message: err.Error()}
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewReader(encoded))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	for key, sealed := range server.HeaderSealed {
		value, err := openSecretValue(sealed, "mcp:"+server.ID, server.OwnerID)
		if err != nil {
			return nil, "", err
		}
		request.Header.Set(key, value)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	// The JSON-RPC envelope must stay parseable; MaxOutputBytes limits the
	// tool's content, which the invoker truncates after parsing. Give the
	// envelope at least 1 MiB of headroom and cap it at 32 MiB.
	maxBytes := server.MaxOutputBytes
	if maxBytes < 1<<20 {
		maxBytes = 1 << 20
	}
	if maxBytes > 32<<20 {
		maxBytes = 32 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("mcp http returned %d: %s", response.StatusCode, preview(string(body), 200))
	}
	newSession := response.Header.Get("Mcp-Session-Id")
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		raw, err := parseSSEForID(body, id)
		return raw, newSession, err
	}
	var envelope mcpEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, newSession, fmt.Errorf("decode mcp http response: %w", err)
	}
	if envelope.Error != nil {
		return nil, newSession, fmt.Errorf("mcp error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope.Result, newSession, nil
}

func parseSSEForID(body []byte, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	var data strings.Builder
	flush := func() (json.RawMessage, bool) {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" {
			return nil, false
		}
		var envelope mcpEnvelope
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return nil, false
		}
		if len(envelope.ID) == 0 {
			return nil, false
		}
		var responseID int64
		if err := json.Unmarshal(envelope.ID, &responseID); err != nil || responseID != id {
			return nil, false
		}
		if envelope.Error != nil {
			return nil, false
		}
		return envelope.Result, true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if strings.TrimSpace(line) == "" {
			if raw, ok := flush(); ok {
				return raw, nil
			}
		}
	}
	if raw, ok := flush(); ok {
		return raw, nil
	}
	return nil, fmt.Errorf("mcp sse response did not contain id %d", id)
}
