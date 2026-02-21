package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/mhpenta/tap-go"
)

type Tool struct {
	Name          string
	Description   string
	Parameters    map[string]any
	Handler       tinymcp.Handler
	StreamHandler tinymcp.StreamHandler
}

type Option func(*Server)

type Server struct {
	mu          sync.RWMutex
	tools       map[string]*Tool
	order       []string
	description string
	logger      *slog.Logger
	maxBodySize int64
}

const defaultMaxRequestBodyBytes int64 = 1 << 20 // 1 MiB

func New(description string, opts ...Option) *Server {
	s := &Server{
		tools:       make(map[string]*Tool),
		description: description,
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		maxBodySize: defaultMaxRequestBodyBytes,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) {
		if logger == nil {
			return
		}
		s.logger = logger
	}
}

func WithMaxRequestBodyBytes(n int64) Option {
	return func(s *Server) {
		if n <= 0 {
			return
		}
		s.maxBodySize = n
	}
}

// AddTool adds a tool to the server.
//
// If a tool with the same name already exists, it is replaced.
// AddTool panics if t is nil or if both Handler and StreamHandler are nil.
func (s *Server) AddTool(t *Tool) {
	if t == nil {
		panic("server.AddTool: nil tool")
	}

	if t.Handler == nil && t.StreamHandler == nil {
		panic(fmt.Errorf("AddTool %q: missing handler", t.Name))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tool := *t
	tool.Parameters = cloneParameters(t.Parameters)
	if _, exists := s.tools[t.Name]; !exists {
		s.order = append(s.order, t.Name)
	}
	s.tools[t.Name] = &tool
	s.logger.Debug("tool registered", "tool", t.Name, "has_stream", t.StreamHandler != nil)
}

func (s *Server) Register(mux *http.ServeMux, middleware func(http.Handler) http.Handler) {
	handle := func(pattern string, h http.HandlerFunc) {
		if middleware != nil {
			mux.Handle(pattern, middleware(h))
		} else {
			mux.HandleFunc(pattern, h)
		}
	}

	handle("GET /tools", s.handleIndex)
	handle("GET /tools/{name}", s.handleDoc)
	handle("POST /tools/{name}/run", s.handleRun)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	type summary struct {
		name string
		desc string
	}

	s.mu.RLock()
	description := s.description
	tools := make([]summary, 0, len(s.order))
	for _, name := range s.order {
		if t, ok := s.tools[name]; ok {
			tools = append(tools, summary{name: t.Name, desc: t.Description})
		}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain")
	if description != "" {
		fmt.Fprintf(w, "%s\n\n", description)
	}
	for _, t := range tools {
		fmt.Fprintf(w, "%s: %s\n", t.name, t.desc)
	}
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.RLock()
	t, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		s.logger.Warn("tool doc not found", "tool", name, "path", r.URL.Path)
		writeError(w, http.StatusNotFound, tinymcp.ErrNotFound, "tool not found: "+name)
		return
	}

	type doc struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	}
	writeJSON(w, http.StatusOK, doc{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  cloneParameters(t.Parameters),
	})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.RLock()
	t, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		s.logger.Warn("tool run not found", "tool", name, "path", r.URL.Path)
		writeError(w, http.StatusNotFound, tinymcp.ErrNotFound, "tool not found: "+name)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodySize)
	var args json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.logger.Warn("tool run request body too large", "tool", name, "max_bytes", s.maxBodySize)
			writeError(w, http.StatusBadRequest, tinymcp.ErrInvalidRequest, fmt.Sprintf("request body too large (max %d bytes)", s.maxBodySize))
			return
		}
		s.logger.Warn("tool run invalid json", "tool", name, "error", err.Error())
		writeError(w, http.StatusBadRequest, tinymcp.ErrInvalidRequest, "invalid JSON body: "+err.Error())
		return
	}

	wantsStream := t.StreamHandler != nil && acceptsSSE(r)

	if wantsStream {
		s.handleStreamRun(w, r, t, args)
		return
	}

	if t.Handler == nil {
		s.logger.Warn("tool requires streaming", "tool", name)
		writeError(w, http.StatusBadRequest, tinymcp.ErrInvalidRequest, "tool requires streaming")
		return
	}

	// Recover panics from user handler code and return a structured execution_error.
	result, err := runHandlerSafely(r.Context(), t.Handler, args)
	if err != nil {
		code := tinymcp.ErrExecution
		msg := err.Error()
		var tErr *tinymcp.Error
		if errors.As(err, &tErr) {
			code = tErr.Code
			msg = tErr.Message
		}
		s.logger.Warn("tool run failed", "tool", name, "code", code, "error", msg)
		writeError(w, statusFromCode(code), code, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleStreamRun(w http.ResponseWriter, r *http.Request, t *Tool, args json.RawMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.logger.Error("streaming not supported by response writer", "tool", t.Name)
		writeError(w, http.StatusInternalServerError, tinymcp.ErrExecution, "streaming not supported")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	stream := tinymcp.NewStream()
	hadError := make(chan bool, 1)

	go func() {
		defer stream.Close()
		if err := runStreamHandlerSafely(ctx, t.StreamHandler, args, stream); err != nil {
			code := tinymcp.ErrExecution
			msg := err.Error()
			var tErr *tinymcp.Error
			if errors.As(err, &tErr) {
				code = tErr.Code
				msg = tErr.Message
			}
			s.logger.Warn("stream handler failed", "tool", t.Name, "code", code, "error", msg)
			stream.Error(code, msg)
			hadError <- true
		} else {
			hadError <- false
		}
	}()

	writeFailed := false
	for ev := range stream.Events() {
		if writeFailed {
			continue
		}

		data, err := json.Marshal(ev.Data)
		if err != nil {
			sseErr, _ := json.Marshal(tinymcp.NewError(tinymcp.ErrExecution, "marshal failed"))
			_ = writeSSEEvent(w, flusher, "error", sseErr)
			s.logger.Error("failed to marshal stream event", "tool", t.Name, "event_type", ev.Type)
			writeFailed = true
			cancel()
			continue
		}
		if err := writeSSEEvent(w, flusher, ev.Type, data); err != nil {
			s.logger.Warn("failed to write stream event", "tool", t.Name, "event_type", ev.Type, "error", err.Error())
			writeFailed = true
			cancel()
			continue
		}
	}

	if !<-hadError && !writeFailed {
		_ = writeSSEEvent(w, flusher, "done", []byte("{}"))
	}
}

func runHandlerSafely(ctx context.Context, handler tinymcp.Handler, args json.RawMessage) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicToExecutionError(recovered)
		}
	}()
	return handler(ctx, args)
}

func runStreamHandlerSafely(ctx context.Context, handler tinymcp.StreamHandler, args json.RawMessage, stream *tinymcp.Stream) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicToExecutionError(recovered)
		}
	}()
	return handler(ctx, args, stream)
}

func panicToExecutionError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return tinymcp.NewError(tinymcp.ErrExecution, "tool panicked: "+err.Error())
	}
	return tinymcp.NewError(tinymcp.ErrExecution, "tool panicked: "+fmt.Sprint(recovered))
}

func acceptsSSE(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/event-stream")
}

func statusFromCode(code string) int {
	switch code {
	case tinymcp.ErrInvalidRequest:
		return http.StatusBadRequest
	case tinymcp.ErrUnauthorized:
		return http.StatusUnauthorized
	case tinymcp.ErrNotFound:
		return http.StatusNotFound
	case tinymcp.ErrTimeout:
		return http.StatusRequestTimeout
	case tinymcp.ErrRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data []byte) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func cloneParameters(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(tinymcp.NewError(code, message))
}
