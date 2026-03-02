// Package lattice implements the growth structure — nodes, sites, constraint
// envelopes, and the graph that connects them.
//
// The lattice is the crystalline state. It grows by accretion, prunes by
// dissolution, and maintains dynamic equilibrium. It is a concurrent data
// structure: goroutines walk it, bond to it, and evaluate it simultaneously.
package lattice

import (
	"math/rand/v2"
	"sync"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/mlekudev/dendrite/pkg/state"
)

// NodeID is a unique identifier for a lattice node.
// It is the index into the lattice's node slice (0-based).
type NodeID uint64

// LockInDepth measures how firmly an element is held at a lattice site.
// Higher values mean deeper lock-in — more constraint satisfaction,
// harder to dissolve. Stored as an exact rational number.
type LockInDepth = ratio.Ratio

// Node is a position in the lattice. Each node has a constraint envelope
// (the negative space defining what fits), an optional occupant, and
// connections to neighbors.
type Node struct {
	mu sync.RWMutex

	id          NodeID
	constraints []axiom.Constraint
	occupant    axiom.Element    // nil if vacant
	candidates  []axiom.Element  // ambiguity: multiple valid fillers (Lake state)
	neighbors   []*Node
	hex         state.Hexagram
	lockIn      LockInDepth
	bondCount   int    // number of constraints satisfied by current occupant
	crossLayer  bool   // true if occupant is misaligned with constraint layer (post-hoc detection target)
	perm        uint8  // S_3 permutation index (0-5), set by PermutedElement on bond
	projVertex  uint8  // 3-bit cube vertex (0-7), set by ProjectedElement on bond
	projKey     uint8  // 3-bit projection key (0-7), set by ProjectedElement on bond
	projPath    uint16 // rendering path index, set by ProjectedElement on bond
	age         uint8  // 2-bit ADSR envelope (0=Attack, 1=Decay, 2=Sustain, 3=Release)
}

// ID returns the node's unique identifier.
func (n *Node) ID() NodeID {
	return n.id
}

// Hexagram returns the node's current dynamical state.
func (n *Node) Hexagram() state.Hexagram {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.hex
}

// Occupied reports whether this node has an element bonded to it.
func (n *Node) Occupied() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.occupant != nil
}

// Occupant returns the bonded element, or nil if vacant.
func (n *Node) Occupant() axiom.Element {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.occupant
}

// CrossLayer reports whether this node's occupant is misaligned with the
// node's constraint layer. These bonds formed legitimately (the element
// satisfied the constraint's Admits check) but across a layer boundary.
// The biological analog: nicotine binding at VTA dopamine neurons instead
// of the intended nicotinic receptor site. The bond is real but weaker,
// and the post-hoc scanner flags it for preferential dissolution.
func (n *Node) CrossLayer() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.crossLayer
}

// LockIn returns the current lock-in depth.
func (n *Node) LockIn() LockInDepth {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lockIn
}

// Permutation returns the node's active S_3 permutation index (0-5).
// Identity (0) is the default for nodes without a PermutedElement.
func (n *Node) Permutation() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.perm
}

// SetPermutation sets the node's active S_3 permutation index.
// Values >= 6 are ignored.
func (n *Node) SetPermutation(p uint8) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if p < 6 {
		n.perm = p
	}
}

// ProjectionVertex returns the node's 3-bit cube vertex (0-7).
func (n *Node) ProjectionVertex() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.projVertex
}

// ProjectionKey returns the node's 3-bit projection key (0-7).
func (n *Node) ProjectionKey() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.projKey
}

// ProjectionPath returns the node's rendering path index.
func (n *Node) ProjectionPath() uint16 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.projPath
}

// SetProjection sets the node's full projection encoding.
func (n *Node) SetProjection(vertex, key uint8, path uint16) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if vertex < 8 {
		n.projVertex = vertex
	}
	if key < 8 {
		n.projKey = key
	}
	n.projPath = path
}

// Projection6Bit returns the packed 6-bit projection: vertex (low 3) | key (high 3).
func (n *Node) Projection6Bit() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return (n.projVertex & 0b111) | (n.projKey&0b111)<<3
}

// Age returns the node's current age (0-3), encoding the ADSR envelope:
//
//	0 = Attack  — freshly bonded, high-energy accretion
//	1 = Decay   — actively growing, settling
//	2 = Sustain — locked in, durable (stable attractor)
//	3 = Release — dissolving, returning to solution
func (n *Node) Age() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.age
}

