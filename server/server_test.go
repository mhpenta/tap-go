package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mhpenta/tap-go"
	"github.com/mhpenta/tap-go/client"
	"github.com/mhpenta/tap-go/server"
)

func testServer() (*server.Server, *httptest.Server) {
	s := server.New("Test Server - Unit test tools")

	s.AddTool(&server.Tool{
		Name:        "search",
		Description: "Search documents",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer", "default": 10},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if p.Limit == 0 {
				p.Limit = 10
			}
			return fmt.Sprintf("results for %q (limit %d)", p.Query, p.Limit), nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "extract",
		Description: "Extract entities",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":  map[string]any{"type": "string"},
				"types": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"text"},
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var p struct {
				Text  string   `json:"text"`
				Types []string `json:"types"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			return map[string]any{
				"entities": []string{"Alice", "Bob"},
				"types":    p.Types,
			}, nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "fail",
		Description: "Always fails",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, fmt.Errorf("something broke")
		},
	})

	s.AddTool(&server.Tool{
		Name:        "single",
		Description: "Streams a single result",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return map[string]any{"answer": 42}, nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			stream.Result(map[string]any{"answer": 42})
			return nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "slow",
		Description: "Streams progress then result",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "final result", nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			stream.Progress(0.5, "halfway")
			stream.Progress(1.0, "done")
			stream.Result("final result")
			return nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "chunked",
		Description: "Streams multiple results",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return []string{"chunk1", "chunk2", "chunk3"}, nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			stream.Result("chunk1")
			stream.Result("chunk2")
			stream.Result("chunk3")
			return nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "stream_fail",
		Description: "Streams then errors",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, fmt.Errorf("boom")
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			stream.Progress(0.25, "starting")
			return fmt.Errorf("boom mid-stream")
		},
	})

	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	return s, ts
}

func TestListTools(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	result, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Description != "Test Server - Unit test tools" {
		t.Fatalf("expected description, got %q", result.Description)
	}
	if len(result.Tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "search" {
		t.Fatalf("expected first tool 'search', got %q", result.Tools[0].Name)
	}
}

func TestListToolsNoDescription(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "ping",
		Description: "Ping",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "pong", nil
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	result, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Description != "" {
		t.Fatalf("expected empty description, got %q", result.Description)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
}

func TestGetTool(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	doc, err := c.Get(context.Background(), "search")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Name != "search" {
		t.Fatalf("expected name 'search', got %q", doc.Name)
	}
	if doc.Parameters == nil {
		t.Fatal("expected parameters to be non-nil")
	}
}

func TestGetToolNotFound(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Get(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestRunToolString(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	raw, err := c.Run(context.Background(), "search", map[string]any{"query": "attention", "limit": 5})
	if err != nil {
		t.Fatal(err)
	}

	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	expected := `results for "attention" (limit 5)`
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestRunToolStructured(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)

	raw, err := c.Run(context.Background(), "extract", map[string]any{
		"text":  "Alice met Bob",
		"types": []string{"person"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Entities []string `json:"entities"`
		Types    []string `json:"types"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(result.Entities))
	}
	if len(result.Types) != 1 || result.Types[0] != "person" {
		t.Fatalf("expected types [person], got %v", result.Types)
	}
}

func TestRunToolError(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "fail", map[string]any{})
	if err == nil {
		t.Fatal("expected error for failed tool")
	}
}

func TestRunToolNotFound(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "nope", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestRunToolInvalidJSON(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/search/run", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRunToolRejectsLargeBody(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	// Default server cap is 1 MiB; this payload is intentionally larger.
	tooLarge := `{"query":"` + strings.Repeat("x", 2<<20) + `"}`
	req, _ := http.NewRequest("POST", ts.URL+"/tools/search/run", strings.NewReader(tooLarge))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var tErr tinymcp.Error
	if err := json.Unmarshal(body, &tErr); err != nil {
		t.Fatalf("expected structured error JSON, got: %s", body)
	}
	if tErr.Code != tinymcp.ErrInvalidRequest {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrInvalidRequest, tErr.Code)
	}
	if !strings.Contains(tErr.Message, "too large") {
		t.Fatalf("expected size limit message, got: %q", tErr.Message)
	}
}

func TestRunToolEmptyBody(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "fail", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %s", err, err)
	}
	if tErr.Code != tinymcp.ErrExecution {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrExecution, tErr.Code)
	}
	if !strings.Contains(tErr.Message, "something broke") {
		t.Fatalf("expected message containing 'something broke', got: %s", tErr.Message)
	}
}

func TestErrorResponseFormat(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/tools/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var errResp tinymcp.Error
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("error response is not valid JSON: %s", body)
	}
	if errResp.Code != tinymcp.ErrNotFound {
		t.Fatalf("expected error code %q, got %q", tinymcp.ErrNotFound, errResp.Code)
	}
	if errResp.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

