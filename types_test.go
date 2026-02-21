package tinymcp

import "testing"

func TestStreamCloseIsIdempotent(t *testing.T) {
	s := NewStream()
	s.Close()
	s.Close()
}

func TestStreamEmitAfterCloseDoesNotPanic(t *testing.T) {
	s := NewStream()
	s.Close()

	s.Progress(0.5, "halfway")
	s.Result("done")
	s.Error(ErrExecution, "failed")
}
