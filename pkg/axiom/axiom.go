// Package axiom defines the seed crystal: the axiom pair from which all
// lattice dynamics derive.
//
// Coherence is determinism. Incoherence is nondeterminism.
//
// These two interfaces are the only hand-written structure. Everything
// else is grown.
package axiom

// Constraint defines the shape of what fits at a lattice site.
// A constraint is negative space — it specifies what an occupant must
// satisfy without specifying the occupant itself.
type Constraint interface {
	// Tag identifies the type layer this constraint belongs to.
	// Constraints from different type layers cannot cross-bond.
	Tag() string

	// Admits reports whether an element satisfies this constraint.
	Admits(Element) bool
}

// Element is the minimal unit that can exist in either the coherent
// (lattice-bound) or incoherent (dissolved) state.
type Element interface {
	// Type returns the element's type tag. An element can only bond
	// at sites whose constraints share its type layer.
	Type() string

	// Value returns the element's content — opaque to the lattice,
	// meaningful only to the constraint that admits it.
	Value() any
}

// Coherent describes something that has constraints and can be checked
// against them. The lattice. The crystalline state. The axiom side.
type Coherent interface {
	// Constraints returns the constraint envelope at this position —
	// the negative space that defines what can bond here.
	Constraints() []Constraint

	// Satisfies reports whether this structure satisfies a given
	// constraint. Used when two lattice regions meet (anastomosis)
	// to check alignment compatibility.
	Satisfies(Constraint) bool
}

// Incoherent describes something that can dissolve into elements and
// report availability. The solution. The dissolved state. The inverse.
type Incoherent interface {
	// Dissolve breaks this structure into its constituent elements,
	// returning them to the free-floating pool.
	Dissolve() []Element

	// Available reports whether this substrate has elements that
	// could potentially bond into a lattice.
	Available() bool
}

// Layer identifies a type layer in the coherence field. Constraints and
// elements belong to layers. Cross-layer bonding is structurally prevented
// — a procedural element cannot nucleate in a lexical region.
type Layer struct {
	Name  string // e.g. "lexical", "syntactic", "semantic"
	Depth int    // 0 = coarsest, higher = finer grain
}

// StickyElement extends Element with dissolution resistance.
// Elements implementing this interface with IsSticky() == true
// survive dissolution regardless of lock-in depth.
type StickyElement interface {
	Element
	IsSticky() bool
}

// LayeredElement extends Element with layer information.
type LayeredElement interface {
	Element
	Layer() Layer
}

// LayeredConstraint extends Constraint with layer information and
// hierarchical alignment checking.
type LayeredConstraint interface {
	Constraint

	// Layer returns the type layer this constraint operates in.
	Layer() Layer

	// Aligns reports whether an element's layer is compatible with
	// this constraint's layer. The coherence field: preventing
	// cross-layer bonding without directing individual elements.
	Aligns(LayeredElement) bool
}

// PermutedElement extends Element with an S_3 projection angle.
// Elements implementing this interface carry a permutation index (0-5)
// that determines which variant transition table governs the lattice
// node they bond to. The permutation reorders the 3 trigram axes
// (Bonding, Constraint, Energy), giving each element its own "shadow"
// of the hexagram dynamics.
type PermutedElement interface {
	Element
	Permutation() uint8 // 0-5, index into S_3
}

// ProjectedElement extends Element with the full cubic projection encoding.
// Elements implementing this interface carry a 6-bit projection identity
// (3-bit vertex + 3-bit key) plus a path index encoding the rendering
// sequence — the temporal order of growth.
type ProjectedElement interface {
	Element
	ProjectionVertex() uint8 // 0-7: which cube vertex
	ProjectionKey() uint8    // 0-7: which projection direction
	ProjectionPath() uint16  // path index: rendering sequence
}

// HexagramElement extends Element with hexagram-encoded value.
// Elements implementing this interface carry their value as a sequence
// of 6-bit hexagram tokens (0-63) alongside the raw value. The encoding
// is deterministic and reversible: 3 bytes → 4 tokens (24 bits = 4 × 6 bits).
type HexagramElement interface {
	Element
	HexTokens() []uint8 // 6-bit hexagram tokens (each in 0-63)
	OrigLen() int        // original byte length before encoding
}

// ContextualConstraint extends Constraint with neighborhood awareness.
// During bonding, the lattice checks whether the element's causal
// prerequisites are satisfied by examining occupied neighbors.
// This allows the lattice to learn causal correctness structurally.
type ContextualConstraint interface {
	Constraint

	// AdmitsInContext checks whether the element can bond at this
	// position given the neighborhood. The neighbors slice contains
	// the occupants of all neighboring nodes (nil entries for vacant).
	AdmitsInContext(elem Element, neighbors []Element) bool
}
