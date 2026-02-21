package client

import (
	"encoding/json"
	"testing"
)

func TestJQIdentity(t *testing.T) {
	in := json.RawMessage(`{"a":1,"b":2}`)
	out, err := JQ(in, ".")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	if got["a"] != float64(1) || got["b"] != float64(2) {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestJQFieldSelect(t *testing.T) {
	in := json.RawMessage(`{"name":"alice","age":30,"email":"a@b.com"}`)
	out, err := JQ(in, "{name, age}")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	if got["name"] != "alice" || got["age"] != float64(30) {
		t.Fatalf("unexpected: %s", out)
	}
	if _, ok := got["email"]; ok {
		t.Fatal("email should be filtered out")
	}
}

func TestJQArrayMap(t *testing.T) {
	in := json.RawMessage(`[{"id":1,"v":"x"},{"id":2,"v":"y"}]`)
	out, err := JQ(in, "[.[].id]")
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	json.Unmarshal(out, &got)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestJQNestedPath(t *testing.T) {
	in := json.RawMessage(`{"response":{"data":{"items":[1,2,3]},"meta":"ignore"}}`)
	out, err := JQ(in, ".response.data.items")
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	json.Unmarshal(out, &got)
	if len(got) != 3 {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestJQFilter(t *testing.T) {
	in := json.RawMessage(`[{"name":"a","score":10},{"name":"b","score":90},{"name":"c","score":50}]`)
	out, err := JQ(in, `[.[] | select(.score > 40)]`)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	json.Unmarshal(out, &got)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d: %s", len(got), out)
	}
}

func TestJQMultipleOutputs(t *testing.T) {
	in := json.RawMessage(`{"a":1,"b":2}`)
	out, err := JQ(in, ".a, .b")
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	json.Unmarshal(out, &got)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestJQInvalidExpr(t *testing.T) {
	in := json.RawMessage(`{"a":1}`)
	_, err := JQ(in, ".[invalid")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestJQNullResult(t *testing.T) {
	in := json.RawMessage(`{"a":1}`)
	out, err := JQ(in, ".missing")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "null" {
		t.Fatalf("expected null, got %s", out)
	}
}
