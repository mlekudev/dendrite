package enzyme

import (
	"strings"
	"testing"
)

func TestTextDigest(t *testing.T) {
	input := "hello world! 42"
	ch := Text{}.Digest(strings.NewReader(input))

	var elems []struct{ tag, val string }
	for e := range ch {
		elems = append(elems, struct{ tag, val string }{e.Type(), e.Value().(string)})
	}

	expected := []struct{ tag, val string }{
		{"w3", "hello"},
		{"space", " "},
		{"w3", "world"},
		{"punct", "!"},
		{"space", " "},
		{"w2", "42"},
	}

	if len(elems) != len(expected) {
		t.Fatalf("expected %d elements, got %d: %+v", len(expected), len(elems), elems)
	}
	for i, e := range elems {
		if e != expected[i] {
			t.Errorf("element %d: expected %+v, got %+v", i, expected[i], e)
		}
	}
}

func TestTextDigestEmpty(t *testing.T) {
	ch := Text{}.Digest(strings.NewReader(""))
	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 elements from empty input, got %d", count)
	}
}

func TestLinesDigest(t *testing.T) {
	input := "first line\nsecond line\nthird"
	ch := Lines{}.Digest(strings.NewReader(input))

	var lines []string
	for e := range ch {
		if e.Type() != "line" {
			t.Errorf("expected tag 'line', got %q", e.Type())
		}
		lines = append(lines, e.Value().(string))
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "first line" || lines[1] != "second line" || lines[2] != "third" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestElem(t *testing.T) {
	e := Elem("test", "value")
	if e.Type() != "test" {
		t.Errorf("expected type 'test', got %q", e.Type())
	}
	if e.Value().(string) != "value" {
		t.Errorf("expected value 'value', got %v", e.Value())
	}
}
