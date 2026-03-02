package grammar

import (
	"math/rand/v2"
	"sort"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// BuildGrammarLattice creates a lattice with grammar-shaped topology.
//
// Each tag gets counts[tag] nodes. Within each tag group, nodes are
// connected in a ring (preserving locality). Between groups, connections
// are made according to the grammar's adjacency rules: a node with tag A
// is connected to nodes with tags that Grammar.CanNeighbor(A, B) permits.
//
// The instanceSeed provides per-instance variation: different seeds produce
// different selections of which grammar-permitted connections are made.
// Same grammar rules, different topological realization. This is what
// differentiates colony instances — the grammar defines the rigid backbone,
// the seed selects the specific innervation.
func BuildGrammarLattice(
	g *Grammar,
	counts map[string]int,
	instanceSeed [32]byte,
	constraintFactory func(string) axiom.Constraint,
) *lattice.Lattice {
	l := lattice.New()

	// Sort tags for deterministic node creation order.
	tags := make([]string, 0, len(counts))
	for tag := range counts {
		if counts[tag] > 0 {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)

	// Create nodes grouped by tag.
	type tagGroup struct {
		tag   string
		nodes []*lattice.Node
	}
	groups := make([]tagGroup, 0, len(tags))
	groupIndex := make(map[string]int) // tag -> index in groups

	for _, tag := range tags {
		n := counts[tag]
		tg := tagGroup{tag: tag, nodes: make([]*lattice.Node, n)}
		for i := range n {
			node := l.AddNode([]axiom.Constraint{constraintFactory(tag)})
			node.SetEnergy(true)
			tg.nodes[i] = node
		}
		groupIndex[tag] = len(groups)
		groups = append(groups, tg)
	}

	// Intra-group connectivity: ring within each tag group.
	for _, tg := range groups {
		if len(tg.nodes) < 2 {
			continue
		}
		for i := range tg.nodes {
			l.Connect(tg.nodes[i], tg.nodes[(i+1)%len(tg.nodes)])
		}
	}

	// Inter-group connectivity: grammar-shaped cross-connections.
	// Seed a deterministic PRNG from the instance seed.
	var seed [32]byte
	copy(seed[:], instanceSeed[:])
	rng := rand.New(rand.NewChaCha8(seed))

	for i, tgA := range groups {
		for j := i + 1; j < len(groups); j++ {
			tgB := groups[j]

			// Check if grammar permits this pair (either direction).
			canAB := g.CanNeighbor(tgA.tag, tgB.tag)
			canBA := g.CanNeighbor(tgB.tag, tgA.tag)
			if !canAB && !canBA {
				continue
			}

			// Number of cross-connections: proportional to the smaller
			// group, with a minimum of 1. The factor (1/3) creates
			// sparse but meaningful bridging.
			smaller := min(len(tgA.nodes), len(tgB.nodes))
			nBridges := max(1, smaller/3)

			// Select which nodes to bridge using the seeded PRNG.
			// Different seeds select different bridge nodes —
			// same grammar shape, different wiring realization.
			for range nBridges {
				idxA := rng.IntN(len(tgA.nodes))
				idxB := rng.IntN(len(tgB.nodes))
				l.Connect(tgA.nodes[idxA], tgB.nodes[idxB])
			}
		}
	}

	return l
}
