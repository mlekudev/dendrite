package cayley

import (
	"bytes"
	"testing"

	"github.com/mlekudev/dendrite/pkg/hadamard"
)

func TestNewTree(t *testing.T) {
	tree := NewTree()
	if tree == nil {
		t.Fatal("NewTree returned nil")
	}
	for i := range tree.Levels {
		if tree.Levels[i].Depth != i {
			t.Errorf("level %d: depth = %d, want %d", i, tree.Levels[i].Depth, i)
		}
	}
	if tree.TokenCount != 0 {
		t.Errorf("TokenCount = %d, want 0", tree.TokenCount)
	}
}

func TestDepositMultiScale(t *testing.T) {
	tree := NewTree()

	// Deposit 128 tokens into channel w2 — exactly enough for root to
	// receive 1 token (stride at root = 2^7 = 128).
	for range 128 {
		tree.Deposit(hadamard.ChanW2)
	}

	if tree.TokenCount != 128 {
		t.Errorf("TokenCount = %d, want 128", tree.TokenCount)
	}

	// Leaf (d=7, stride=1): all 128 tokens.
	leaf := tree.Leaf()
	if leaf.Spatial[hadamard.ChanW2] != 128 {
		t.Errorf("leaf w2 = %d, want 128", leaf.Spatial[hadamard.ChanW2])
	}

	// Level 6 (stride=2): 64 tokens.
	if tree.Levels[6].Spatial[hadamard.ChanW2] != 64 {
		t.Errorf("level 6 w2 = %d, want 64", tree.Levels[6].Spatial[hadamard.ChanW2])
	}

	// Level 0 (root, stride=128): 1 token.
	root := tree.Root()
	if root.Spatial[hadamard.ChanW2] != 1 {
		t.Errorf("root w2 = %d, want 1", root.Spatial[hadamard.ChanW2])
	}

	// Walsh[0] at root = DC = total tokens at that level.
	if root.Walsh[0] != 1 {
		t.Errorf("root Walsh[0] = %d, want 1", root.Walsh[0])
	}

	// Each level should have half the tokens of the level below it.
	for d := MaxDepth - 2; d >= 0; d-- {
		below := tree.Levels[d+1].Spatial[hadamard.ChanW2]
		this := tree.Levels[d].Spatial[hadamard.ChanW2]
		expected := below / 2
		if this != expected {
			t.Errorf("level %d: w2 = %d, want %d (half of level %d's %d)",
				d, this, expected, d+1, below)
		}
	}
}

func TestDepositMultipleChannels(t *testing.T) {
	tree := NewTree()

	// Deposit enough tokens for all levels to have data.
	// 256 each = leaf gets 256, root gets 2.
	for range 256 {
		tree.Deposit(hadamard.ChanW2) // 256
	}
	for range 256 {
		tree.Deposit(hadamard.ChanW3) // 256
	}
	for range 128 {
		tree.Deposit(hadamard.ChanPunct) // 128
	}

	if tree.TokenCount != 640 {
		t.Errorf("TokenCount = %d, want 640", tree.TokenCount)
	}

	leaf := tree.Leaf()
	if leaf.Spatial[hadamard.ChanW2] != 256 {
		t.Errorf("leaf w2 = %d, want 256", leaf.Spatial[hadamard.ChanW2])
	}
	if leaf.Spatial[hadamard.ChanW3] != 256 {
		t.Errorf("leaf w3 = %d, want 256", leaf.Spatial[hadamard.ChanW3])
	}
	if leaf.Spatial[hadamard.ChanPunct] != 128 {
		t.Errorf("leaf punct = %d, want 128", leaf.Spatial[hadamard.ChanPunct])
	}

	// DC at leaf should be total (256+256+128 = 640).
	if leaf.Walsh[0] != 640 {
		t.Errorf("leaf DC = %d, want 640", leaf.Walsh[0])
	}
}

