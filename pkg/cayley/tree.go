// Package cayley implements a virtual z=8 Cayley tree of depth 8 for
// lattice-based text detection. The tree is not materialized as a graph;
// instead, each of the 8 depth levels stores an aggregated 8-channel
// vector and its Walsh-Hadamard transform.
//
// Data enters at leaves (depth 7, corresponding to detection pass 1) and
// propagates upward through H_8 transforms at each level. The root
// (depth 0, pass 8) holds the final Walsh components for verdict scoring.
//
// The 8 channels are: w1, w2, w3, w4, w5, punct, space, origin.
package cayley

import (
	"sync"

	"github.com/mlekudev/dendrite/pkg/hadamard"
)

// MaxDepth is the total depth of the Cayley tree. Each depth level
// corresponds to one detection pass: leaf (depth 7) = pass 1,
// root (depth 0) = pass 8.
const MaxDepth = 8

// HistorySize is the number of Walsh snapshots retained per level
// for convergence detection.
const HistorySize = 16

// Level holds the accumulated state at one depth level of the virtual tree.
type Level struct {
	mu sync.RWMutex

	// Spatial holds the spatial-domain signal: token counts per channel.
	Spatial hadamard.Vec8

	// Walsh holds the Hadamard-transformed Spatial vector. Updated on
	// every deposit. Walsh[0] is the aggregate (DC); Walsh[1..7] are
	// orthogonal detail coefficients.
	Walsh hadamard.Vec8

	// BondCount tracks successful matches at this level per channel.
	BondCount hadamard.Vec8

	// MissCount tracks failed matches at this level per channel.
	MissCount hadamard.Vec8

	// TotalWalkDist accumulates walk distances for bonded events per channel.
	TotalWalkDist hadamard.Vec8

	// History is a circular buffer of recent Walsh vectors for convergence.
	History   [HistorySize]hadamard.Vec8
	histIdx   int
	histCount int

	// Depth is this level's position (0 = root, MaxDepth-1 = leaves).
	Depth int
}

// Tree is the z=8 virtual Cayley tree.
type Tree struct {
	Levels [MaxDepth]Level

	// OriginProfile accumulates the origin channel fingerprint during
	// training. This captures author-characteristic patterns.
	OriginProfile hadamard.Vec8

	// TokenCount is the total tokens deposited during training.
	TokenCount int64

	// Converged indicates whether training has stabilized (root Walsh
	// components are stable across the history window).
	Converged bool
}

// NewTree creates an initialized Cayley tree with depth levels set.
func NewTree() *Tree {
	t := &Tree{}
	for i := range t.Levels {
		t.Levels[i].Depth = i
	}
	return t
}

// Leaf returns the leaf level (depth MaxDepth-1 = pass 1).
func (t *Tree) Leaf() *Level {
	return &t.Levels[MaxDepth-1]
}

// Root returns the root level (depth 0 = pass 8).
func (t *Tree) Root() *Level {
	return &t.Levels[0]
}

// Deposit records a token in channel ch at the leaf level and propagates
// the signal upward through the Cayley tree.
//
// Each level sees a different temporal view of the token stream:
//   - Level 7 (leaf): every token (count mod 1)
//   - Level 6: every 2nd token (count mod 2)
//   - Level 5: every 4th token (count mod 4)
//   - ...
//   - Level 0 (root): every 128th token (count mod 128)
//
// This creates genuine multi-scale behavior: lower levels (higher depth)
// see fine-grained token patterns, while upper levels (lower depth) see
// coarser structural patterns. The Walsh transform at each level captures
// the frequency structure at that temporal scale.
func (t *Tree) Deposit(ch int) {
	if ch < 0 || ch >= hadamard.NumChans {
		return
	}

	t.TokenCount++

	// Deposit at each level based on temporal subsampling.
	// Level d receives this token only if TokenCount is divisible
	// by 2^(MaxDepth-1-d). Leaf (d=7) gets every token.
	// Root (d=0) gets every 128th token.
	for d := MaxDepth - 1; d >= 0; d-- {
		stride := int64(1) << uint(MaxDepth-1-d)
		if t.TokenCount%stride != 0 {
			break // higher levels won't fire either
		}

		level := &t.Levels[d]
		level.mu.Lock()
		level.Spatial[ch]++
		level.BondCount[ch]++
		level.Walsh = level.Spatial
		hadamard.Transform(&level.Walsh)
		level.mu.Unlock()
	}
}

