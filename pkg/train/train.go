// Package train implements the training loop for the Cayley tree detector.
// It tokenizes text input and deposits tokens into the virtual z=8 Cayley
// tree, building the word-length distribution profile that characterizes
// human text.
package train

import (
	"context"
	"io"

	"github.com/mlekudev/dendrite/pkg/cayley"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/hadamard"
)

// Config controls training parameters.
type Config struct {
	// ConvergenceThreshold is the L1 norm threshold for root Walsh
	// stability. Training is converged when consecutive root Walsh
	// vectors differ by less than this.
	ConvergenceThreshold int64

	// AuthorID is the identity string for the origin channel.
	// Empty string means the origin channel is not populated.
	AuthorID string

	// TokenWindow caps tokens per file. 0 = unlimited.
	TokenWindow int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ConvergenceThreshold: 100,
		TokenWindow:          50000,
	}
}

// FromReader feeds text from r into the Cayley tree.
// Returns the number of tokens processed.
func FromReader(ctx context.Context, t *cayley.Tree, r io.Reader, cfg Config) (int64, error) {
	tokens := enzyme.Text{}.Digest(r)

	var count int64
	for tok := range tokens {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		default:
		}

		ch := hadamard.ChanIndex(tok.Type())
		if ch < 0 {
			continue
		}

		t.Deposit(ch)
		count++

		if cfg.TokenWindow > 0 && count >= int64(cfg.TokenWindow) {
			break
		}
	}

	// If author ID is provided, populate the origin channel.
	// The origin signal is proportional to the total tokens processed,
	// creating a fingerprint that scales with the training corpus.
	if cfg.AuthorID != "" {
		// Deposit origin tokens proportional to the text volume.
		// Use 3% of token count (matching the grammar allocation).
		originCount := count * 3 / 100
		if originCount < 1 {
			originCount = 1
		}
		for range originCount {
			t.Deposit(hadamard.ChanOrigin)
		}
		// Also record in the tree's origin profile.
		t.OriginProfile[hadamard.ChanOrigin] += count
	}

	return count, nil
}

// IsConverged checks whether the tree has converged.
func IsConverged(t *cayley.Tree, cfg Config) bool {
	return t.IsConverged(cfg.ConvergenceThreshold)
}
