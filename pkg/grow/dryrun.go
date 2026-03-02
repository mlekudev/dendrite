package grow

import (
	"context"
	"sync"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// DryRun is like Run but bonds are immediately reversed after recording.
// Each element is walked through the lattice using the same directed start,
// chemotaxis, and constraint checking as Run. If a bond forms, the event is
// emitted and the node is immediately dissolved so the lattice never saturates.
//
// This gives a "would this bond?" test for every token without capacity
// exhaustion. The trained lattice topology and constraints are the detector;
// the occupancy state doesn't accumulate.
func DryRun(ctx context.Context, l *lattice.Lattice, solution <-chan axiom.Element, cfg Config, events chan<- Event) {
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
					ev := dryWalk(ctx, l, elem, cfg.MaxSteps)
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

// dryWalk performs a single Brownian walk that bonds and immediately unbonds.
// Uses the same vascularization and chemotaxis as walk() but dissolves the
// bonded element after recording the event, keeping the lattice unsaturated.
func dryWalk(ctx context.Context, l *lattice.Lattice, elem axiom.Element, maxSteps int) Event {
	// Directed start: try to begin near a compatible vacant site.
	current := l.VacantByTag(elem.Type())
	if current == nil {
		current = l.RandomNode()
	}
	if current == nil {
		return Event{Type: EventRejected, Element: elem}
	}

	for step := 0; step < maxSteps; step++ {
		select {
		case <-ctx.Done():
			return Event{Type: EventExpired, Element: elem, Steps: step}
		default:
		}

		// Does this site admit the element?
		if current.Admits(elem) {
			if current.Bond(elem) {
				ev := Event{
					Type:    EventBonded,
					NodeID:  current.ID(),
					Element: elem,
					Steps:   step,
				}
				// Immediately unbond so lattice stays unsaturated.
				current.Dissolve()
				l.ReindexVacant(current)
				return ev
			}
		}

		// Chemotaxis: check neighbors for immediate bond opportunity.
		neighbors := current.Neighbors()
		if len(neighbors) > 0 {
			bonded := false
			for _, nb := range neighbors {
				if nb.Admits(elem) {
					if nb.Bond(elem) {
						ev := Event{
							Type:    EventBonded,
							NodeID:  nb.ID(),
							Element: elem,
							Steps:   step,
						}
						nb.Dissolve()
						l.ReindexVacant(nb)
						return ev
					}
					bonded = true
				}
			}

			// Gradient following toward vacant space.
			if !bonded {
				bestScore := -1
				var best *lattice.Node
				for _, nb := range neighbors {
					score := 0
					for _, nnb := range nb.Neighbors() {
						if !nnb.Occupied() {
							score++
						}
					}
					if score > bestScore {
						bestScore = score
						best = nb
					}
				}
				if best != nil && bestScore > 0 {
					current = best
					continue
				}
			}
		}

		// Random walk fallback.
		next := lattice.RandomNeighbor(current)
		if next == nil {
			next = l.RandomNode()
			if next == nil {
				return Event{Type: EventRejected, Element: elem}
			}
		}
		current = next
	}

	return Event{Type: EventExpired, Element: elem, Steps: maxSteps}
}