// --- Auth tests ---

func testAuthServer() *httptest.Server {
	s := server.New("Auth Test")
	s.AddTool(&server.Tool{
		Name:        "ping",
		Description: "Ping",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "pong", nil
		},
	})

	apiKey := "secret"
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != apiKey {
				http.Error(w, `{"code":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	s.Register(mux, auth)
	return httptest.NewServer(mux)
}

func TestAPIKeyRequired(t *testing.T) {
	ts := testAuthServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.List(context.Background())
	if err == nil {
		t.Fatal("expected auth error without key")
	}

	c = client.New(ts.URL, client.WithAPIKey("wrong"))
	_, err = c.List(context.Background())
	if err == nil {
		t.Fatal("expected auth error with wrong key")
	}

	c = client.New(ts.URL, client.WithAPIKey("secret"))
	result, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
}

func TestAPIKeyOnAllEndpoints(t *testing.T) {
	ts := testAuthServer()
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/tools")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /tools, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, _ = http.Get(ts.URL + "/tools/ping")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /tools/ping, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp, _ = http.Post(ts.URL+"/tools/ping/run", "application/json", strings.NewReader("{}"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /tools/ping/run, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Streaming tests ---

func TestStreamProgress(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	ch, err := c.RunStream(context.Background(), "slow", map[string]any{"query": "test"})
	if err != nil {
		t.Fatal(err)
	}

	var events []tinymcp.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
done:

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != "progress" {
		t.Fatalf("expected progress, got %s", events[0].Type)
	}
	if events[1].Type != "progress" {
		t.Fatalf("expected progress, got %s", events[1].Type)
	}
	if events[2].Type != "result" {
		t.Fatalf("expected result, got %s", events[2].Type)
	}
	if events[3].Type != "done" {
		t.Fatalf("expected done, got %s", events[3].Type)
	}
}

func TestStreamChunked(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	ch, err := c.RunStream(context.Background(), "chunked", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	var results []tinymcp.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			results = append(results, ev)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
done:

	if len(results) != 4 {
		t.Fatalf("expected 4 events, got %d", len(results))
	}
	for i := 0; i < 3; i++ {
		if results[i].Type != "result" {
			t.Fatalf("event %d: expected result type, got %s", i, results[i].Type)
		}
	}
	if results[3].Type != "done" {
		t.Fatalf("expected done, got %s", results[3].Type)
	}
}

func TestStreamError(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	ch, err := c.RunStream(context.Background(), "stream_fail", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	var events []tinymcp.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
done:

	if len(events) < 1 {
		t.Fatal("expected at least 1 event")
	}
	last := events[len(events)-1]
	if last.Type != "error" {
		t.Fatalf("expected last event to be error, got %s", last.Type)
	}
}

func TestStreamFallbackToJSON(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "basic",
		Description: "No stream handler",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "just json", nil
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	ch, err := c.RunStream(context.Background(), "basic", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	ev, ok := <-ch
	if !ok {
		t.Fatal("expected at least one event")
	}
	if ev.Type != "result" {
		t.Fatalf("expected result, got %s", ev.Type)
	}

	ev, ok = <-ch
	if !ok {
		t.Fatal("expected done event")
	}
	if ev.Type != "done" {
		t.Fatalf("expected done, got %s", ev.Type)
	}

	_, ok = <-ch
	if ok {
		t.Fatal("expected channel to be closed")
	}
}

func TestTrailingSlashInBaseURL(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL + "/")
	result, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("trailing slash should not break: %v", err)
	}
	if len(result.Tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(result.Tools))
	}
}

// --- Raw wire format tests ---
// These verify exactly what a human would see with curl.

func TestWireFormatSingleResult(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/single/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	// A single-result stream looks like:
	//   event: result
	//   data: {"answer":42}
	//
	// (two lines + blank line, that's it)

	if !strings.Contains(raw, "event: result\n") {
		t.Fatalf("missing 'event: result' line in:\n%s", raw)
	}
	if !strings.Contains(raw, `data: {"answer":42}`) {
		t.Fatalf("missing data line in:\n%s", raw)
	}
	if !strings.Contains(raw, "event: done\n") {
		t.Fatalf("missing 'event: done' line in:\n%s", raw)
	}

	events := parseSSEEvents(raw)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (result + done), got %d:\n%s", len(events), raw)
	}
	if events[0].eventType != "result" {
		t.Fatalf("expected result, got %s", events[0].eventType)
	}
	if events[1].eventType != "done" {
		t.Fatalf("expected done, got %s", events[1].eventType)
	}
}

func TestWireFormatProgressThenResult(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/slow/run", strings.NewReader(`{"query":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	// Should look like:
	//   event: progress
	//   data: {"message":"halfway","progress":0.5}
	//
	//   event: progress
	//   data: {"message":"done","progress":1}
	//
	//   event: result
	//   data: "final result"
	//

	events := parseSSEEvents(raw)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d:\n%s", len(events), raw)
	}
	if events[0].eventType != "progress" {
		t.Fatalf("event 0: expected progress, got %s", events[0].eventType)
	}
	if events[1].eventType != "progress" {
		t.Fatalf("event 1: expected progress, got %s", events[1].eventType)
	}
	if events[2].eventType != "result" {
		t.Fatalf("event 2: expected result, got %s", events[2].eventType)
	}
	if events[2].data != `"final result"` {
		t.Fatalf("event 2: expected '\"final result\"', got %s", events[2].data)
	}
	if events[3].eventType != "done" {
		t.Fatalf("event 3: expected done, got %s", events[3].eventType)
	}
}

func TestWireFormatChunked(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/chunked/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	events := parseSSEEvents(raw)
	if len(events) != 4 {
		t.Fatalf("expected 4 events (3 results + done), got %d:\n%s", len(events), raw)
	}
	for i := 0; i < 3; i++ {
		if events[i].eventType != "result" {
			t.Fatalf("event %d: expected result, got %s", i, events[i].eventType)
		}
	}
	if events[0].data != `"chunk1"` || events[1].data != `"chunk2"` || events[2].data != `"chunk3"` {
		t.Fatalf("unexpected chunk data:\n%s", raw)
	}
	if events[3].eventType != "done" {
		t.Fatalf("expected done, got %s", events[3].eventType)
	}
}

func TestWireFormatErrorMidStream(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/stream_fail/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	raw := string(body)

	events := parseSSEEvents(raw)
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (progress + error), got %d:\n%s", len(events), raw)
	}
	if events[0].eventType != "progress" {
		t.Fatalf("event 0: expected progress, got %s", events[0].eventType)
	}
	last := events[len(events)-1]
	if last.eventType != "error" {
		t.Fatalf("last event: expected error, got %s", last.eventType)
	}
	var sseErr tinymcp.Error
	if err := json.Unmarshal([]byte(last.data), &sseErr); err != nil {
		t.Fatalf("error event data is not structured error: %s", last.data)
	}
	if sseErr.Code != tinymcp.ErrExecution {
		t.Fatalf("expected error code %q, got %q", tinymcp.ErrExecution, sseErr.Code)
	}
	if !strings.Contains(sseErr.Message, "boom mid-stream") {
		t.Fatalf("error message should contain 'boom mid-stream', got: %s", sseErr.Message)
	}
}