// IncrementAge advances Attack→Decay→Sustain automatically.
// Sustain is the stable attractor — nodes remain there indefinitely
// while well-supported. The transition to Release requires an explicit
// Destabilize() call triggered by oscillation or weak lock-in.
func (n *Node) IncrementAge() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.age < 2 {
		n.age++
	}
}

// Destabilize forces a Sustain node into Release phase.
// No-op if the node is not in Sustain (age 2).
func (n *Node) Destabilize() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.age == 2 {
		n.age = 3
	}
}

// ProjectionByte returns the full 8-bit encoding:
//
//	bit  7  6  5  4  3  2  1  0
//	    [age ] [  key  ] [vertex]
func (n *Node) ProjectionByte() uint8 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return (n.age&0b11)<<6 | (n.projKey&0b111)<<3 | (n.projVertex & 0b111)
}

// ContextualLockIn computes effective lock-in that accounts for neighborhood.
// A bonded element surrounded by occupied neighbors has higher effective lock-in
// than an isolated one. This allows dissolution to selectively remove elements
// that bonded but lack structural support from their neighborhood.
//
// The result is normalized to [0,1] range:
//   - neighborOccupancyRate alone determines the contextual factor
//   - baseLockIn > 0 means the element bonded validly; the neighborhood
//     determines how firmly it's held
//
// Formula: 0.3 + 0.7 * neighborOccupancyRate
// - No neighbors: 0.3 (weakly held)
// - All neighbors empty: 0.3
// - All neighbors occupied: 1.0
func (n *Node) ContextualLockIn() LockInDepth {
	n.mu.RLock()
	base := n.lockIn
	nbs := n.neighbors
	n.mu.RUnlock()

	if base.IsZero() {
		return ratio.Zero // not bonded at all
	}

	if len(nbs) == 0 {
		return ratio.New(3, 10) // no neighbors = weakly held
	}

	occupied := 0
	for _, nb := range nbs {
		if nb.Occupied() {
			occupied++
		}
	}
	rate := ratio.New(int64(occupied), int64(len(nbs)))
	return ratio.New(3, 10).Add(ratio.New(7, 10).Mul(rate))
}

// Constraints returns the node's constraint envelope.
func (n *Node) Constraints() []axiom.Constraint {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]axiom.Constraint, len(n.constraints))
	copy(out, n.constraints)
	return out
}

// Neighbors returns all neighbor nodes. The caller must not modify the slice.
func (n *Node) Neighbors() []*Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.neighbors
}

// Admits checks whether the given element satisfies this node's constraint
// envelope. Does not acquire write lock — read-only check.
//
// Cross-layer bonding is permitted — biology has no pre-hoc type enforcer.
// A molecule binds wherever its shape fits, even across receptor systems.
// Cross-layer bonds are detected and corrected post-hoc by the dissolution
// scanner, not prevented at admission. The coherence field is diagnostic,
// not structural.
func (n *Node) Admits(e axiom.Element) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.occupant != nil {
		return false // already occupied
	}
	if len(n.constraints) == 0 {
		return false // no constraints = no shape = nothing fits
	}
	for _, c := range n.constraints {
		if !c.Admits(e) {
			return false
		}
	}
	return true
}

