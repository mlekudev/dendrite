package grow

import (
	"context"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// SeqProbe walks a trained (saturated) lattice sequentially, checking whether
// each element from the input stream matches an occupant reachable from the
// previous match position. This tests whether the input text follows the
// structural patterns encoded in the lattice topology.
//
// Unlike Probe (parallel, position-independent), SeqProbe maintains a cursor
// position in the lattice. Each new element searches outward from the cursor
// for a matching occupant. If found, the cursor moves there and a bonded event
// is emitted. If not found within MaxSteps, an expired event is emitted and
// the cursor jumps to a random matching occupant (if any exist) to resync.
//
// The lattice is never modified — this is purely read-only.
func SeqProbe(ctx context.Context, l *lattice.Lattice, solution <-chan axiom.Element, cfg Config, events chan<- Event) {
	var cursor *lattice.Node

	for {
		select {
		case <-ctx.Done():
			return
		case elem, ok := <-solution:
			if !ok {
				return
			}
			ev := seqProbeStep(l, cursor, elem, cfg.MaxSteps)
			if ev.Type == EventBonded {
				cursor = l.Node(ev.NodeID)
			} else {
				// Resync: find any matching occupant.
				cursor = findMatchingOccupant(l, elem.Type())
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}
}

// seqProbeStep searches outward from cursor for a node whose occupant
// matches the element's type. Returns a bonded event if found within
// MaxSteps, or expired if not.
func seqProbeStep(l *lattice.Lattice, cursor *lattice.Node, elem axiom.Element, maxSteps int) Event {
	tag := elem.Type()

	// If no cursor yet (first element), find any matching occupant.
	if cursor == nil {
		n := findMatchingOccupant(l, tag)
		if n != nil {
			return Event{Type: EventBonded, NodeID: n.ID(), Element: elem, Steps: 0}
		}
		return Event{Type: EventExpired, Element: elem, Steps: 0}
	}

	// BFS-like expansion from cursor: check cursor itself, then neighbors,
	// then neighbors of neighbors, etc. The walk distance (steps) measures
	// how far we had to go in the lattice topology to find a matching occupant.
	// Short distances = the input follows the lattice structure.
	// Long distances = the input deviates from trained patterns.

	visited := make(map[lattice.NodeID]bool)
	current := []*lattice.Node{cursor}

	for step := 0; step < maxSteps && len(current) > 0; step++ {
		var next []*lattice.Node
		for _, n := range current {
			if visited[n.ID()] {
				continue
			}
			visited[n.ID()] = true

			occ := n.Occupant()
			if occ != nil && occ.Type() == tag {
				return Event{
					Type:    EventBonded,
					NodeID:  n.ID(),
					Element: elem,
					Steps:   step,
				}
			}

			for _, nb := range n.Neighbors() {
				if !visited[nb.ID()] {
					next = append(next, nb)
				}
			}
		}
		current = next
	}

	return Event{Type: EventExpired, Element: elem, Steps: maxSteps}
}

// findMatchingOccupant scans the lattice for any node whose occupant
// matches the given type tag. Used for resyncing after a miss.
func findMatchingOccupant(l *lattice.Lattice, tag string) *lattice.Node {
	// Random start to avoid always landing on the same node.
	start := l.RandomNode()
	if start == nil {
		return nil
	}
	// Walk from random start looking for a match.
	current := start
	for i := 0; i < 100; i++ {
		occ := current.Occupant()
		if occ != nil && occ.Type() == tag {
			return current
		}
		next := lattice.RandomNeighbor(current)
		if next == nil {
			break
		}
		current = next
	}
	return nil
}