func TestWireFormatNoAcceptHeaderGetsJSON(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/single/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json without Accept header, got %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("expected JSON response, got: %s", body)
	}
	if envelope.Result["answer"] != float64(42) {
		t.Fatalf("expected answer=42, got %v", envelope.Result)
	}
}

type sseEvent struct {
	eventType string
	data      string
}

func parseSSEEvents(raw string) []sseEvent {
	var events []sseEvent
	var current sseEvent
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "event: ") {
			current.eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			current.data = strings.TrimPrefix(line, "data: ")
		} else if line == "" && current.eventType != "" {
			events = append(events, current)
			current = sseEvent{}
		}
	}
	return events
}

// --- Structured error tests ---

func TestErrorCodeNotFound(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "nope", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %s", err, err)
	}
	if tErr.Code != tinymcp.ErrNotFound {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrNotFound, tErr.Code)
	}
}

func TestErrorCodeInvalidRequest(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/search/run", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var tErr tinymcp.Error
	if err := json.Unmarshal(body, &tErr); err != nil {
		t.Fatalf("expected structured error, got: %s", body)
	}
	if tErr.Code != tinymcp.ErrInvalidRequest {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrInvalidRequest, tErr.Code)
	}
}

func TestErrorCodeExecution(t *testing.T) {
	_, ts := testServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "fail", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %s", err, err)
	}
	if tErr.Code != tinymcp.ErrExecution {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrExecution, tErr.Code)
	}
	if !strings.Contains(tErr.Message, "something broke") {
		t.Fatalf("expected message about 'something broke', got: %s", tErr.Message)
	}
}

func TestErrorCodeUnauthorized(t *testing.T) {
	ts := testAuthServer()
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %s", err, err)
	}
	if tErr.Code != tinymcp.ErrUnauthorized {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrUnauthorized, tErr.Code)
	}
}

func TestHandlerCanReturnTypedError(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "typed_fail",
		Description: "Returns a typed error",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, tinymcp.NewError(tinymcp.ErrInvalidRequest, "field 'cik' is required")
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "typed_fail", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %s", err, err)
	}
	if tErr.Code != tinymcp.ErrInvalidRequest {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrInvalidRequest, tErr.Code)
	}
	if tErr.Message != "field 'cik' is required" {
		t.Fatalf("expected specific message, got: %s", tErr.Message)
	}
}