// Bond attempts to place an element at this node. Returns true if the
// bond formed, false if the site was already claimed or the element
// doesn't fit. This is an atomic operation — only one goroutine bonds
// at a time per node.
//
// Cross-layer bonding is permitted. Biology allows molecules to bind
// at receptors across system boundaries (nicotine hits both neuromuscular
// junction and VTA dopamine neurons). Misaligned bonds are weaker —
// they contribute less lock-in — and are preferentially cleared by
// the dissolution scanner's post-hoc detection pass.
func (n *Node) Bond(e axiom.Element) bool {
	// Snapshot neighbor occupants BEFORE acquiring the node lock.
	// This prevents ABBA deadlock: if two adjacent nodes are being bonded
	// concurrently, holding node_X.Lock while reading node_Y.RLock (and
	// vice versa) creates a circular wait. By reading neighbors first
	// with only RLocks (released immediately), we avoid holding two
	// write locks simultaneously.
	//
	// The snapshot may be slightly stale by the time we check it, but
	// contextual constraints (grammar adjacency) are heuristic checks —
	// a brief window of inconsistency is acceptable.
	var nbOccupants []axiom.Element
	hasContextual := false
	for _, c := range n.constraints {
		if _, ok := c.(axiom.ContextualConstraint); ok {
			hasContextual = true
			break
		}
	}
	if hasContextual {
		n.mu.RLock()
		neighbors := n.neighbors
		n.mu.RUnlock()
		nbOccupants = make([]axiom.Element, len(neighbors))
		for i, nb := range neighbors {
			nb.mu.RLock()
			nbOccupants[i] = nb.occupant
			nb.mu.RUnlock()
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.occupant != nil {
		return false
	}
	// Verify constraints under write lock. Cross-layer bonds are permitted
	// but tracked: misaligned bonds contribute reduced lock-in rather than
	// being prevented outright.
	satisfied := 0
	misaligned := false
	for _, c := range n.constraints {
		if lc, ok := c.(axiom.LayeredConstraint); ok {
			if le, ok := e.(axiom.LayeredElement); ok {
				if !lc.Aligns(le) {
					misaligned = true
				}
			}
		}
		if c.Admits(e) {
			satisfied++
		} else {
			return false
		}
	}
	// Contextual constraint check using pre-collected neighbor snapshot.
	if hasContextual {
		for _, c := range n.constraints {
			if cc, ok := c.(axiom.ContextualConstraint); ok {
				if !cc.AdmitsInContext(e, nbOccupants) {
					return false
				}
			}
		}
	}

	n.occupant = e
	n.bondCount = satisfied
	n.crossLayer = misaligned
	// Cross-layer bonds form but are weaker — reduced lock-in makes them
	// preferential dissolution targets. Biology: nicotine binds VTA receptors
	// but the bond is pharmacologically weaker than at the intended nicotinic
	// receptor site. The "symptom" (tremor, euphoria) is the observable
	// consequence; dissolution (metabolic clearance + dose adjustment) is
	// the correction mechanism.
	if misaligned {
		n.lockIn = ratio.New(int64(satisfied), 2) // half lock-in for cross-layer
	} else {
		n.lockIn = ratio.FromInt(int64(satisfied))
	}
	// Capture projection angle from element if it carries one.
	if pe, ok := e.(axiom.PermutedElement); ok {
		p := pe.Permutation()
		if p < 6 {
			n.perm = p
		}
	}
	// Capture full cubic projection encoding if the element carries one.
	if pe, ok := e.(axiom.ProjectedElement); ok {
		v := pe.ProjectionVertex()
		if v < 8 {
			n.projVertex = v
		}
		k := pe.ProjectionKey()
		if k < 8 {
			n.projKey = k
		}
		n.projPath = pe.ProjectionPath()
		// Derive S_3 permutation from the projection key for consistency.
		// The key's permutation takes precedence over PermutedElement.
		n.perm = keyToPermLookup(k)
	}
	n.age = 0 // newborn on bond
	n.updateHex()
	return true
}

// Dissolve removes the occupant from this node, returning the element
// to the free pool. Returns the dissolved element, or nil if vacant.
func (n *Node) Dissolve() axiom.Element {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := n.occupant
	n.occupant = nil
	n.candidates = nil
	n.bondCount = 0
	n.crossLayer = false
	n.lockIn = ratio.Zero
	n.perm = 0 // reset to Identity
	n.projVertex = 0
	n.projKey = 0
	n.projPath = 0
	n.age = 0
	n.updateHex()
	return e
}

// AddCandidate records an element as a valid filler for this site
// without committing to it. This is structured ambiguity — the Lake
// state (011). Multiple valid occupants coexist until external context
// narrows the set.
//
// Returns true if the candidate was added, false if it doesn't satisfy
// constraints or is a duplicate.
func (n *Node) AddCandidate(e axiom.Element) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Must satisfy all constraints. Cross-layer candidates are admitted —
	// ambiguity can span layers, just as a biological receptor site can
	// admit structurally similar molecules from different systems.
	for _, c := range n.constraints {
		if !c.Admits(e) {
			return false
		}
	}
	// Check for duplicates.
	for _, existing := range n.candidates {
		if existing.Type() == e.Type() && existing.Value() == e.Value() {
			return false
		}
	}
	// Cap candidate list to prevent memory bloat. When thousands of
	// elements satisfy a constraint (e.g., ident nodes with 9000+
	// identifiers), storing all of them serves no purpose — the
	// lattice only needs a small pool for ambiguity resolution.
	const maxCandidates = 32
	if len(n.candidates) >= maxCandidates {
		return false
	}
	n.candidates = append(n.candidates, e)
	return true
}

// Candidates returns the set of valid fillers at this site.
// Empty if no ambiguity has been recorded.
func (n *Node) Candidates() []axiom.Element {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(n.candidates) == 0 {
		return nil
	}
	out := make([]axiom.Element, len(n.candidates))
	copy(out, n.candidates)
	return out
}

// Ambiguous reports whether this site has multiple valid fillers.
func (n *Node) Ambiguous() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.candidates) > 1
}

