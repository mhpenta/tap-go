package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mhpenta/tap-go"
	"github.com/mhpenta/tap-go/client"
	"github.com/mhpenta/tap-go/server"
)

func newTestClientServer(t *testing.T, handler func(ctx context.Context, args json.RawMessage) (any, error)) (*client.Client, func()) {
	t.Helper()

	s := server.New("test")
	s.AddTool(&server.Tool{
		Name:        "demo",
		Description: "demo tool",
		Parameters:  map[string]any{},
		Handler:     handler,
	})

	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)

	return client.New(ts.URL), ts.Close
}

func TestRunAppliesJQAndStripsDirective(t *testing.T) {
	var gotArgs map[string]any

	c, cleanup := newTestClientServer(t, func(ctx context.Context, args json.RawMessage) (any, error) {
		if err := json.Unmarshal(args, &gotArgs); err != nil {
			return nil, err
		}
		return map[string]any{
			"numbers": []int{1, 2, 3},
			"meta":    "keep-on-server",
		}, nil
	})
	defer cleanup()

	raw, err := c.Run(context.Background(), "demo", map[string]any{
		"query": "abc",
		"_jq":   ".numbers[0:2]",
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("unexpected jq output: %s", raw)
	}
	if _, exists := gotArgs["_jq"]; exists {
		t.Fatalf("_jq should not be sent to server, args=%v", gotArgs)
	}
	if gotArgs["query"] != "abc" {
		t.Fatalf("expected query arg to reach server, got %v", gotArgs)
	}
}

func TestRunWithoutJQThenWithJQ(t *testing.T) {
	var seenArgs []map[string]any

	c, cleanup := newTestClientServer(t, func(ctx context.Context, args json.RawMessage) (any, error) {
		var got map[string]any
		if err := json.Unmarshal(args, &got); err != nil {
			return nil, err
		}
		seenArgs = append(seenArgs, got)

		return map[string]any{
			"status": "ok",
			"response": map[string]any{
				"items": []map[string]any{
					{"id": 1, "name": "alpha", "score": 99},
					{"id": 2, "name": "beta", "score": 75},
					{"id": 3, "name": "gamma", "score": 42},
				},
				"meta": map[string]any{
					"source": "unit-test",
					"count":  3,
				},
			},
		}, nil
	})
	defer cleanup()

	raw, err := c.Run(context.Background(), "demo", map[string]any{"query": "abc"})
	if err != nil {
		t.Fatal(err)
	}

	var full map[string]any
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	if full["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", full["status"])
	}
	response, ok := full["response"].(map[string]any)
	if !ok {
		t.Fatalf("expected response object, got %T", full["response"])
	}
	items, ok := response["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("expected 3 response items, got %v", response["items"])
	}

	filteredRaw, err := c.Run(context.Background(), "demo", map[string]any{
		"query": "abc",
		"_jq":   ".response.items | map(.id)",
	})
	if err != nil {
		t.Fatal(err)
	}

	var filtered []int
	if err := json.Unmarshal(filteredRaw, &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 3 || filtered[0] != 1 || filtered[1] != 2 || filtered[2] != 3 {
		t.Fatalf("unexpected jq output: %s", filteredRaw)
	}

	if len(seenArgs) != 2 {
		t.Fatalf("expected handler to be called twice, got %d", len(seenArgs))
	}
	if seenArgs[0]["query"] != "abc" {
		t.Fatalf("expected query on first call, got %v", seenArgs[0])
	}
	if _, exists := seenArgs[1]["_jq"]; exists {
		t.Fatalf("_jq should not be sent to server, args=%v", seenArgs[1])
	}
	if seenArgs[1]["query"] != "abc" {
		t.Fatalf("expected query on second call, got %v", seenArgs[1])
	}
}

func TestRunRejectsNonStringJQDirective(t *testing.T) {
	c, cleanup := newTestClientServer(t, func(ctx context.Context, args json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	defer cleanup()

	_, err := c.Run(context.Background(), "demo", map[string]any{"_jq": 123})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "_jq must be a string") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunReturnsJQError(t *testing.T) {
	c, cleanup := newTestClientServer(t, func(ctx context.Context, args json.RawMessage) (any, error) {
		return map[string]any{"a": 1}, nil
	})
	defer cleanup()

	_, err := c.Run(context.Background(), "demo", map[string]any{"_jq": ".[bad"})
	if err == nil {
		t.Fatal("expected jq error")
	}
	if !strings.Contains(err.Error(), "jq filter") {
		t.Fatalf("expected jq filter error context, got: %v", err)
	}
}

func TestListDescriptionWithColonNotParsedAsTool(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Book Service: test environment\nSupports books and notes.\n\nsearch: Search books\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	result, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	wantDesc := "Book Service: test environment\nSupports books and notes."
	if result.Description != wantDesc {
		t.Fatalf("unexpected description: %q", result.Description)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "search" {
		t.Fatalf("expected tool name search, got %q", result.Tools[0].Name)
	}
}

func TestRunStreamLargeEventPayload(t *testing.T) {
	s := server.New("")
	s.AddTool(&server.Tool{
		Name:        "big",
		Description: "large stream payload",
		Parameters:  map[string]any{},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			return "fallback", nil
		},
		StreamHandler: func(ctx context.Context, args json.RawMessage, stream *tap.Stream) error {
			stream.Result(strings.Repeat("x", 70*1024))
			return nil
		},
	})
	mux := http.NewServeMux()
	s.Register(mux, nil)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	ch, err := c.RunStream(context.Background(), "big", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	timeout := time.After(2 * time.Second)
	seenResult := false
	seenDone := false
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if !seenResult || !seenDone {
					t.Fatalf("expected both result and done events, got result=%v done=%v", seenResult, seenDone)
				}
				return
			}
			switch ev.Type {
			case "result":
				str, ok := ev.Data.(string)
				if !ok {
					t.Fatalf("expected result payload string, got %T", ev.Data)
				}
				if len(str) != 70*1024 {
					t.Fatalf("expected payload len %d, got %d", 70*1024, len(str))
				}
				seenResult = true
			case "done":
				seenDone = true
			case "error":
				t.Fatalf("unexpected stream error event: %#v", ev.Data)
			}
		case <-timeout:
			t.Fatal("timeout waiting for stream events")
		}
	}
}

func TestListTruncatesLargeNonJSONErrorBody(t *testing.T) {
	body := "<html><body>" + strings.Repeat("reverse-proxy-502-", 200) + "</body></html>"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, body)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL)
	_, err := c.List(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	var tErr *tap.Error
	if !errors.As(err, &tErr) {
		t.Fatalf("expected tap.Error, got %T: %v", err, err)
	}
	if tErr.Code != tap.ErrExecution {
		t.Fatalf("expected code %q, got %q", tap.ErrExecution, tErr.Code)
	}
	if len(tErr.Message) > 512 {
		t.Fatalf("expected truncated message <= 512 chars, got %d", len(tErr.Message))
	}
	if !strings.Contains(tErr.Message, "[truncated]") {
		t.Fatalf("expected truncation marker, got: %q", tErr.Message)
	}
}

func TestWithTimeoutOption(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "slow tool index\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := client.New(ts.URL, client.WithTimeout(10*time.Millisecond))
	_, err := c.List(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected timeout-like error, got: %v", err)
	}
}

func TestWithHTTPClientOptionAndTimeoutOverride(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "slow tool index\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	baseClient := &http.Client{Timeout: 2 * time.Second}
	c := client.New(
		ts.URL,
		client.WithHTTPClient(baseClient),
		client.WithTimeout(10*time.Millisecond),
	)

	if baseClient.Timeout != 2*time.Second {
		t.Fatalf("expected original http client timeout unchanged, got %s", baseClient.Timeout)
	}

	_, err := c.List(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected timeout-like error, got: %v", err)
	}
}
