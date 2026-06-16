package config

import (
	"strings"
	"testing"
)

func TestParse_Empty(t *testing.T) {
	got, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestParse_BasicKeyValue(t *testing.T) {
	in := "foo=bar\nbaz=qux\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["foo"] != "bar" || got["baz"] != "qux" {
		t.Errorf("unexpected map: %v", got)
	}
}

func TestParse_TrimsWhitespace(t *testing.T) {
	in := "   key   =   value with spaces   \n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["key"] != "value with spaces" {
		t.Errorf("expected 'value with spaces', got %q", got["key"])
	}
}

func TestParse_CommentsAndBlankLines(t *testing.T) {
	in := "# comment\n\n  # indented comment\nkey=value\n\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got["key"] != "value" {
		t.Errorf("unexpected map: %v", got)
	}
}

func TestParse_ValueWithEquals(t *testing.T) {
	in := "url=http://host:8080/path?a=b&c=d\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://host:8080/path?a=b&c=d"
	if got["url"] != want {
		t.Errorf("expected %q, got %q", want, got["url"])
	}
}

func TestParse_EmptyValue(t *testing.T) {
	in := "key=\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := got["key"]
	if !ok {
		t.Errorf("key missing")
	}
	if v != "" {
		t.Errorf("expected empty value, got %q", v)
	}
}

func TestParse_CRLFLineEndings(t *testing.T) {
	in := "a=1\r\nb=2\r\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["a"] != "1" || got["b"] != "2" {
		t.Errorf("unexpected map: %v", got)
	}
}

func TestParse_DuplicateKeyLastWins(t *testing.T) {
	in := "k=first\nk=second\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["k"] != "second" {
		t.Errorf("expected 'second', got %q", got["k"])
	}
}

func TestParse_MissingEqualsReturnsError(t *testing.T) {
	in := "good=1\nbroken line without equals\nignored=2\n"
	_, err := Parse(strings.NewReader(in))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("expected error mentioning line 2, got %v", err)
	}
}

func TestParse_EmptyKeyReturnsError(t *testing.T) {
	in := "=value\n"
	_, err := Parse(strings.NewReader(in))
	if err == nil {
		t.Fatalf("expected error for empty key, got nil")
	}
}

func TestParse_UTF8BOM(t *testing.T) {
	in := "\xef\xbb\xbffoo=bar\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("expected BOM stripped, got map: %v", got)
	}
}

func TestParse_NoTrailingNewline(t *testing.T) {
	in := "foo=bar"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["foo"] != "bar" {
		t.Errorf("expected 'bar', got %q", got["foo"])
	}
}

func TestParse_HashInValue(t *testing.T) {
	// '#' only counts as comment at start of (trimmed) line, not inside values
	in := "color=#ff0000\n"
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["color"] != "#ff0000" {
		t.Errorf("expected '#ff0000', got %q", got["color"])
	}
}
