package mindsicle

import (
	"bytes"
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/spore"
)

type testConstraint struct{ tag string }

func (c testConstraint) Tag() string                { return c.tag }
func (c testConstraint) Admits(e axiom.Element) bool { return e.Type() == c.tag }

type testElement struct {
	typ string
	val string
}

func (e testElement) Type() string { return e.typ }
func (e testElement) Value() any   { return e.val }

func testConstraintFactory(tag string) axiom.Constraint {
	return testConstraint{tag}
}

func TestFreezeThawRoundTrip(t *testing.T) {
	// Build a small lattice: 3 nodes, 2 edges, 2 occupied.
	l := lattice.New()
	n0 := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n1 := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n2 := l.AddNode([]axiom.Constraint{testConstraint{"number"}})
	l.Connect(n0, n1)
	l.Connect(n1, n2)

	n0.Bond(testElement{"word", "hello"})
	n1.Bond(testElement{"word", "world"})
	n0.SetPermutation(2)
	n0.SetProjection(3, 5, 42)
	n0.SetEnergy(true)

	// Increment age on n0 twice.
	n0.IncrementAge()
	n0.IncrementAge()

	// Create spore.
	sp := spore.Extract(l)

	// Freeze.
	m := Freeze(l, sp)
	if len(m.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(m.Nodes))
	}
	if m.Version != Version {
		t.Fatalf("expected version %d, got %d", Version, m.Version)
	}

	// Serialize → deserialize.
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	m2, err := ReadMindsicle(&buf)
	if err != nil {
		t.Fatalf("ReadMindsicle: %v", err)
	}
	if len(m2.Nodes) != 3 {
		t.Fatalf("deserialized: expected 3 nodes, got %d", len(m2.Nodes))
	}

	// Thaw.
	l2 := m2.Thaw(testConstraintFactory)
	if l2.Size() != 3 {
		t.Fatalf("thawed lattice size: expected 3, got %d", l2.Size())
	}

	// Verify occupants.
	tn0 := l2.Node(0)
	if !tn0.Occupied() {
		t.Fatal("node 0 should be occupied")
	}
	if tn0.Occupant().Type() != "word" {
		t.Fatalf("node 0 type: expected 'word', got %q", tn0.Occupant().Type())
	}
	if tn0.Occupant().Value().(string) != "hello" {
		t.Fatalf("node 0 value: expected 'hello', got %v", tn0.Occupant().Value())
	}

	// Verify vacancy.
	tn2 := l2.Node(2)
	if tn2.Occupied() {
		t.Fatal("node 2 should be vacant")
	}

	// Verify connectivity: node 1 should have 2 neighbors.
	tn1 := l2.Node(1)
	if len(tn1.Neighbors()) != 2 {
		t.Fatalf("node 1 neighbors: expected 2, got %d", len(tn1.Neighbors()))
	}

	// Verify age survives round-trip.
	if tn0.Age() != 2 {
		t.Fatalf("node 0 age: expected 2, got %d", tn0.Age())
	}

	// Verify permutation.
	if tn0.Permutation() != 2 {
		t.Fatalf("node 0 perm: expected 2, got %d", tn0.Permutation())
	}

	// Verify projection.
	if tn0.ProjectionVertex() != 3 {
		t.Fatalf("node 0 proj vertex: expected 3, got %d", tn0.ProjectionVertex())
	}
	if tn0.ProjectionKey() != 5 {
		t.Fatalf("node 0 proj key: expected 5, got %d", tn0.ProjectionKey())
	}
	if tn0.ProjectionPath() != 42 {
		t.Fatalf("node 0 proj path: expected 42, got %d", tn0.ProjectionPath())
	}

	// Verify bond count.
	if tn0.BondCount() != 1 {
		t.Fatalf("node 0 bond count: expected 1, got %d", tn0.BondCount())
	}
}

func TestEmptyLattice(t *testing.T) {
	l := lattice.New()
	m := Freeze(l, nil)
	if len(m.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(m.Nodes))
	}

	var buf bytes.Buffer
	m.WriteTo(&buf)
	m2, err := ReadMindsicle(&buf)
	if err != nil {
		t.Fatalf("ReadMindsicle: %v", err)
	}

	l2 := m2.Thaw(testConstraintFactory)
	if l2.Size() != 0 {
		t.Fatalf("thawed empty lattice size: expected 0, got %d", l2.Size())
	}
}

func TestVacantNodes(t *testing.T) {
	l := lattice.New()
	l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	l.AddNode([]axiom.Constraint{testConstraint{"number"}})

	m := Freeze(l, nil)

	var buf bytes.Buffer
	m.WriteTo(&buf)
	m2, _ := ReadMindsicle(&buf)
	l2 := m2.Thaw(testConstraintFactory)

	if l2.Size() != 2 {
		t.Fatalf("expected 2 nodes, got %d", l2.Size())
	}
	// Both vacant.
	for i := range 2 {
		if l2.Node(lattice.NodeID(i)).Occupied() {
			t.Fatalf("node %d should be vacant", i)
		}
	}
	// Constraints survived.
	cs := l2.Node(0).Constraints()
	if len(cs) != 1 || cs[0].Tag() != "word" {
		t.Fatalf("node 0 constraints: expected [word], got %v", cs)
	}
}

func TestCandidates(t *testing.T) {
	l := lattice.New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n.AddCandidate(testElement{"word", "alpha"})
	n.AddCandidate(testElement{"word", "beta"})

	m := Freeze(l, nil)

	var buf bytes.Buffer
	m.WriteTo(&buf)
	m2, _ := ReadMindsicle(&buf)
	l2 := m2.Thaw(testConstraintFactory)

	tn := l2.Node(0)
	// Candidates are added via AddCandidate which checks constraints.
	// frozenElement has type "word" which satisfies testConstraint{"word"}.
	cands := tn.Candidates()
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(cands))
	}
	types := map[string]bool{}
	for _, c := range cands {
		types[c.Value().(string)] = true
	}
	if !types["alpha"] || !types["beta"] {
		t.Fatalf("candidates missing: got %v", types)
	}
}
