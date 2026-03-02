package grow

import (
	"context"
	"sync"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// ProbeEvent records what happened when an element was probed against a
// trained lattice. Unlike growth events which bond elements to empty sites,
// probe events measure how well the element fits the existing occupants.
type ProbeEvent struct {
	// Type: EventBonded means a matching occupant was found (type match
	// at a constrained site). EventExpired means no match within MaxSteps.
	Type EventType

	// NodeID is the node where a match was found (if matched).
	NodeID lattice.NodeID

	// Element is the probed element.
	Element axiom.Element

	// Steps is how many walk steps before finding a match (or expiring).
	Steps int
}

// Probe walks elements through a trained (saturated) lattice without bonding.
// For each element, it walks the lattice and checks if visited nodes have
// occupants matching the element's type. A match means the lattice has
// "seen" this kind of element at this position — the text fits the trained
// topology.
//
// This is the inference counterpart to Run: Run grows the lattice,
// Probe reads it.
func Probe(ctx context.Context, l *lattice.Lattice, solution <-chan axiom.Element, cfg Config, events chan<- ProbeEvent) {
	var wg sync.WaitGroup

	for range cfg.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case elem, ok := <-solution:
					if !ok {
						return
					}
					ev := probeWalk(ctx, l, elem, cfg.MaxSteps)
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	wg.Wait()
}

// probeWalk walks the lattice checking for type-matching occupants.
// It uses the same directed start and chemotaxis as growth walks,
// but instead of bonding, it checks occupant compatibility.
func probeWalk(ctx context.Context, l *lattice.Lattice, elem axiom.Element, maxSteps int) ProbeEvent {
	// Start at a random node (can't use VacantByTag — lattice is full).
	current := l.RandomNode()
	if current == nil {
		return ProbeEvent{Type: EventExpired, Element: elem}
	}

	tag := elem.Type()

	for step := 0; step < maxSteps; step++ {
		select {
		case <-ctx.Done():
			return ProbeEvent{Type: EventExpired, Element: elem, Steps: step}
		default:
		}

		// Check if this node's occupant matches the element's type.
		occ := current.Occupant()
		if occ != nil && occ.Type() == tag {
			return ProbeEvent{
				Type:    EventBonded, // "matched" — reuse the type
				NodeID:  current.ID(),
				Element: elem,
				Steps:   step,
			}
		}

		// Walk toward matching occupants via neighbor checking.
		neighbors := current.Neighbors()
		if len(neighbors) == 0 {
			break
		}

		// Check neighbors for immediate match.
		for _, nb := range neighbors {
			occ := nb.Occupant()
			if occ != nil && occ.Type() == tag {
				return ProbeEvent{
					Type:    EventBonded,
					NodeID:  nb.ID(),
					Element: elem,
					Steps:   step,
				}
			}
		}

		// No immediate match — step to a random neighbor.
		next := lattice.RandomNeighbor(current)
		if next == nil {
			break
		}
		current = next
	}

	return ProbeEvent{Type: EventExpired, Element: elem, Steps: maxSteps}
}