func TestProbeAtBonds(t *testing.T) {
	tree := NewTree()

	// Build a trained tree with varied channel distribution.
	// Need enough tokens for leaf level to have meaningful counts.
	for range 200 {
		tree.Deposit(hadamard.ChanW2)
	}
	for range 160 {
		tree.Deposit(hadamard.ChanW3)
	}
	for range 100 {
		tree.Deposit(hadamard.ChanSpace)
	}

	// Probe for w2 at leaf — should bond (well represented).
	bonded, dist := tree.ProbeAt(MaxDepth-1, hadamard.ChanW2)
	if !bonded {
		t.Error("w2 at leaf should bond")
	}
	t.Logf("w2 leaf distance: %d", dist)

	// Probe for w1 at leaf — should not bond (never deposited).
	bonded, _ = tree.ProbeAt(MaxDepth-1, hadamard.ChanW1)
	if bonded {
		t.Error("w1 at leaf should not bond (never deposited)")
	}
}

func TestProbeAtRoot(t *testing.T) {
	tree := NewTree()

	// Build profile with enough tokens so root (stride 128) has data.
	// Deposit channels sequentially in large blocks to ensure all
	// channels get representation at the root level.
	// Each block of 128 tokens → 1 root deposit.
	channels := []int{
		hadamard.ChanW1, hadamard.ChanW2, hadamard.ChanW3,
		hadamard.ChanW4, hadamard.ChanPunct, hadamard.ChanSpace,
	}
	for _, ch := range channels {
		for range 128 {
			tree.Deposit(ch)
		}
	}

	// Log root state for debugging.
	root := tree.Root()
	t.Logf("root spatial: %v", root.Spatial)

	// All deposited channels should bond at root.
	for _, ch := range channels {
		bonded, _ := tree.ProbeAt(0, ch)
		if !bonded {
			t.Errorf("channel %s should bond at root (spatial[%d]=%d)",
				hadamard.ChanName[ch], ch, root.Spatial[ch])
		}
	}

	// Never-deposited channels should not bond.
	bonded, _ := tree.ProbeAt(0, hadamard.ChanW5)
	if bonded {
		t.Error("w5 should not bond at root (never deposited)")
	}
}

func TestIsConverged(t *testing.T) {
	tree := NewTree()

	// Not enough history yet.
	if tree.IsConverged(100) {
		t.Error("should not be converged with no data")
	}

	// Simulate training on 20 files with identical distribution.
	// Each "file" deposits 5000 tokens (realistic file size).
	for file := range 20 {
		for range 5000 {
			tree.Deposit(hadamard.ChanW2)
			tree.Deposit(hadamard.ChanW3)
			tree.Deposit(hadamard.ChanPunct)
		}
		tree.RecordHistory()
		_ = file
	}

	// With identical distributions across 20 files and 15000 tokens each,
	// relative Walsh change decreases across the history window.
	converged := tree.IsConverged(100) // 10% relative
	t.Logf("converged after 20 files (threshold 100): %v", converged)
	if !converged {
		t.Error("should converge with identical distributions across files")
	}

	// With fewer than 8 history entries, should not converge.
	tree2 := NewTree()
	for range 5 {
		for range 5000 {
			tree2.Deposit(hadamard.ChanW2)
		}
		tree2.RecordHistory()
	}
	if tree2.IsConverged(100) {
		t.Error("should not converge with only 5 history entries")
	}
}

func TestFreezeThawRoundTrip(t *testing.T) {
	tree := NewTree()
	for range 256 {
		tree.Deposit(hadamard.ChanW1)
		tree.Deposit(hadamard.ChanW2)
		tree.Deposit(hadamard.ChanW3)
	}
	tree.OriginProfile = hadamard.Vec8{1, 2, 3, 4, 5, 6, 7, 8}

	snap := Freeze(tree)
	thawed := Thaw(snap)

	// Verify all levels match.
	for d := range MaxDepth {
		origStats := tree.StatsAt(d)
		thawStats := thawed.StatsAt(d)
		if origStats.Spatial != thawStats.Spatial {
			t.Errorf("depth %d: spatial mismatch", d)
		}
		if origStats.Walsh != thawStats.Walsh {
			t.Errorf("depth %d: walsh mismatch", d)
		}
		if origStats.BondCount != thawStats.BondCount {
			t.Errorf("depth %d: bond count mismatch", d)
		}
		if origStats.MissCount != thawStats.MissCount {
			t.Errorf("depth %d: miss count mismatch", d)
		}
	}

	if thawed.OriginProfile != tree.OriginProfile {
		t.Error("origin profile mismatch")
	}
	if thawed.TokenCount != tree.TokenCount {
		t.Errorf("token count: %d != %d", thawed.TokenCount, tree.TokenCount)
	}
}

