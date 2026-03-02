package grow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

type tagConstraint struct{ tag string }

func (c tagConstraint) Tag() string              { return c.tag }
func (c tagConstraint) Admits(e axiom.Element) bool { return e.Type() == c.tag }

func TestGrowthBasic(t *testing.T) {
	l := lattice.New()

	// Create a small lattice with word sites.
	nodes := make([]*lattice.Node, 10)
	for i := range nodes {
		nodes[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	// Connect in a ring.
	for i := range nodes {
		l.Connect(nodes[i], nodes[(i+1)%len(nodes)])
	}

	// Feed three elements.
	solution := make(chan axiom.Element, 3)
	solution <- enzyme.Elem("word", "hello")
	solution <- enzyme.Elem("word", "world")
	solution <- enzyme.Elem("word", "test")
	close(solution)

	events := make(chan Event, 10)

	ctx := context.Background()
	cfg := Config{MaxSteps: 500, Workers: 2}

	Run(ctx, l, solution, cfg, events)
	close(events)

	bonded := 0
	for ev := range events {
		if ev.Type == EventBonded {
			bonded++
		}
	}

	if bonded != 3 {
		t.Errorf("expected 3 bonds, got %d", bonded)
	}

	// Verify lattice state.
	occupied := 0
	for _, n := range l.Nodes() {
		if n.Occupied() {
			occupied++
		}
	}
	if occupied != 3 {
		t.Errorf("expected 3 occupied nodes, got %d", occupied)
	}
}

func TestGrowthTypeRejection(t *testing.T) {
	l := lattice.New()

	// Only word sites.
	nodes := make([]*lattice.Node, 5)
	for i := range nodes {
		nodes[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	for i := range nodes {
		l.Connect(nodes[i], nodes[(i+1)%len(nodes)])
	}

	// Feed a number element — wrong type.
	solution := make(chan axiom.Element, 1)
	solution <- enzyme.Elem("number", "42")
	close(solution)

	events := make(chan Event, 5)
	ctx := context.Background()
	cfg := Config{MaxSteps: 100, Workers: 1}

	Run(ctx, l, solution, cfg, events)
	close(events)

	for ev := range events {
		if ev.Type == EventBonded {
			t.Fatal("number should not bond at word site")
		}
	}
}

func TestGrowthCancellation(t *testing.T) {
	l := lattice.New()
	n := l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	_ = n

	// Endless solution.
	solution := make(chan axiom.Element)
	events := make(chan Event, 100)

	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{MaxSteps: 100, Workers: 1}

	done := make(chan struct{})
	go func() {
		Run(ctx, l, solution, cfg, events)
		close(done)
	}()

	// Cancel immediately.
	cancel()
	<-done // should return promptly
}

func TestGrowthSaturation(t *testing.T) {
	l := lattice.New()

	// 3 sites.
	nodes := make([]*lattice.Node, 3)
	for i := range nodes {
		nodes[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	for i := range nodes {
		l.Connect(nodes[i], nodes[(i+1)%len(nodes)])
	}

	// Feed 5 elements — only 3 can bond.
	solution := make(chan axiom.Element, 5)
	for i := range 5 {
		solution <- enzyme.Elem("word", string(rune('a'+i)))
	}
	close(solution)

	events := make(chan Event, 10)
	ctx := context.Background()
	cfg := Config{MaxSteps: 500, Workers: 2}

	Run(ctx, l, solution, cfg, events)
	close(events)

	bonded := 0
	expired := 0
	for ev := range events {
		switch ev.Type {
		case EventBonded:
			bonded++
		case EventExpired:
			expired++
		}
	}

	if bonded != 3 {
		t.Errorf("expected 3 bonds, got %d", bonded)
	}
	if expired != 2 {
		t.Errorf("expected 2 expired, got %d", expired)
	}
}

func TestBlockFreezeThawRoundTrip(t *testing.T) {
	l := lattice.New()

	// Create 8 nodes with "word" constraints, connect in ring.
	nodes := make([]*lattice.Node, 8)
	for i := range nodes {
		nodes[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	for i := range nodes {
		l.Connect(nodes[i], nodes[(i+1)%len(nodes)])
	}

	// Bond some elements.
	nodes[0].Bond(enzyme.Elem("word", "alpha"))
	nodes[3].Bond(enzyme.Elem("word", "beta"))
	nodes[5].Bond(enzyme.Elem("word", "gamma"))

	// Build a block covering all nodes.
	bm := buildBlockMap(l, 8)
	if len(bm.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(bm.Blocks))
	}
	b := bm.Blocks[0]

	// Snapshot pre-freeze state.
	type nodeState struct {
		occupied  bool
		bondCount int
		perm      uint8
		age       uint8
	}
	pre := make([]nodeState, len(b.Nodes))
	for i, n := range b.Nodes {
		pre[i] = nodeState{
			occupied:  n.Occupied(),
			bondCount: n.BondCount(),
			perm:      n.Permutation(),
			age:       n.Age(),
		}
	}

	// Freeze to temp dir.
	dir := t.TempDir()
	if err := freezeBlock(b, dir); err != nil {
		t.Fatalf("freezeBlock: %v", err)
	}

	// Verify file exists.
	path := blockFilePath(dir, b.ID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("block file not found: %v", err)
	}

	// Strip nodes.
	for _, n := range b.Nodes {
		n.StripForEvictionUnsafe()
	}

	// Verify stripped: all nodes should report unoccupied.
	for i, n := range b.Nodes {
		if n.Occupied() {
			t.Errorf("node %d still occupied after strip", i)
		}
	}

	// Thaw.
	cf := func(tag string) axiom.Constraint { return tagConstraint{tag} }
	if err := thawBlock(b, dir, cf); err != nil {
		t.Fatalf("thawBlock: %v", err)
	}

	// Verify state matches pre-freeze.
	for i, n := range b.Nodes {
		got := nodeState{
			occupied:  n.Occupied(),
			bondCount: n.BondCount(),
			perm:      n.Permutation(),
			age:       n.Age(),
		}
		if got.occupied != pre[i].occupied {
			t.Errorf("node %d: occupied mismatch: got %v, want %v", i, got.occupied, pre[i].occupied)
		}
		if got.bondCount != pre[i].bondCount {
			t.Errorf("node %d: bondCount mismatch: got %d, want %d", i, got.bondCount, pre[i].bondCount)
		}
	}

	// Verify occupant values.
	occ0 := b.Nodes[0].Occupant()
	if occ0 == nil || occ0.Value() != "alpha" {
		t.Errorf("node 0: expected occupant value 'alpha', got %v", occ0)
	}
	occ3 := b.Nodes[3].Occupant()
	if occ3 == nil || occ3.Value() != "beta" {
		t.Errorf("node 3: expected occupant value 'beta', got %v", occ3)
	}
}

func TestPagedGrowth(t *testing.T) {
	l := lattice.New()

	// Create a lattice large enough for multiple blocks.
	// 4 blocks of 4 nodes = 16 nodes total.
	numNodes := 16
	nodes := make([]*lattice.Node, numNodes)
	for i := range nodes {
		nodes[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	// Connect in ring + cross-links for better connectivity.
	for i := range nodes {
		l.Connect(nodes[i], nodes[(i+1)%numNodes])
		if i+4 < numNodes {
			l.Connect(nodes[i], nodes[i+4]) // cross-block bridge
		}
	}

	// Feed 6 elements — should all find homes in 16 sites.
	numElems := 6
	solution := make(chan axiom.Element, numElems)
	for i := range numElems {
		solution <- enzyme.Elem("word", string(rune('a'+i)))
	}
	close(solution)

	dir := t.TempDir()
	blockDir := filepath.Join(dir, "blocks")
	os.MkdirAll(blockDir, 0o755)

	events := make(chan Event, 20)
	ctx := context.Background()
	cf := func(tag string) axiom.Constraint { return tagConstraint{tag} }

	cfg := Config{
		MaxSteps:          500,
		Workers:           2,
		BlockSize:         4,  // 4 blocks of 4 nodes
		MaxRounds:         50,
		MaxResidentBlocks: 2, // only 2 of 4 blocks resident at a time
		BlockDir:          blockDir,
		ConstraintFactory: cf,
	}

	RunBlocked(ctx, l, solution, cfg, events)
	close(events)

	bonded := 0
	expired := 0
	rejected := 0
	for ev := range events {
		switch ev.Type {
		case EventBonded:
			bonded++
		case EventExpired:
			expired++
		case EventRejected:
			rejected++
		}
	}

	t.Logf("paged growth: bonded=%d expired=%d rejected=%d", bonded, expired, rejected)

	if bonded != numElems {
		t.Errorf("paged growth: expected %d bonds, got %d", numElems, bonded)
	}

	// Verify lattice state.
	occupied := 0
	for _, n := range l.Nodes() {
		if n.Occupied() {
			occupied++
		}
	}
	if occupied != bonded {
		t.Errorf("paged growth: occupied=%d should match bonded=%d", occupied, bonded)
	}
}