// Collapse resolves ambiguity by selecting one candidate as the occupant
// and clearing the rest. The selector function chooses which candidate
// wins. Returns true if collapse occurred.
func (n *Node) Collapse(selector func([]axiom.Element) axiom.Element) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.candidates) < 2 {
		return false
	}
	chosen := selector(n.candidates)
	if chosen == nil {
		return false
	}
	// Bond the chosen element.
	satisfied := 0
	for _, c := range n.constraints {
		if c.Admits(chosen) {
			satisfied++
		}
	}
	n.occupant = chosen
	n.bondCount = satisfied
	n.lockIn = ratio.FromInt(int64(satisfied))
	n.candidates = nil
	n.updateHex()
	return true
}

// keyToPermLookup maps a 3-bit projection key to an S_3 permutation index.
// This duplicates the mapping from the projection package to avoid circular
// imports. The canonical mapping is in projection.keyToPerm.
func keyToPermLookup(k uint8) uint8 {
	// Keys 0-5 map to S_3 permutations; 6,7 collapse to 0,1.
	switch k {
	case 0:
		return 0 // Identity
	case 1:
		return 3 // Swap12 (Swap C,E)
	case 2:
		return 2 // Swap02 (Swap B,E)
	case 3:
		return 1 // Swap01 (Swap B,C)
	case 4:
		return 4 // Cycle012
	case 5:
		return 5 // Cycle021
	case 6:
		return 0 // Collapse → Identity
	case 7:
		return 3 // Collapse → Swap12
	default:
		return 0
	}
}

// updateHex recalculates the node's hexagram based on current state.
// Must be called with write lock held.
func (n *Node) updateHex() {
	// Preserve the energy bit — it's set externally by supersaturation.
	currentEnergy := n.hex.Inner().Energy()
	inner := state.Trigram(0).
		SetBonding(n.occupant != nil).
		SetConstraint(n.occupant != nil && n.bondCount > 0).
		SetEnergy(currentEnergy)
	n.hex = state.Hex(inner, n.hex.Outer())
}

// Lattice is the graph of nodes — the crystalline structure.
type Lattice struct {
	mu    sync.RWMutex
	nodes []*Node

	// vacantIdx maps constraint tags to slices of node IDs with matching
	// vacant sites. This is the vascular index — active transport routes
	// elements to demand sites instead of relying on diffusion. Entries
	// may go stale (node bonded since indexing); VacantByTag lazily
	// compacts stale entries on access.
	vacantIdx map[string][]NodeID
}

// New creates an empty lattice.
func New() *Lattice {
	return &Lattice{
		nodes:     make([]*Node, 0, 256),
		vacantIdx: make(map[string][]NodeID),
	}
}

// AddNode creates a new node with the given constraint envelope and
// adds it to the lattice. Returns the new node.
func (l *Lattice) AddNode(constraints []axiom.Constraint) *Node {
	l.mu.Lock()
	id := NodeID(len(l.nodes))
	n := &Node{
		id:          id,
		constraints: constraints,
		neighbors:   getNeighborSlice(),
	}
	l.nodes = append(l.nodes, n)
	// Register in vascular index: this vacant site is now reachable
	// by directed transport.
	for _, c := range constraints {
		tag := c.Tag()
		l.vacantIdx[tag] = append(l.vacantIdx[tag], id)
	}
	l.mu.Unlock()
	return n
}

