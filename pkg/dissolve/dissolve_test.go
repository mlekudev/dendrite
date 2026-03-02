package dissolve

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

type tagConstraint struct{ tag string }

func (c tagConstraint) Tag() string              { return c.tag }
func (c tagConstraint) Admits(e axiom.Element) bool { return e.Type() == c.tag }

func TestDissolveWeakBonds(t *testing.T) {
	l := lattice.New()

	// One constraint = lock-in of 1.0 (survives threshold 0.5).
	strong := l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	strong.Bond(enzyme.Elem("word", "strong"))

	// One constraint = lock-in of 1.0, but we'll test with high threshold.
	weak := l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	weak.Bond(enzyme.Elem("word", "weak"))

	dissolved := make(chan axiom.Element, 10)
	events := make(chan Event, 10)

	// Threshold 1.5 — both should dissolve since lock-in is 1.0.
	ScanOnce(l, Config{Threshold: ratio.New(3, 2)}, dissolved, events)

	close(dissolved)
	close(events)

	count := 0
	for range dissolved {
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 dissolved, got %d", count)
	}

	if strong.Occupied() {
		t.Error("strong should be dissolved at threshold 1.5")
	}
	if weak.Occupied() {
		t.Error("weak should be dissolved at threshold 1.5")
	}
}

func TestDissolveLeavesStrongBonds(t *testing.T) {
	l := lattice.New()

	// Two constraints = lock-in of 2.0.
	n := l.AddNode([]axiom.Constraint{
		tagConstraint{"word"},
		lengthConstraint{"word", 3},
	})
	n.Bond(enzyme.Elem("word", "hello"))

	// Add occupied neighbors so contextual lock-in stays high.
	// ContextualLockIn = base * (0.3 + 0.7 * neighborOccupancyRate).
	// Neighbors also need high enough lock-in to survive the scan themselves,
	// otherwise they dissolve first and n loses support.
	// Give neighbors 2 constraints each, and connect them to each other
	// so everyone has occupied neighbors.
	nb1 := l.AddNode([]axiom.Constraint{tagConstraint{"word"}, lengthConstraint{"word", 3}})
	nb1.Bond(enzyme.Elem("word", "peer1"))
	l.Connect(n, nb1)

	nb2 := l.AddNode([]axiom.Constraint{tagConstraint{"word"}, lengthConstraint{"word", 3}})
	nb2.Bond(enzyme.Elem("word", "peer2"))
	l.Connect(n, nb2)

	// Connect neighbors to each other for mutual support.
	l.Connect(nb1, nb2)

	dissolved := make(chan axiom.Element, 10)
	events := make(chan Event, 10)

	// Threshold 0.9 — contextual lock-in with all-occupied neighbors = 1.0.
	// All three nodes have full neighbor support → contextual lock-in = 1.0 > 0.9.
	ScanOnce(l, Config{Threshold: ratio.New(9, 10)}, dissolved, events)

	close(dissolved)
	close(events)

	if !n.Occupied() {
		t.Error("strongly bonded node should survive with neighbor support")
	}
}

func TestDissolveReturnsToSolution(t *testing.T) {
	l := lattice.New()
	n := l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	n.Bond(enzyme.Elem("word", "recycled"))

	dissolved := make(chan axiom.Element, 10)
	events := make(chan Event, 10)

	ScanOnce(l, Config{Threshold: ratio.FromInt(2)}, dissolved, events)

	close(dissolved)
	close(events)

	elem := <-dissolved
	if elem == nil {
		t.Fatal("expected dissolved element")
	}
	if elem.Value().(string) != "recycled" {
		t.Errorf("expected 'recycled', got %v", elem.Value())
	}
}

func TestDissolveEmptyLattice(t *testing.T) {
	l := lattice.New()
	dissolved := make(chan axiom.Element, 10)
	events := make(chan Event, 10)

	// Should not panic on empty lattice.
	ScanOnce(l, Config{Threshold: ratio.One}, dissolved, events)

	close(dissolved)
	close(events)

	count := 0
	for range dissolved {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 dissolved from empty lattice, got %d", count)
	}
}

// lengthConstraint admits elements with string values of at least minLen.
type lengthConstraint struct {
	tag    string
	minLen int
}

func (c lengthConstraint) Tag() string { return c.tag }
func (c lengthConstraint) Admits(e axiom.Element) bool {
	if e.Type() != c.tag {
		return false
	}
	s, ok := e.Value().(string)
	if !ok {
		return false
	}
	return len(s) >= c.minLen
}
