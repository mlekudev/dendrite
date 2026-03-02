// Package grow implements the core growth loop: Brownian walk over the
// lattice, constraint capture, and typed bonding. This is the inner loop
// where elements from solution find their lattice sites.
//
// Each walker is a goroutine — cheap, numerous, uncoordinated. The lattice
// structure does the work, not the walkers.
package grow

import (
	"context"
	"runtime"
	"sync"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// WorkerCount returns the number of workers to use: NumCPU minus 25%
// headroom for GC, scheduler, and monitoring.
func WorkerCount() int {
	n := runtime.NumCPU()
	w := n - n/4
	if w < 1 {
		w = 1
	}
	return w
}

// Event records something that happened during growth.
type Event struct {
	Type    EventType
	NodeID  lattice.NodeID
	Element axiom.Element
	Steps   int // walk steps taken before outcome (0 = immediate)
}

// EventType classifies growth events.
type EventType int

const (
	EventBonded   EventType = iota // element bonded to a site
	EventRejected                  // element didn't fit anywhere
	EventExpired                   // walk exceeded step budget
)

// Config controls growth parameters.
type Config struct {
	// MaxSteps is the maximum number of walk steps before giving up.
	// This is temperature control — shorter walks mean faster but
	// noisier growth.
	MaxSteps int

	// Workers is the number of concurrent walkers.
	Workers int

	// BlockSize is the number of nodes per block for RunBlocked.
	// 0 means DefaultBlockSize (1024). Ignored by Run.
	BlockSize int

	// MaxRounds is the maximum number of spillover rounds for RunBlocked.
	// 0 means 100 (safety cap). Ignored by Run.
	MaxRounds int

	// MaxResidentBlocks is the maximum number of blocks held in memory
	// simultaneously. 0 means all blocks stay resident (no paging).
	// When set, blocks are demand-paged: the wavefront walk's spill
	// across a block boundary loads the target from disk and the source
	// block (now workless) gets stripped back to skeleton.
	MaxResidentBlocks int

	// BlockDir is the directory for block snapshot files. Required when
	// MaxResidentBlocks > 0. Each block is serialized as a separate file.
	BlockDir string

	// ConstraintFactory reconstructs axiom.Constraint objects from tag
	// strings during block thaw. Required when MaxResidentBlocks > 0.
	ConstraintFactory func(string) axiom.Constraint
}

// DefaultConfig returns reasonable defaults.
// Workers is NumCPU - NumCPU/4: leaves 25% headroom for GC and scheduler.
func DefaultConfig() Config {
	return Config{
		MaxSteps: 1000,
		Workers:  WorkerCount(),
	}
}

// Run starts the growth loop. It reads elements from the solution channel,
// launches walkers to find lattice sites, and reports events. It blocks
// until the context is cancelled or the solution channel is closed.
func Run(ctx context.Context, l *lattice.Lattice, solution <-chan axiom.Element, cfg Config, events chan<- Event) {
	var wg sync.WaitGroup

	// Worker pool — each worker is a Brownian walker.
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
					ev := walk(ctx, l, elem, cfg.MaxSteps)
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

// walk performs a single Brownian walk for one element, returning an event.
//
// Vascularization: the walker starts at a directed position (near a
// vacant site matching the element's type) rather than a random node.
// This is active transport — the bloodstream carrying molecules to
// receptor sites instead of relying on diffusion through tissue.
//
// Chemotaxis: at each step, the walker checks neighbors for compatible
// sites before taking a random step. It can "smell" compatible sites
// one hop away and moves toward regions with more vacant neighbors.
func walk(ctx context.Context, l *lattice.Lattice, elem axiom.Element, maxSteps int) Event {
	// Directed start: try to begin near a compatible vacant site.
	// This is the vascular system — routing to demand, not diffusing.
	current := l.VacantByTag(elem.Type())
	if current == nil {
		current = l.RandomNode()
	}
	if current == nil {
		return Event{Type: EventRejected, Element: elem}
	}

	for step := 0; step < maxSteps; step++ {
		// Check context.
		select {
		case <-ctx.Done():
			return Event{Type: EventExpired, Element: elem, Steps: step}
		default:
		}

		// Does this site admit the element?
		if current.Admits(elem) {
			if current.Bond(elem) {
				return Event{
					Type:    EventBonded,
					NodeID:  current.ID(),
					Element: elem,
					Steps:   step,
				}
			}
			// Bond failed (race — another walker got it). Keep walking.
		}

		// Chemotaxis: check neighbors before taking a random step.
		// Like a molecule following a concentration gradient toward
		// a receptor — if a compatible site is one hop away, go there.
		neighbors := current.Neighbors()
		if len(neighbors) > 0 {
			// Phase 1: immediate capture — any neighbor that admits?
			bonded := false
			for _, nb := range neighbors {
				if nb.Admits(elem) {
					if nb.Bond(elem) {
						return Event{
							Type:    EventBonded,
							NodeID:  nb.ID(),
							Element: elem,
							Steps:   step,
						}
					}
					bonded = true // race lost, but the site type is right
				}
			}

			// Phase 2: gradient following — prefer neighbors near
			// vacant space. Score each neighbor by how many of its
			// own neighbors are vacant. Move toward the highest score.
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

		// No gradient detected — fall back to random walk.
		next := lattice.RandomNeighbor(current)
		if next == nil {
			// Dead end — jump to a random node (long-range hop).
			next = l.RandomNode()
			if next == nil {
				return Event{Type: EventRejected, Element: elem}
			}
		}
		current = next
	}

	return Event{Type: EventExpired, Element: elem, Steps: maxSteps}
}