// Connect creates a bidirectional neighbor relationship between two nodes.
// This is anastomosis — creating new paths in the graph.
func (l *Lattice) Connect(a, b *Node) {
	if a == b {
		return // no self-loops
	}
	// Lock both nodes in ID order to prevent deadlock.
	first, second := a, b
	if first.id > second.id {
		first, second = second, first
	}
	first.mu.Lock()
	second.mu.Lock()
	// Check for duplicate before appending.
	if !hasNeighbor(a.neighbors, b) {
		a.neighbors = append(a.neighbors, b)
	}
	if !hasNeighbor(b.neighbors, a) {
		b.neighbors = append(b.neighbors, a)
	}
	second.mu.Unlock()
	first.mu.Unlock()
}

// Disconnect severs the neighbor relationship between two nodes.
func (l *Lattice) Disconnect(a, b *Node) {
	if a == b {
		return
	}
	first, second := a, b
	if first.id > second.id {
		first, second = second, first
	}
	first.mu.Lock()
	second.mu.Lock()
	a.neighbors = removeNeighbor(a.neighbors, b)
	b.neighbors = removeNeighbor(b.neighbors, a)
	second.mu.Unlock()
	first.mu.Unlock()
}

// hasNeighbor reports whether nb is in the neighbor slice.
func hasNeighbor(nbs []*Node, nb *Node) bool {
	for _, n := range nbs {
		if n == nb {
			return true
		}
	}
	return false
}

// removeNeighbor removes nb from the slice using swap-remove. Order
// does not matter for neighbor lists.
func removeNeighbor(nbs []*Node, nb *Node) []*Node {
	for i, n := range nbs {
		if n == nb {
			last := len(nbs) - 1
			nbs[i] = nbs[last]
			nbs[last] = nil // clear for GC
			return nbs[:last]
		}
	}
	return nbs
}

// Node returns a node by ID, or nil if not found.
func (l *Lattice) Node(id NodeID) *Node {
	l.mu.RLock()
	defer l.mu.RUnlock()
	idx := int(id)
	if idx < 0 || idx >= len(l.nodes) {
		return nil
	}
	return l.nodes[idx]
}

// Size returns the number of nodes in the lattice.
func (l *Lattice) Size() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.nodes)
}

// Nodes returns all nodes. The caller must not modify the slice.
func (l *Lattice) Nodes() []*Node {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.nodes
}

// VacantSites returns all nodes that are unoccupied and have constraints
// (i.e., have a defined shape that something could fill).
func (l *Lattice) VacantSites() []*Node {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var sites []*Node
	for _, n := range l.nodes {
		n.mu.RLock()
		vacant := n.occupant == nil && len(n.constraints) > 0
		n.mu.RUnlock()
		if vacant {
			sites = append(sites, n)
		}
	}
	return sites
}

// Health reports the lattice's structural health metrics.
type Health struct {
	NodeCount      int         // total nodes
	Occupied       int         // nodes with bonded elements
	Vacant         int         // nodes available for bonding
	AvgLockIn      ratio.Ratio // average lock-in depth across occupied nodes
	MaxLockIn      ratio.Ratio // deepest lock-in
	Ambiguous      int         // nodes with multiple candidate elements
	OccupancyRate  ratio.Ratio // occupied / total [0, 1]
	AccretionReady int         // vacant nodes with energy (ready for growth)
	DissolveSoft   int         // occupied nodes with lock-in < 1 (dissolution candidates)
}

// Health computes the current structural health of the lattice.
func (l *Lattice) Health() Health {
	l.mu.RLock()
	defer l.mu.RUnlock()

	h := Health{NodeCount: len(l.nodes)}
	totalLockIn := ratio.Zero

	for _, n := range l.nodes {
		n.mu.RLock()
		if n.occupant != nil {
			h.Occupied++
			totalLockIn = totalLockIn.Add(n.lockIn)
			if h.MaxLockIn.Less(n.lockIn) {
				h.MaxLockIn = n.lockIn
			}
			if n.lockIn.Less(ratio.One) {
				h.DissolveSoft++
			}
		} else if len(n.constraints) > 0 {
			h.Vacant++
			if n.hex.Inner().Energy() {
				h.AccretionReady++
			}
		}
		if len(n.candidates) > 1 {
			h.Ambiguous++
		}
		n.mu.RUnlock()
	}

	if h.Occupied > 0 {
		h.AvgLockIn = totalLockIn.Div(ratio.FromInt(int64(h.Occupied)))
	}
	if h.NodeCount > 0 {
		h.OccupancyRate = ratio.New(int64(h.Occupied), int64(h.NodeCount))
	}

	return h
}

