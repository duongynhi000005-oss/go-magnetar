package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/wmentor/go-magnetar/internal/config"
	"github.com/wmentor/go-magnetar/internal/printer"
)

const (
	defaultProtocolVersion = "2024-11-05"
	defaultTimeoutSeconds  = 60
	maxScannerBufferBytes  = 16 * 1024 * 1024
)

// ServerConfig describes a single MCP server configuration under mcp.servers.<name>.
type ServerConfig struct {
	Name      string
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
	Timeout   time.Duration
}

// ToolInfo represents a single tool advertised by an MCP server via tools/list.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// BoundTool binds a discovered MCP tool to its server client and OpenAI function name.
type BoundTool struct {
	FunctionName string
	ServerName   string
	Tool         ToolInfo
	Client       Client
}

// Definition returns the OpenAI tool schema for this MCP tool.
func (b *BoundTool) Definition() openai.Tool {
	params := b.Tool.InputSchema
	if len(params) == 0 {
		params = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	} else if _, hasType := params["type"]; !hasType {
		cloned := make(map[string]any, len(params)+1)
		for k, v := range params {
			cloned[k] = v
		}
		cloned["type"] = "object"
		params = cloned
	}

	desc := strings.TrimSpace(b.Tool.Description)
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %q from server %q", b.Tool.Name, b.ServerName)
	} else {
		desc = fmt.Sprintf("[MCP:%s] %s", b.ServerName, desc)
	}

	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        b.FunctionName,
			Description: desc,
			Parameters:  params,
		},
	}
}

// Execute invokes the tool on its MCP server and returns the formatted text result.
func (b *BoundTool) Execute(ctx context.Context, rawArgs string) (string, error) {
	printer.ToolCall(printer.IconModule, b.FunctionName, "server", b.ServerName, "tool", b.Tool.Name)

	var arguments map[string]any
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal([]byte(trimmed), &arguments); err != nil {
			return fmt.Sprintf("error: failed to parse tool arguments: %v", err), nil
		}
	}
	if arguments == nil {
		arguments = map[string]any{}
	}

	out, err := b.Client.CallTool(ctx, b.Tool.Name, arguments)
	if err != nil {
		return fmt.Sprintf("error: %v", err), nil
	}
	return out, nil
}