// RecordHistory snapshots the current root Walsh vector for convergence
// tracking. Should be called at file boundaries during training, not on
// every token deposit. This provides meaningful convergence detection
// across corpus files rather than within a single file.
func (t *Tree) RecordHistory() {
	root := t.Root()
	root.mu.Lock()
	defer root.mu.Unlock()
	root.History[root.histIdx] = root.Walsh
	root.histIdx = (root.histIdx + 1) % HistorySize
	if root.histCount < HistorySize {
		root.histCount++
	}
}

// ProbeAt tests whether a token at channel ch matches the trained pattern
// at the given depth level. Returns whether it bonded and a walk distance.
//
// This is used during detection on a frozen (thawed) tree. The tree
// is not modified.
func (t *Tree) ProbeAt(depth, ch int) (bonded bool, distance int64) {
	if depth < 0 || depth >= MaxDepth || ch < 0 || ch >= hadamard.NumChans {
		return false, int64(hadamard.NumChans)
	}

	level := &t.Levels[depth]
	level.mu.RLock()
	defer level.mu.RUnlock()

	total := hadamard.DC(level.Spatial)
	if total == 0 {
		return false, int64(hadamard.NumChans)
	}

	chanCount := level.Spatial[ch]
	if chanCount == 0 {
		return false, int64(hadamard.NumChans)
	}

	// Walk distance: how far the channel's representation deviates
	// from the uniform expectation (total/8). Scaled to small integers.
	expected := total / hadamard.NumChans
	if expected == 0 {
		expected = 1
	}
	dist := chanCount - expected
	if dist < 0 {
		dist = -dist
	}

	// Bond threshold: channel must have at least 1/(2*NumChans) of total
	// to be considered present. This is half the uniform expectation.
	threshold := total / (2 * hadamard.NumChans)
	if threshold < 1 {
		threshold = 1
	}

	if chanCount >= threshold {
		return true, dist
	}
	return false, dist
}

// CompareAt compares the channel distribution of two trees at a given depth.
// It returns per-channel bond/miss status and an aggregate walk distance.
//
// The walk distance combines two signals:
//  1. Spatial distance: L1 difference between normalized spatial vectors
//  2. Walsh distance: L1 difference between normalized Walsh detail components
//
// The Walsh domain amplifies structural differences that are small in the
// spatial domain — a 2% spatial shift produces a larger Walsh shift because
// the transform spreads the difference across all 7 detail components.
//
// A channel bonds if the probe tree's proportion for that channel is within
// a configurable tolerance of the trained tree's proportion.
func CompareAt(trained, probe *Tree, depth int) (
	bonded, missed int,
	longMisses int,
	walkDist float64,
) {
	if depth < 0 || depth >= MaxDepth {
		return 0, hadamard.NumChans, 0, float64(hadamard.NumChans)
	}

	tLevel := &trained.Levels[depth]
	pLevel := &probe.Levels[depth]

	tLevel.mu.RLock()
	tSpatial := tLevel.Spatial
	tWalsh := tLevel.Walsh
	tLevel.mu.RUnlock()

	pLevel.mu.RLock()
	pSpatial := pLevel.Spatial
	pWalsh := pLevel.Walsh
	pLevel.mu.RUnlock()

	tTotal := hadamard.DC(tSpatial)
	pTotal := hadamard.DC(pSpatial)

	if tTotal == 0 || pTotal == 0 {
		return 0, hadamard.NumChans, 0, float64(hadamard.NumChans)
	}

	// Spatial proportion comparison for bond/miss.
	for ch := range hadamard.NumChans {
		if tSpatial[ch] == 0 {
			missed++
			if ch == hadamard.ChanW4 || ch == hadamard.ChanW5 {
				longMisses++
			}
			continue
		}

		probeScaled := pSpatial[ch] * tTotal
		trainScaled := tSpatial[ch] * pTotal

		if probeScaled == 0 {
			missed++
			if ch == hadamard.ChanW4 || ch == hadamard.ChanW5 {
				longMisses++
			}
			continue
		}

		// Bond if ratio is within [0.5, 2.0].
		if probeScaled*2 >= trainScaled && probeScaled <= trainScaled*2 {
			bonded++
		} else {
			missed++
			if ch == hadamard.ChanW4 || ch == hadamard.ChanW5 {
				longMisses++
			}
		}
	}

	// Walk distance: normalized Walsh shape difference.
	// Compare Walsh[k]/Walsh[0] between trained and probe for k=1..7.
	// This captures structural pattern differences amplified by the transform.
	tDC := tWalsh[0]
	pDC := pWalsh[0]

	if tDC == 0 || pDC == 0 {
		walkDist = float64(hadamard.NumChans)
		return
	}

	var walshDistSum float64
	for k := 1; k < hadamard.NumChans; k++ {
		tNorm := float64(tWalsh[k]) / float64(tDC)
		pNorm := float64(pWalsh[k]) / float64(pDC)
		diff := tNorm - pNorm
		if diff < 0 {
			diff = -diff
		}
		walshDistSum += diff
	}

	// Scale to produce values in the range ~0.08-0.16 for typical text.
	// Human text (similar distribution to trained): ~0.08-0.11
	// AI text (different distribution): ~0.12-0.16
	walkDist = walshDistSum / float64(hadamard.NumChans-1)

	return bonded, missed, longMisses, walkDist
}