// ClearOccupants removes all occupants and resets bond counts across the
// lattice while preserving topology (nodes, edges) and constraints.
// Used for detection: thaw a trained lattice, clear its occupants, then
// try to grow sample text into the constraint-shaped topology.
func (l *Lattice) ClearOccupants() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, n := range l.nodes {
		n.mu.Lock()
		n.occupant = nil
		n.candidates = nil
		n.bondCount = 0
		n.lockIn = ratio.Zero
		n.age = 0
		n.mu.Unlock()
	}
	// Rebuild vacant index — all nodes are now vacant.
	l.vacantIdx = make(map[string][]NodeID, len(l.vacantIdx))
	for _, n := range l.nodes {
		for _, c := range n.constraints {
			l.vacantIdx[c.Tag()] = append(l.vacantIdx[c.Tag()], n.id)
		}
	}
}

// RandomNode returns a random node from the lattice for walk initialization.
// Returns nil if the lattice is empty.
func (l *Lattice) RandomNode() *Node {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.nodes) == 0 {
		return nil
	}
	return l.nodes[rand.IntN(len(l.nodes))]
}

// VacantByTag returns a random vacant node whose constraint envelope
// matches the given tag. This is directed transport — the vascular
// system routing elements to sites of demand instead of relying on
// diffusion. Returns nil if no matching vacant site exists.
//
// Lazily compacts stale entries (nodes that were bonded since indexing).
func (l *Lattice) VacantByTag(tag string) *Node {
	l.mu.Lock()
	defer l.mu.Unlock()

	ids := l.vacantIdx[tag]
	if len(ids) == 0 {
		return nil
	}

	// Try up to len(ids) random picks, compacting stale entries.
	for attempts := len(ids); attempts > 0 && len(ids) > 0; attempts-- {
		idx := rand.IntN(len(ids))
		nid := ids[idx]
		if int(nid) >= len(l.nodes) {
			// Stale — swap-remove.
			ids[idx] = ids[len(ids)-1]
			ids = ids[:len(ids)-1]
			l.vacantIdx[tag] = ids
			continue
		}
		n := l.nodes[nid]
		n.mu.RLock()
		vacant := n.occupant == nil && n.constraints != nil
		n.mu.RUnlock()
		if vacant {
			return n
		}
		// Stale — node was bonded. Swap-remove.
		ids[idx] = ids[len(ids)-1]
		ids = ids[:len(ids)-1]
		l.vacantIdx[tag] = ids
	}
	return nil
}

// ReindexVacant re-registers a node in the vascular index after its
// occupant was dissolved. The node is now vacant and available for
// directed transport.
func (l *Lattice) ReindexVacant(n *Node) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n.mu.RLock()
	constraints := n.constraints
	n.mu.RUnlock()
	for _, c := range constraints {
		tag := c.Tag()
		l.vacantIdx[tag] = append(l.vacantIdx[tag], n.id)
	}
}

// RandomNeighbor returns a random neighbor of the given node.
// Returns nil if the node has no neighbors.
func RandomNeighbor(n *Node) *Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(n.neighbors) == 0 {
		return nil
	}
	return n.neighbors[rand.IntN(len(n.neighbors))]
}

// SetEnergy updates the energy bit of a node's inner trigram.
// Called by the supersaturation system when local energy changes.
func (n *Node) SetEnergy(supersaturated bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hex = state.Hex(n.hex.Inner().SetEnergy(supersaturated), n.hex.Outer())
}

// SetOuterTrigram updates the node's outer trigram based on its
// environment (computed from neighbor states).
func (n *Node) SetOuterTrigram(outer state.Trigram) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hex = state.Hex(n.hex.Inner(), outer)
}

// BondCount returns the number of constraints satisfied by the current occupant.
func (n *Node) BondCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.bondCount
}

// RestoreAge sets the node's age directly (bypass increment logic).
// Used during mindsicle thaw to restore frozen state.
func (n *Node) RestoreAge(age uint8) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if age > 3 {
		age = 3
	}
	n.age = age
}

// ForceOccupant places an element without checking constraints.
// Used during mindsicle thaw where the state has been pre-validated.
// Sets occupant, bondCount, lockIn, age=0, calls updateHex().
func (n *Node) ForceOccupant(e axiom.Element, bondCount int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.occupant = e
	n.bondCount = bondCount
	n.lockIn = ratio.FromInt(int64(bondCount))
	n.age = 0
	n.updateHex()
}

