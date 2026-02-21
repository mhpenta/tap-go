package tap

import (
	"context"
	"encoding/json"
	"sync"
)

type Handler func(ctx context.Context, args json.RawMessage) (any, error)

type StreamHandler func(ctx context.Context, args json.RawMessage, stream *Stream) error

type Event struct {
	Type string
	Data any
}

type Stream struct {
	ch        chan Event
	closeOnce sync.Once
}

func NewStream() *Stream {
	return &Stream{ch: make(chan Event, 64)}
}

func (s *Stream) emit(ev Event) {
	// Stream consumers may close early; treat post-close emits as no-ops.
	defer func() { _ = recover() }()
	s.ch <- ev
}

func (s *Stream) Progress(progress float64, message string) {
	s.emit(Event{
		Type: "progress",
		Data: map[string]any{"progress": progress, "message": message},
	})
}

func (s *Stream) Result(data any) {
	s.emit(Event{Type: "result", Data: data})
}

func (s *Stream) Error(code, message string) {
	s.emit(Event{
		Type: "error",
		Data: &Error{Code: code, Message: message},
	})
}

func (s *Stream) Close() {
	s.closeOnce.Do(func() {
		close(s.ch)
	})
}

func (s *Stream) Events() <-chan Event {
	return s.ch
}
