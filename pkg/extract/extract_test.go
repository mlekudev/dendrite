package extract

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPlainTextExtract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "Hello, world! This is a test."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	e := PlainText{}
	if !e.CanExtract(path) {
		t.Error("should handle .txt files")
	}

	rc, err := e.Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("got %q, want %q", string(data), content)
	}
}

func TestPlainTextExtensions(t *testing.T) {
	e := PlainText{}
	tests := []struct {
		path string
		want bool
	}{
		{"foo.txt", true},
		{"foo.md", true},
		{"foo.text", true},
		{"foo", true}, // no extension
		{"foo.pdf", false},
		{"foo.html", false},
		{"foo.docx", false},
	}
	for _, tt := range tests {
		if got := e.CanExtract(tt.path); got != tt.want {
			t.Errorf("CanExtract(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestPandocExtensions(t *testing.T) {
	e := PandocExtractor{}
	tests := []struct {
		path string
		want bool
	}{
		{"foo.html", true},
		{"foo.htm", true},
		{"foo.docx", true},
		{"foo.epub", true},
		{"foo.rtf", true},
		{"foo.txt", false},
		{"foo.pdf", false},
	}
	for _, tt := range tests {
		if got := e.CanExtract(tt.path); got != tt.want {
			t.Errorf("CanExtract(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestPDFExtractorExtension(t *testing.T) {
	e := PDFExtractor{}
	if !e.CanExtract("book.pdf") {
		t.Error("should handle .pdf")
	}
	if e.CanExtract("book.txt") {
		t.Error("should not handle .txt")
	}
}

func TestRegistryFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	rc, err := r.Extract(path)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
}

func TestIsTextFile(t *testing.T) {
	dir := t.TempDir()

	// Text file.
	textPath := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(textPath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsTextFile(textPath) {
		t.Error("should detect as text")
	}

	// Binary file.
	binPath := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if IsTextFile(binPath) {
		t.Error("should detect as binary")
	}
}