// Client is the interface implemented by stdio and HTTP MCP transports.
type Client interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (string, error)
	Close() error
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	Resource *struct {
		URI  string `json:"uri,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"resource,omitempty"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func formatCallToolResult(res callToolResult) (string, error) {
	var parts []string
	for _, item := range res.Content {
		switch item.Type {
		case "text", "":
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		case "resource":
			if item.Resource != nil && item.Resource.Text != "" {
				parts = append(parts, item.Resource.Text)
			} else if item.Resource != nil && item.Resource.URI != "" {
				parts = append(parts, fmt.Sprintf("[resource: %s]", item.Resource.URI))
			}
		default:
			if item.Text != "" {
				parts = append(parts, item.Text)
			} else if item.MimeType != "" {
				parts = append(parts, fmt.Sprintf("[%s content: %s]", item.Type, item.MimeType))
			}
		}
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		if text == "" {
			text = "MCP tool returned an unspecified error"
		}
		return "", errors.New(text)
	}
	return text, nil
}

// ParseServers extracts configured MCP servers from Config.
func ParseServers(cfg *config.Config) []ServerConfig {
	if cfg == nil || !cfg.Bool("mcp.enable") {
		return nil
	}

	names := cfg.MapKeys("mcp.servers")
	sort.Strings(names)

	var servers []ServerConfig
	for _, name := range names {
		prefix := "mcp.servers." + name
		if cfg.Exists(prefix+".enable") && !cfg.Bool(prefix+".enable") {
			continue
		}

		transport := strings.ToLower(strings.TrimSpace(cfg.String(prefix + ".transport")))
		command := strings.TrimSpace(cfg.String(prefix + ".command"))
		url := strings.TrimSpace(cfg.String(prefix + ".url"))

		if transport == "" {
			if url != "" {
				transport = "http"
			} else if command != "" {
				transport = "stdio"
			}
		}

		if transport != "stdio" && transport != "http" {
			continue
		}
		if transport == "stdio" && command == "" {
			continue
		}
		if transport == "http" && url == "" {
			continue
		}

		timeoutSec := cfg.Int(prefix + ".timeout")
		if timeoutSec <= 0 {
			timeoutSec = defaultTimeoutSeconds
		}

		args := cfg.Strings(prefix + ".args")
		resolvedArgs := make([]string, len(args))
		for i, a := range args {
			resolvedArgs[i] = config.ResolveEnvVars(a, "")
		}

		servers = append(servers, ServerConfig{
			Name:      name,
			Transport: transport,
			Command:   command,
			Args:      resolvedArgs,
			Env:       cfg.StringMap(prefix + ".env"),
			URL:       url,
			Headers:   cfg.StringMap(prefix + ".headers"),
			Timeout:   time.Duration(timeoutSec) * time.Second,
		})
	}

	return servers
}

// SanitizeName converts a server or tool identifier into an OpenAI-safe function name part.
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "tool"
	}
	return out
}

// NewClient constructs a transport client for the given ServerConfig.
func NewClient(sc ServerConfig) (Client, error) {
	switch sc.Transport {
	case "http":
		return NewHTTPClient(sc), nil
	case "stdio":
		return NewStdioClient(sc), nil
	default:
		return nil, fmt.Errorf("mcp: unsupported transport %q for server %q", sc.Transport, sc.Name)
	}
}

// Manager manages connections and discovered tools across configured MCP servers.
type Manager struct {
	mu      sync.Mutex
	clients []Client
	tools   []*BoundTool
}

// NewManager connects to all enabled MCP servers in cfg, performs the MCP initialization
// handshake, and discovers their tools.
func NewManager(ctx context.Context, cfg *config.Config) (*Manager, error) {
	servers := ParseServers(cfg)
	mgr := &Manager{}

	usedNames := make(map[string]int)
	for _, sc := range servers {
		client, err := NewClient(sc)
		if err != nil {
			_ = mgr.Close()
			return nil, err
		}

		initCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
		err = client.Initialize(initCtx)
		cancel()
		if err != nil {
			_ = client.Close()
			_ = mgr.Close()
			return nil, fmt.Errorf("mcp: failed to initialize server %q: %w", sc.Name, err)
		}

		listCtx, listCancel := context.WithTimeout(ctx, sc.Timeout)
		discovered, err := client.ListTools(listCtx)
		listCancel()
		if err != nil {
			_ = client.Close()
			_ = mgr.Close()
			return nil, fmt.Errorf("mcp: failed to list tools from server %q: %w", sc.Name, err)
		}

		mgr.clients = append(mgr.clients, client)
		serverPart := SanitizeName(sc.Name)
		for _, t := range discovered {
			if strings.TrimSpace(t.Name) == "" {
				continue
			}
			fnName := fmt.Sprintf("mcp_%s_%s", serverPart, SanitizeName(t.Name))
			if count := usedNames[fnName]; count > 0 {
				fnName = fmt.Sprintf("%s_%d", fnName, count+1)
			}
			usedNames[fnName]++

			mgr.tools = append(mgr.tools, &BoundTool{
				FunctionName: fnName,
				ServerName:   sc.Name,
				Tool:         t,
				Client:       client,
			})
		}
	}

	return mgr, nil
}

// Tools returns all discovered bound tools across active MCP servers.
func (m *Manager) Tools() []*BoundTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*BoundTool, len(m.tools))
	copy(out, m.tools)
	return out
}

// Close closes all active MCP server clients.
func (m *Manager) Close() error {
	m.mu.Lock()
	clients := m.clients
	m.clients = nil
	m.mu.Unlock()

	var firstErr error
	for _, c := range clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// HTTPClient implements the MCP Streamable HTTP / JSON-RPC over HTTP POST transport.
type HTTPClient struct {
	cfg        ServerConfig
	httpClient *http.Client
	nextID     atomic.Int64
	mu         sync.Mutex
	sessionID  string
}

// NewHTTPClient creates an HTTPClient for an HTTP MCP server.
func NewHTTPClient(cfg ServerConfig) *HTTPClient {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds * time.Second
	}
	return &HTTPClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *HTTPClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": defaultProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "go-magnetar",
			"version": "1.0.0",
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", nil)
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var all []ToolInfo
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page listToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: invalid tools/list response: %w", err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var res callToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("mcp: invalid tools/call response: %w", err)
	}
	return formatCallToolResult(res)
}

func (c *HTTPClient) Close() error {
	return nil
}

func (c *HTTPClient) notify(ctx context.Context, method string, params any) error {
	reqBody, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	c.captureSessionID(resp)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("mcp: notification %q returned HTTP %d", method, resp.StatusCode)
	}
	return nil
}

func (c *HTTPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	reqBody, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.captureSessionID(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp: %s returned HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	rpcResp, err := parseHTTPJSONRPCResponse(resp.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func (c *HTTPClient) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	sid := c.sessionID
	c.mu.Unlock()
	if sid != "" && req.Header.Get("Mcp-Session-Id") == "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
}

func (c *HTTPClient) captureSessionID(resp *http.Response) {
	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}
}

func parseHTTPJSONRPCResponse(contentType string, body []byte) (*jsonRPCResponse, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferBytes)
		var lastResp *jsonRPCResponse
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			var r jsonRPCResponse
			if err := json.Unmarshal([]byte(payload), &r); err == nil && (len(r.Result) > 0 || r.Error != nil) {
				lastResp = &r
			}
		}
		if lastResp != nil {
			return lastResp, nil
		}
		return nil, errors.New("mcp: no JSON-RPC response found in event stream")
	}

	var r jsonRPCResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("mcp: failed to decode JSON-RPC response: %w", err)
	}
	return &r, nil
}

// StdioClient implements the MCP stdio subprocess transport.
type StdioClient struct {
	cfg     ServerConfig
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Scanner
	nextID  atomic.Int64
	started bool
	closed  bool
}

// NewStdioClient creates a StdioClient for a command-based MCP server.
func NewStdioClient(cfg ServerConfig) *StdioClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeoutSeconds * time.Second
	}
	return &StdioClient{cfg: cfg}
}

func (c *StdioClient) ensureStartedLocked() error {
	if c.closed {
		return errors.New("mcp: stdio client is closed")
	}
	if c.started {
		return nil
	}

	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	if len(c.cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range c.cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferBytes)

	c.cmd = cmd
	c.stdin = stdin
	c.reader = scanner
	c.started = true
	return nil
}

func (c *StdioClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": defaultProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "go-magnetar",
			"version": "1.0.0",
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

func (c *StdioClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	var all []ToolInfo
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page listToolsResult
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("mcp: invalid tools/list response: %w", err)
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

func (c *StdioClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var res callToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("mcp: invalid tools/call response: %w", err)
	}
	return formatCallToolResult(res)
}

func (c *StdioClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureStartedLocked(); err != nil {
		return err
	}
	data, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *StdioClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureStartedLocked(); err != nil {
		return nil, err
	}

	id := c.nextID.Add(1)
	data, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return nil, err
	}

	type readResult struct {
		resp *jsonRPCResponse
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		for c.reader.Scan() {
			line := strings.TrimSpace(c.reader.Text())
			if line == "" {
				continue
			}
			var r jsonRPCResponse
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				continue
			}
			// Skip server-initiated notifications (no id).
			if len(r.ID) == 0 || string(r.ID) == "null" {
				continue
			}
			var respID int64
			if err := json.Unmarshal(r.ID, &respID); err == nil && respID == id {
				ch <- readResult{resp: &r}
				return
			}
		}
		if err := c.reader.Err(); err != nil {
			ch <- readResult{err: err}
			return
		}
		ch <- readResult{err: io.EOF}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("mcp: stdio read failed for %s: %w", method, res.err)
		}
		if res.resp.Error != nil {
			return nil, fmt.Errorf("mcp error (%d): %s", res.resp.Error.Code, res.resp.Error.Message)
		}
		return res.resp.Result, nil
	}
}

func (c *StdioClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}
