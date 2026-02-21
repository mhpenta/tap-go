package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mhpenta/tap-go"
)

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ToolDoc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolClient interface {
	List(ctx context.Context) (*ListResult, error)
	Get(ctx context.Context, name string) (*ToolDoc, error)
	Run(ctx context.Context, name string, args any) (json.RawMessage, error)
	RunStream(ctx context.Context, name string, args any) (<-chan tap.Event, error)
}

type Client struct {
	base   string
	apiKey string
	http   *http.Client
	logger *slog.Logger
}

type Option func(*Client)

func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger == nil {
			return
		}
		c.logger = logger
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient == nil {
			return
		}
		clone := *httpClient
		c.http = &clone
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout <= 0 {
			return
		}
		if c.http == nil {
			c.http = &http.Client{}
		}
		c.http.Timeout = timeout
	}
}

func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		base:   strings.TrimRight(baseURL, "/"),
		http:   &http.Client{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	c.logger.Debug("client request", "method", req.Method, "url", req.URL.String())
	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Warn("client request failed", "method", req.Method, "url", req.URL.String(), "error", err.Error())
		return nil, err
	}
	c.logger.Debug("client response", "method", req.Method, "url", req.URL.String(), "status", resp.StatusCode)
	return resp, nil
}

type ListResult struct {
	Description string
	Tools       []Tool
}

const maxErrorMessageLen = 512

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (c *Client) List(ctx context.Context) (*ListResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/tools", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	result := &ListResult{}
	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	separator := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			separator = i
			break
		}
	}

	parseTools := func(lines []string) []Tool {
		tools := make([]Tool, 0, len(lines))
		for _, line := range lines {
			tool, ok := parseToolLine(line)
			if ok {
				tools = append(tools, tool)
			}
		}
		return tools
	}

	if separator >= 0 {
		result.Description = strings.Join(lines[:separator], "\n")
		result.Tools = parseTools(lines[separator+1:])
		return result, nil
	}

	i := 0
	var desc []string
	for ; i < len(lines); i++ {
		if _, ok := parseToolLine(lines[i]); ok {
			break
		}
		if strings.TrimSpace(lines[i]) != "" {
			desc = append(desc, lines[i])
		}
	}
	result.Description = strings.Join(desc, "\n")
	result.Tools = parseTools(lines[i:])

	return result, nil
}

func parseToolLine(line string) (Tool, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Tool{}, false
	}
	name, desc, ok := strings.Cut(line, ": ")
	if !ok || !toolNamePattern.MatchString(name) {
		return Tool{}, false
	}
	return Tool{Name: name, Description: desc}, true
}

func (c *Client) Get(ctx context.Context, name string) (*ToolDoc, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/tools/"+name, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, fmt.Errorf("get tool %s: %w", name, err)
	}

	var doc ToolDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

type runDirectives struct {
	JQ string
}

func splitRunArgs(args any) (any, runDirectives, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, runDirectives{}, err
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return args, runDirectives{}, nil
	}

	directives := runDirectives{}
	hasDirective := false

	if v, ok := obj["_jq"]; ok {
		hasDirective = true
		s, ok := v.(string)
		if !ok {
			return nil, runDirectives{}, fmt.Errorf("_jq must be a string")
		}
		directives.JQ = s
		delete(obj, "_jq")
	}

	if !hasDirective {
		return args, directives, nil
	}
	return obj, directives, nil
}

func (c *Client) Run(ctx context.Context, name string, args any) (json.RawMessage, error) {
	serverArgs, directives, err := splitRunArgs(args)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(serverArgs)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/tools/"+name+"/run", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := checkStatus(resp); err != nil {
		return nil, fmt.Errorf("run tool %s: %w", name, err)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}

	if directives.JQ == "" {
		return envelope.Result, nil
	}

	filtered, err := JQ(envelope.Result, directives.JQ)
	if err != nil {
		return nil, fmt.Errorf("run tool %s jq filter: %w", name, err)
	}
	return filtered, nil
}

func (c *Client) RunStream(ctx context.Context, name string, args any) (<-chan tap.Event, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/tools/"+name+"/run", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}

	if err := checkStatus(resp); err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("run stream %s: %w", name, err)
	}

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		defer resp.Body.Close()
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return nil, err
		}
		ch := make(chan tap.Event, 2)
		ch <- tap.Event{Type: "result", Data: envelope.Result}
		ch <- tap.Event{Type: "done"}
		close(ch)
		return ch, nil
	}

	ch := make(chan tap.Event, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		// SSE payload lines can be large (single-line JSON blobs).
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		var eventType string
		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimPrefix(line, "event: ")
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var parsed any
				if err := json.Unmarshal([]byte(data), &parsed); err != nil {
					parsed = data
				}
				if eventType == "" {
					eventType = "result"
				}
				ch <- tap.Event{Type: eventType, Data: parsed}
				eventType = ""
				continue
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- tap.Event{
				Type: "error",
				Data: tap.NewError(tap.ErrExecution, "stream read error: "+err.Error()),
			}
		}
	}()

	return ch, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	var tErr tap.Error
	if json.Unmarshal(body, &tErr) == nil && tErr.Code != "" {
		return &tErr
	}
	return &tap.Error{
		Code:    codeFromStatus(resp.StatusCode),
		Message: truncateMessage(string(body), maxErrorMessageLen),
	}
}

func truncateMessage(s string, max int) string {
	const suffix = "... [truncated]"
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= len(suffix) {
		return suffix[:max]
	}
	return s[:max-len(suffix)] + suffix
}

func codeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return tap.ErrInvalidRequest
	case http.StatusRequestEntityTooLarge:
		return tap.ErrInvalidRequest
	case http.StatusUnauthorized:
		return tap.ErrUnauthorized
	case http.StatusNotFound:
		return tap.ErrNotFound
	case http.StatusRequestTimeout:
		return tap.ErrTimeout
	case http.StatusTooManyRequests:
		return tap.ErrRateLimited
	default:
		return tap.ErrExecution
	}
}
