package grammar

import "github.com/mlekudev/dendrite/pkg/axiom"

// GrammarConstraint wraps a tag constraint with grammar adjacency checking.
// During bonding, it verifies that the element's type is a valid grammar
// neighbor of the types already present in the node's neighborhood.
//
// Implements axiom.ContextualConstraint. The lattice Bond() method
// dispatches to AdmitsInContext when this interface is satisfied.
type GrammarConstraint struct {
	tag     string
	grammar *Grammar
}

// NewConstraint creates a GrammarConstraint for the given tag and grammar.
func NewConstraint(tag string, g *Grammar) GrammarConstraint {
	return GrammarConstraint{tag: tag, grammar: g}
}

// Tag returns the constraint's type tag.
func (c GrammarConstraint) Tag() string { return c.tag }

// Admits checks whether the element's type matches this constraint's tag.
func (c GrammarConstraint) Admits(e axiom.Element) bool {
	return e.Type() == c.tag
}

// AdmitsInContext checks grammar adjacency: the element must have at least
// one occupied neighbor whose type is grammar-adjacent to the element's type.
// If no neighbors are occupied (seed bonding), the element is admitted.
func (c GrammarConstraint) AdmitsInContext(e axiom.Element, neighbors []axiom.Element) bool {
	if !c.Admits(e) {
		return false
	}

	// If no occupied neighbors, allow seed bonding.
	hasOccupied := false
	for _, nb := range neighbors {
		if nb != nil {
			hasOccupied = true
			break
		}
	}
	if !hasOccupied {
		return true
	}

	// At least one occupied neighbor must have a grammar-adjacent type.
	// Check bidirectionally: a→b OR b→a.
	eType := e.Type()
	for _, nb := range neighbors {
		if nb == nil {
			continue
		}
		nbType := nb.Type()
		if c.grammar.CanNeighbor(eType, nbType) || c.grammar.CanNeighbor(nbType, eType) {
			return true
		}
	}
	return false
}