// --- Unsafe accessors for block-decomposed growth ---
// These methods skip mutex acquisition. The contract: the caller has
// exclusive access to this node (e.g., owns the block containing it).
// Using these on a shared node is a data race.

// OccupiedUnsafe checks occupancy without locking.
func (n *Node) OccupiedUnsafe() bool {
	return n.occupant != nil
}

// NeighborsUnsafe returns the neighbor slice without locking.
func (n *Node) NeighborsUnsafe() []*Node {
	return n.neighbors
}

// AdmitsUnsafe checks constraint satisfaction without locking.
func (n *Node) AdmitsUnsafe(e axiom.Element) bool {
	if n.occupant != nil {
		return false
	}
	if len(n.constraints) == 0 {
		return false
	}
	for _, c := range n.constraints {
		if !c.Admits(e) {
			return false
		}
	}
	return true
}

// BondUnsafe places an element without locking. Same logic as Bond
// but no mutex and no neighbor snapshot for contextual constraints
// (caller must handle cross-block neighbors separately).
func (n *Node) BondUnsafe(e axiom.Element) bool {
	if n.occupant != nil {
		return false
	}
	satisfied := 0
	misaligned := false
	for _, c := range n.constraints {
		if lc, ok := c.(axiom.LayeredConstraint); ok {
			if le, ok := e.(axiom.LayeredElement); ok {
				if !lc.Aligns(le) {
					misaligned = true
				}
			}
		}
		if c.Admits(e) {
			satisfied++
		} else {
			return false
		}
	}
	n.occupant = e
	n.bondCount = satisfied
	n.crossLayer = misaligned
	if misaligned {
		n.lockIn = ratio.New(int64(satisfied), 2)
	} else {
		n.lockIn = ratio.FromInt(int64(satisfied))
	}
	if pe, ok := e.(axiom.PermutedElement); ok {
		p := pe.Permutation()
		if p < 6 {
			n.perm = p
		}
	}
	if pe, ok := e.(axiom.ProjectedElement); ok {
		v := pe.ProjectionVertex()
		if v < 8 {
			n.projVertex = v
		}
		k := pe.ProjectionKey()
		if k < 8 {
			n.projKey = k
		}
		n.projPath = pe.ProjectionPath()
		n.perm = keyToPermLookup(k)
	}
	n.age = 0
	n.updateHex()
	return true
}

// StripForEvictionUnsafe zeros all node state except id and neighbors.
// The node remains in the lattice's node slice so that neighbor pointers
// from adjacent blocks stay valid. The stripped node consumes ~64 bytes
// (struct overhead + neighbor slice) vs ~200+ bytes when fully populated.
// The caller must have exclusive access (no concurrent readers/writers).
func (n *Node) StripForEvictionUnsafe() {
	n.occupant = nil
	n.candidates = nil
	n.constraints = nil
	n.bondCount = 0
	n.crossLayer = false
	n.lockIn = ratio.Zero
	n.perm = 0
	n.projVertex = 0
	n.projKey = 0
	n.projPath = 0
	n.age = 0
	n.hex = 0
}

// RestoreConstraintsUnsafe sets the constraint envelope on a stripped node.
// Called during block thaw after loading constraint tags from disk and
// reconstructing constraint objects via the factory function.
// The caller must have exclusive access.
func (n *Node) RestoreConstraintsUnsafe(constraints []axiom.Constraint) {
	n.constraints = constraints
}

// NeighborStates returns a summary trigram of the neighborhood.
// Majority vote on each bit across all neighbors.
func (n *Node) NeighborStates() state.Trigram {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.neighbors) == 0 {
		return state.Earth // no neighbors = substrate
	}

	var bonding, constraint, energy int
	total := len(n.neighbors)
	for _, nb := range n.neighbors {
		nb.mu.RLock()
		inner := nb.hex.Inner()
		nb.mu.RUnlock()
		if inner.Bonding() {
			bonding++
		}
		if inner.Constraint() {
			constraint++
		}
		if inner.Energy() {
			energy++
		}
	}

	return state.Trigram(0).
		SetBonding(bonding > total/2).
		SetConstraint(constraint > total/2).
		SetEnergy(energy > total/2)
}
