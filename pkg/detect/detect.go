// Package detect provides a Cayley tree-based text detection chain.
// It loads a trained snapshot, thaws it into a read-only virtual tree,
// and probes text against the 8-level Walsh-Hadamard structure.
//
// Thread-safe: Detect can be called concurrently from multiple goroutines.
package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mlekudev/dendrite/pkg/cayley"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/grammar"
	"github.com/mlekudev/dendrite/pkg/hadamard"
	"github.com/mlekudev/dendrite/pkg/memory"
)

// Detector holds a thawed Cayley tree for detection.
// The tree is read-only after thaw; Detect is safe for concurrent use.
type Detector struct {
	tree      *cayley.Tree
	trollTree *cayley.Tree
	window    int
}

// NewDetector loads a trained Cayley tree snapshot from the badger DB,
// thaws it, and closes the DB. The returned Detector holds only the
// in-memory virtual tree.
func NewDetector(memoryDir string, window int) (*Detector, error) {
	db, err := memory.Open(memoryDir)
	if err != nil {
		return nil, fmt.Errorf("open memory: %w", err)
	}
	defer db.Close()

	data, err := db.LoadMindsicle(1)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}

	var snap cayley.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return &Detector{
		tree:   cayley.Thaw(&snap),
		window: window,
	}, nil
}

// NewDetectorFromSnapshot creates a Detector from an already-loaded snapshot.
func NewDetectorFromSnapshot(snap *cayley.Snapshot, window int) *Detector {
	return &Detector{
		tree:   cayley.Thaw(snap),
		window: window,
	}
}

// NewDetectorFromTree creates a Detector from a live tree (used in testing).
func NewDetectorFromTree(tree *cayley.Tree, window int) *Detector {
	return &Detector{
		tree:   tree,
		window: window,
	}
}

// LoadTrollTree loads a manipulation-trained Cayley tree.
func (d *Detector) LoadTrollTree(memoryDir string) error {
	db, err := memory.Open(memoryDir)
	if err != nil {
		return fmt.Errorf("open troll memory: %w", err)
	}
	defer db.Close()

	data, err := db.LoadMindsicle(1)
	if err != nil {
		return fmt.Errorf("load troll snapshot: %w", err)
	}

	var snap cayley.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("unmarshal troll snapshot: %w", err)
	}

	d.trollTree = cayley.Thaw(&snap)
	return nil
}

// Detect runs the 8-level Cayley tree detection on text content.
//
// Detection works by building a fresh "probe tree" from the input text,
// then comparing it level-by-level against the trained tree. The distance
// between the two trees' channel distributions at each level determines
// the walk distance and bond/miss metrics.
//
// Each depth level of the tree corresponds to one detection pass:
// leaf (depth 7) = pass 1, root (depth 0) = pass 8.
func (d *Detector) Detect(ctx context.Context, content string) grammar.Verdict {
	solution := enzyme.Text{}.Digest(strings.NewReader(content))

	// Build a probe tree from the input text.
	probeTree := cayley.NewTree()
	count := 0
	for tok := range solution {
		if d.window > 0 && count >= d.window {
			for range solution {
			}
			break
		}
		ch := hadamard.ChanIndex(tok.Type())
		if ch >= 0 {
			probeTree.Deposit(ch)
		}
		count++
	}

	select {
	case <-ctx.Done():
		return grammar.Verdict{Label: "cancelled"}
	default:
	}

	// Compare probe tree against trained tree at each depth level.
	// Only include levels where the probe tree has at least 32 tokens,
	// since sparse levels produce noisy Walsh comparisons.
	const minTokensPerLevel = 32
	perLevel := make([]grammar.PassStats, cayley.MaxDepth)
	usedLevels := 0
	for depth := cayley.MaxDepth - 1; depth >= 0; depth-- {
		// Check probe token count at this level.
		probeTokens := probeTree.TokenCountAt(depth)
		if probeTokens < minTokensPerLevel {
			continue
		}

		bonded, missed, longMisses, walkDist := cayley.CompareAt(d.tree, probeTree, depth)
		passIdx := cayley.MaxDepth - 1 - depth
		perLevel[passIdx].Bonded = int64(bonded)
		perLevel[passIdx].Missed = int64(missed)
		perLevel[passIdx].Total = int64(bonded + missed)
		perLevel[passIdx].LongMisses = int64(longMisses)
		perLevel[passIdx].TotalWalkDist = walkDist
		usedLevels++
	}

	// Compute probe features from the leaf-level spatial distribution.
	leafStats := probeTree.StatsAt(cayley.MaxDepth - 1)
	leafTotal := hadamard.DC(leafStats.Spatial)
	var features grammar.ProbeFeatures
	if leafTotal > 0 {
		features.PunctRatio = float64(leafStats.Spatial[hadamard.ChanPunct]) / float64(leafTotal)
		features.LongWordRatio = float64(leafStats.Spatial[hadamard.ChanW4]+leafStats.Spatial[hadamard.ChanW5]) / float64(leafTotal)
	}

	verdict := grammar.ScoreWalsh(perLevel, features)

	// Troll scoring if available.
	if d.trollTree != nil {
		trollLevels := make([]grammar.PassStats, cayley.MaxDepth)
		for depth := cayley.MaxDepth - 1; depth >= 0; depth-- {
			bonded, missed, _, _ := cayley.CompareAt(d.trollTree, probeTree, depth)
			passIdx := cayley.MaxDepth - 1 - depth
			trollLevels[passIdx].Bonded = int64(bonded)
			trollLevels[passIdx].Missed = int64(missed)
			trollLevels[passIdx].Total = int64(bonded + missed)
		}
		grammar.ScoreTroll(&verdict, trollLevels)
	}

	return verdict
}