func TestHandlerTypedErrorUsesMatchingHTTPStatus(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "typed_fail",
		Description: "Returns a typed error",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return nil, tinymcp.NewError(tinymcp.ErrInvalidRequest, "field 'cik' is required")
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/typed_fail/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid_request, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var tErr tinymcp.Error
	if err := json.Unmarshal(body, &tErr); err != nil {
		t.Fatalf("expected structured error, got: %s", body)
	}
	if tErr.Code != tinymcp.ErrInvalidRequest {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrInvalidRequest, tErr.Code)
	}
}

func TestAddToolPanicsWithoutHandlers(t *testing.T) {
	s := server.New("")

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic for tool without handlers")
		}
		if !strings.Contains(fmt.Sprint(recovered), "missing handler") {
			t.Fatalf("unexpected panic message: %v", recovered)
		}
	}()

	s.AddTool(&server.Tool{
		Name:        "invalid",
		Description: "no handlers",
	})
}

func TestAddToolDuplicateNameReplaces(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "dup",
		Description: "first",
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "first", nil
		},
	})

	s.AddTool(&server.Tool{
		Name:        "dup",
		Description: "second",
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "second", nil
		},
	})

	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 {
		t.Fatalf("expected duplicate registration to replace in-place, got %d tools", len(list.Tools))
	}
	if list.Tools[0].Name != "dup" {
		t.Fatalf("expected tool name dup, got %q", list.Tools[0].Name)
	}
	if list.Tools[0].Description != "second" {
		t.Fatalf("expected latest description, got %q", list.Tools[0].Description)
	}

	raw, err := c.Run(context.Background(), "dup", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Fatalf("expected replacement handler result %q, got %q", "second", got)
	}
}

func TestRunRecoversFromHandlerPanic(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "panic_handler",
		Description: "panics in handler",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			panic("handler boom")
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.Run(context.Background(), "panic_handler", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
	var tErr *tinymcp.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tinymcp.Error, got %T: %v", err, err)
	}
	if tErr.Code != tinymcp.ErrExecution {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrExecution, tErr.Code)
	}
	if !strings.Contains(tErr.Message, "tool panicked: handler boom") {
		t.Fatalf("unexpected message: %q", tErr.Message)
	}
}

func TestWireFormatRecoversFromStreamHandlerPanic(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "panic_stream",
		Description: "panics in stream handler",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "fallback", nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			stream.Progress(0.1, "starting")
			panic("stream boom")
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/panic_stream/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	events := parseSSEEvents(string(body))
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (progress + error), got %d: %s", len(events), body)
	}
	last := events[len(events)-1]
	if last.eventType != "error" {
		t.Fatalf("expected terminal error event, got %q", last.eventType)
	}

	var tErr tinymcp.Error
	if err := json.Unmarshal([]byte(last.data), &tErr); err != nil {
		t.Fatalf("expected structured error JSON, got %s", last.data)
	}
	if tErr.Code != tinymcp.ErrExecution {
		t.Fatalf("expected code %q, got %q", tinymcp.ErrExecution, tErr.Code)
	}
	if !strings.Contains(tErr.Message, "tool panicked: stream boom") {
		t.Fatalf("unexpected message: %q", tErr.Message)
	}
}

func TestStreamMarshalFailureDoesNotBlockHandler(t *testing.T) {
	s := server.New("")
	handlerDone := make(chan struct{})
	s.AddTool(&server.Tool{
		Name:        "marshal_fail",
		Description: "emits invalid payload then many events",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "fallback", nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tinymcp.Stream) error {
			defer close(handlerDone)

			// json.Marshal cannot encode func values; this triggers an SSE encode error.
			stream.Result(func() {})
			for i := 0; i < 200; i++ {
				stream.Progress(float64(i), "tick")
			}
			return nil
		},
	})

	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/tools/marshal_fail/run", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	select {
	case <-handlerDone:
	case <-time.After(1 * time.Second):
		t.Fatal("stream handler blocked after marshal failure")
	}
}

func TestAddToolConcurrentWithReads(t *testing.T) {
	s := server.New("Concurrent registration")
	s.AddTool(&server.Tool{
		Name:        "seed",
		Description: "seed",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	const n = 100
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			s.AddTool(&server.Tool{
				Name:        fmt.Sprintf("dyn_%d", i),
				Description: "dynamic",
				Parameters:  map[string]any{},
				Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
					return "ok", nil
				},
			})
		}
		close(done)
	}()

	for i := 0; i < n; i++ {
		resp, err := http.Get(ts.URL + "/tools")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for concurrent AddTool loop")
	}
}