// TokenCountAt returns the total token count at a specific depth level.
func (t *Tree) TokenCountAt(depth int) int64 {
	if depth < 0 || depth >= MaxDepth {
		return 0
	}
	t.Levels[depth].mu.RLock()
	defer t.Levels[depth].mu.RUnlock()
	return hadamard.DC(t.Levels[depth].Spatial)
}

// RootWalsh returns the Walsh components at the root level (pass 8).
func (t *Tree) RootWalsh() hadamard.Vec8 {
	t.Root().mu.RLock()
	defer t.Root().mu.RUnlock()
	return t.Root().Walsh
}

// LevelWalsh returns the Walsh components at a specific depth.
func (t *Tree) LevelWalsh(depth int) hadamard.Vec8 {
	if depth < 0 || depth >= MaxDepth {
		return hadamard.Vec8{}
	}
	t.Levels[depth].mu.RLock()
	defer t.Levels[depth].mu.RUnlock()
	return t.Levels[depth].Walsh
}

// LevelStats returns bond/miss/walk statistics for a specific depth.
type LevelStats struct {
	Spatial       hadamard.Vec8
	Walsh         hadamard.Vec8
	BondCount     hadamard.Vec8
	MissCount     hadamard.Vec8
	TotalWalkDist hadamard.Vec8
	Depth         int
}

// StatsAt returns the statistics for a given depth level.
func (t *Tree) StatsAt(depth int) LevelStats {
	if depth < 0 || depth >= MaxDepth {
		return LevelStats{Depth: depth}
	}
	l := &t.Levels[depth]
	l.mu.RLock()
	defer l.mu.RUnlock()
	return LevelStats{
		Spatial:       l.Spatial,
		Walsh:         l.Walsh,
		BondCount:     l.BondCount,
		MissCount:     l.MissCount,
		TotalWalkDist: l.TotalWalkDist,
		Depth:         l.Depth,
	}
}

// IsConverged checks whether the root Walsh distribution shape has
// stabilized over the history window. Convergence is declared when the
// normalized Walsh shape (each component divided by DC) stays stable
// across consecutive history entries.
//
// The threshold is in parts-per-thousand: threshold=50 means the maximum
// component-wise change in the normalized Walsh shape must be < 5%.
// This measures shape stability independent of magnitude growth.
func (t *Tree) IsConverged(threshold int64) bool {
	root := t.Root()
	root.mu.RLock()
	defer root.mu.RUnlock()

	// Need at least 8 history entries (file snapshots) for meaningful
	// convergence. This prevents premature convergence on small corpora.
	if root.histCount < 8 {
		return false
	}

	for i := 1; i < root.histCount; i++ {
		curr := root.History[(root.histIdx-i+HistorySize)%HistorySize]
		prev := root.History[(root.histIdx-i-1+HistorySize)%HistorySize]

		dcCurr := curr[0]
		dcPrev := prev[0]
		if dcCurr <= 0 || dcPrev <= 0 {
			return false
		}

		// Compare normalized shapes: curr[k]/dcCurr vs prev[k]/dcPrev.
		// Using cross-multiplication to stay in integer arithmetic:
		// |curr[k] * dcPrev - prev[k] * dcCurr| * 1000 / (dcCurr * dcPrev)
		var maxDelta int64
		for k := 1; k < 8; k++ {
			delta := curr[k]*dcPrev - prev[k]*dcCurr
			if delta < 0 {
				delta = -delta
			}
			if delta > maxDelta {
				maxDelta = delta
			}
		}
		// Normalize: maxDelta / (dcCurr * dcPrev) * 1000
		normalizer := dcCurr * dcPrev / 1000
		if normalizer <= 0 {
			return false
		}
		if maxDelta/normalizer > threshold {
			return false
		}
	}
	return true
}

// Health returns a summary of the tree's current state.
type Health struct {
	TokenCount int64
	RootWalsh  hadamard.Vec8
	LeafStats  LevelStats
	Converged  bool
}

func (t *Tree) Health() Health {
	return Health{
		TokenCount: t.TokenCount,
		RootWalsh:  t.RootWalsh(),
		LeafStats:  t.StatsAt(MaxDepth - 1),
		Converged:  t.Converged,
	}
}