func TestSnapshotJSON(t *testing.T) {
	tree := NewTree()
	for range 256 {
		tree.Deposit(hadamard.ChanW2)
		tree.Deposit(hadamard.ChanSpace)
	}

	snap := Freeze(tree)

	var buf bytes.Buffer
	_, err := snap.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	loaded, err := ReadSnapshot(&buf)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}

	if loaded.TokenCount != snap.TokenCount {
		t.Errorf("token count: %d != %d", loaded.TokenCount, snap.TokenCount)
	}

	for d := range MaxDepth {
		if loaded.Levels[d].Spatial != snap.Levels[d].Spatial {
			t.Errorf("depth %d: spatial mismatch after JSON round-trip", d)
		}
		if loaded.Levels[d].Walsh != snap.Levels[d].Walsh {
			t.Errorf("depth %d: walsh mismatch after JSON round-trip", d)
		}
	}
}

func TestInvalidChannelIgnored(t *testing.T) {
	tree := NewTree()
	tree.Deposit(-1)
	tree.Deposit(99)
	if tree.TokenCount != 0 {
		t.Errorf("invalid deposits should be ignored, got %d tokens", tree.TokenCount)
	}
}

func TestHealth(t *testing.T) {
	tree := NewTree()
	for range 10 {
		tree.Deposit(hadamard.ChanW1)
	}
	h := tree.Health()
	if h.TokenCount != 10 {
		t.Errorf("health token count: %d, want 10", h.TokenCount)
	}
	if h.LeafStats.Spatial[hadamard.ChanW1] != 10 {
		t.Errorf("health leaf w1: %d, want 10", h.LeafStats.Spatial[hadamard.ChanW1])
	}
}

func TestMultiScaleDifferentiation(t *testing.T) {
	tree := NewTree()

	// Deposit 1024 tokens of a single channel to verify multi-scale counts.
	for range 1024 {
		tree.Deposit(hadamard.ChanW2)
	}

	// Verify each level has the expected token count: total/stride.
	for d := range MaxDepth {
		stats := tree.StatsAt(d)
		total := hadamard.DC(stats.Spatial)
		stride := int64(1) << uint(MaxDepth-1-d)
		expected := int64(1024) / stride
		if total != expected {
			t.Errorf("depth %d: total=%d, want %d (stride=%d)", d, total, expected, stride)
		}
		t.Logf("depth %d (stride %3d): total=%4d w2=%4d",
			d, stride, total, stats.Spatial[hadamard.ChanW2])
	}

	// With multi-channel deposits in sequential blocks, each level
	// should have genuinely different channel distributions.
	tree2 := NewTree()
	// Block 1: 256 tokens of w2
	for range 256 {
		tree2.Deposit(hadamard.ChanW2)
	}
	// Block 2: 256 tokens of w3
	for range 256 {
		tree2.Deposit(hadamard.ChanW3)
	}

	leaf := tree2.StatsAt(MaxDepth - 1)
	mid := tree2.StatsAt(MaxDepth / 2)
	t.Logf("leaf spatial: w2=%d w3=%d", leaf.Spatial[hadamard.ChanW2], leaf.Spatial[hadamard.ChanW3])
	t.Logf("mid  spatial: w2=%d w3=%d", mid.Spatial[hadamard.ChanW2], mid.Spatial[hadamard.ChanW3])

	// At the leaf, both channels should have their full counts.
	if leaf.Spatial[hadamard.ChanW2] != 256 {
		t.Errorf("leaf w2 = %d, want 256", leaf.Spatial[hadamard.ChanW2])
	}
	if leaf.Spatial[hadamard.ChanW3] != 256 {
		t.Errorf("leaf w3 = %d, want 256", leaf.Spatial[hadamard.ChanW3])
	}

	// At the mid level (stride ~16), the distribution should still reflect
	// both channels but with different proportions due to temporal sampling.
	midTotal := hadamard.DC(mid.Spatial)
	if midTotal == 0 {
		t.Error("mid level should have some tokens")
	}
}
