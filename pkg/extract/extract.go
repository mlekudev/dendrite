// Package extract provides text extraction from arbitrary file formats.
// It shells out to external tools (pandoc, pdftotext) for complex formats
// and reads plain text directly. Missing tools cause graceful fallback
// to raw text or skip.
package extract

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Extractor can extract plain text from a file.
type Extractor interface {
	// CanExtract reports whether this extractor handles the given path.
	CanExtract(path string) bool

	// Extract returns a reader of plain text content from the file.
	// The caller must close the returned ReadCloser.
	Extract(path string) (io.ReadCloser, error)
}

// Registry holds extractors in priority order.
type Registry struct {
	extractors []Extractor
}

// NewRegistry creates a registry with the standard set of extractors.
func NewRegistry() *Registry {
	return &Registry{
		extractors: []Extractor{
			PlainText{},
			PDFExtractor{},
			PandocExtractor{},
		},
	}
}

// Extract finds the first matching extractor and returns the text content.
func (r *Registry) Extract(path string) (io.ReadCloser, error) {
	for _, e := range r.extractors {
		if e.CanExtract(path) {
			return e.Extract(path)
		}
	}
	return nil, fmt.Errorf("extract: no extractor for %s", path)
}

// PlainText reads .txt and .md files directly.
type PlainText struct{}

func (PlainText) CanExtract(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".txt" || ext == ".md" || ext == ".text" || ext == ""
}

func (PlainText) Extract(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// PDFExtractor uses pdftotext to extract text from PDFs.
type PDFExtractor struct{}

func (PDFExtractor) CanExtract(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

func (PDFExtractor) Extract(path string) (io.ReadCloser, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return nil, fmt.Errorf("extract: pdftotext not found: %w", err)
	}
	cmd := exec.Command("pdftotext", "-enc", "UTF-8", path, "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

// PandocExtractor uses pandoc to convert various formats to plain text.
type PandocExtractor struct{}

var pandocExts = map[string]bool{
	".html":  true,
	".htm":   true,
	".docx":  true,
	".epub":  true,
	".rtf":   true,
	".odt":   true,
	".rst":   true,
	".latex": true,
	".tex":   true,
	".org":   true,
}

func (PandocExtractor) CanExtract(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return pandocExts[ext]
}

func (PandocExtractor) Extract(path string) (io.ReadCloser, error) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		return nil, fmt.Errorf("extract: pandoc not found: %w", err)
	}
	cmd := exec.Command("pandoc", "-t", "plain", "--wrap=none", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cmdReadCloser{ReadCloser: stdout, cmd: cmd}, nil
}

// cmdReadCloser wraps a command's stdout pipe and waits for the command
// on Close.
type cmdReadCloser struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (c *cmdReadCloser) Close() error {
	err := c.ReadCloser.Close()
	if werr := c.cmd.Wait(); werr != nil && err == nil {
		err = werr
	}
	return err
}

// IsTextFile does a quick heuristic check: reads the first 512 bytes
// and checks if they look like text (no null bytes, mostly printable).
func IsTextFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}
	buf = buf[:n]
	for _, b := range buf {
		if b == 0 {
			return false
		}
	}
	return true
}
