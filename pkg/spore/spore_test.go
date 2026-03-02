package spore

import (
	"bytes"
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

type tagConstraint struct{ tag string }

func (c tagConstraint) Tag() string              { return c.tag }
func (c tagConstraint) Admits(e axiom.Element) bool { return e.Type() == c.tag }

func makeLattice() *lattice.Lattice {
	l := lattice.New()

	// 10 word sites, 3 punct sites.
	words := make([]*lattice.Node, 10)
	for i := range words {
		words[i] = l.AddNode([]axiom.Constraint{tagConstraint{"word"}})
	}
	puncts := make([]*lattice.Node, 3)
	for i := range puncts {
		puncts[i] = l.AddNode([]axiom.Constraint{tagConstraint{"punct"}})
	}

	// Connect.
	for i := range words {
		l.Connect(words[i], words[(i+1)%len(words)])
	}
	for i := range puncts {
		l.Connect(puncts[i], puncts[(i+1)%len(puncts)])
	}
	l.Connect(words[0], puncts[0])

	// Bond some elements.
	words[0].Bond(enzyme.Elem("word", "hello"))
	words[1].Bond(enzyme.Elem("word", "world"))
	puncts[0].Bond(enzyme.Elem("punct", "!"))

	return l
}

func TestExtract(t *testing.T) {
	l := makeLattice()
	s := Extract(l)

	if s.TotalNodes != 13 {
		t.Errorf("expected 13 nodes, got %d", s.TotalNodes)
	}
	if tagCountValue(s.TypeSignature, "word") != 10 {
		t.Errorf("expected 10 word constraints, got %d", tagCountValue(s.TypeSignature, "word"))
	}
	if tagCountValue(s.TypeSignature, "punct") != 3 {
		t.Errorf("expected 3 punct constraints, got %d", tagCountValue(s.TypeSignature, "punct"))
	}
	if tagCountValue(s.ElementTypes, "word") != 2 {
		t.Errorf("expected 2 word elements, got %d", tagCountValue(s.ElementTypes, "word"))
	}
	if s.Occupied != 3 {
		t.Errorf("expected 3 occupied, got %d", s.Occupied)
	}

	t.Log(s.String())
}

func TestNucleate(t *testing.T) {
	l := makeLattice()
	s := Extract(l)

	// Nucleate a new lattice of size 50.
	l2 := s.Nucleate(50, func(tag string) axiom.Constraint {
		return tagConstraint{tag}
	})

	if l2.Size() < 40 || l2.Size() > 55 {
		t.Errorf("expected ~50 nodes, got %d", l2.Size())
	}

	// Check that type proportions are roughly preserved.
	wordCount := 0
	punctCount := 0
	for _, n := range l2.Nodes() {
		cs := n.Constraints()
		if len(cs) > 0 {
			switch cs[0].Tag() {
			case "word":
				wordCount++
			case "punct":
				punctCount++
			}
		}
	}

	t.Logf("nucleated: %d word, %d punct (total %d)", wordCount, punctCount, l2.Size())

	// Word should dominate (10:3 ratio in source).
	if wordCount < punctCount {
		t.Error("word count should be greater than punct count")
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	l := makeLattice()
	s := Extract(l)

	var buf bytes.Buffer
	_, err := s.WriteTo(&buf)
	if err != nil {
		t.Fatal(err)
	}

	s2, err := ReadSpore(&buf)
	if err != nil {
		t.Fatal(err)
	}

	if s2.TotalNodes != s.TotalNodes {
		t.Errorf("total nodes mismatch: %d vs %d", s2.TotalNodes, s.TotalNodes)
	}
	if tagCountValue(s2.TypeSignature, "word") != tagCountValue(s.TypeSignature, "word") {
		t.Error("word count mismatch after roundtrip")
	}
}

func TestLineage(t *testing.T) {
	l := makeLattice()
	gen0 := Extract(l)

	if gen0.Generation != 0 {
		t.Errorf("gen0 should be generation 0, got %d", gen0.Generation)
	}
	if gen0.ParentHash != "" {
		t.Error("gen0 should have no parent hash")
	}

	h0 := gen0.Hash()
	if len(h0) != 64 {
		t.Errorf("hash should be 64 hex chars, got %d", len(h0))
	}

	// Nucleate daughter, feed it, sporulate with lineage.
	daughter := gen0.Nucleate(20, func(tag string) axiom.Constraint {
		return tagConstraint{tag}
	})
	daughter.Nodes()[0].Bond(enzyme.Elem("word", "test"))

	gen1 := Extract(daughter, gen0)

	if gen1.Generation != 1 {
		t.Errorf("gen1 should be generation 1, got %d", gen1.Generation)
	}
	if gen1.ParentHash != h0 {
		t.Error("gen1 parent hash should match gen0 hash")
	}

	// Second generation.
	d2 := gen1.Nucleate(30, func(tag string) axiom.Constraint {
		return tagConstraint{tag}
	})
	gen2 := Extract(d2, gen1)

	if gen2.Generation != 2 {
		t.Errorf("gen2 should be generation 2, got %d", gen2.Generation)
	}
	if gen2.ParentHash != gen1.Hash() {
		t.Error("gen2 parent hash should match gen1 hash")
	}

	t.Logf("lineage: gen0=%s...", h0[:16])
	t.Logf("         gen1=%s... (parent=%s...)", gen1.Hash()[:16], gen1.ParentHash[:16])
	t.Logf("         gen2=%s... (parent=%s...)", gen2.Hash()[:16], gen2.ParentHash[:16])
}
