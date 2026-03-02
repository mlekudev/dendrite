// Package dissolve implements continuous equilibrium evaluation and
// dissolution of weakly-bound lattice elements.
//
// Dissolution is the complement of accretion, not its opposite. Both
// operate simultaneously, driven by the same thermodynamic gradient.
// The lattice persists because accretion dominates dissolution — there
// is a net positive growth rate. But dissolution is continuous and
// essential: it removes impurities, prunes overextension, and corrects
// errors.
package dissolve

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// Event records a dissolution.
type Event struct {
	NodeID  lattice.NodeID
	Element axiom.Element
	LockIn  lattice.LockInDepth
	Reason  Reason
}

// Reason classifies why dissolution occurred.
type Reason int

const (
	ReasonWeakBond   Reason = iota // lock-in below threshold
	ReasonMetastable               // locally stable but globally inconsistent
	ReasonSenescent                // age reached maximum lifespan
	ReasonCrossLayer               // post-hoc: element bonded across layer boundary
)

// Config controls dissolution parameters.
type Config struct {
	// Threshold is the minimum lock-in depth to survive dissolution.
	// Elements with lock-in below this are released. This is now a
	// secondary criterion — the primary driver is half-life aging.
	Threshold lattice.LockInDepth

	// Interval is how often the dissolver scans the lattice.
	Interval time.Duration

	// MaxAge is the age at which elements are dissolved regardless of
	// lock-in depth (senescence). 0 means no age limit.
	// With 2-bit age encoding, the natural maximum is 3.
	MaxAge uint8

	// HalfLife controls substrate-blind dissolution probability.
	// Each scan, an element at age A has dissolution probability:
	//   P = A / (HalfLife + A)
	// At age == HalfLife, P = 0.5 (the element has a 50% chance
	// of dissolving on each scan). Higher HalfLife means elements
	// persist longer. Zero means half-life dissolution is disabled
	// (falls back to pure threshold mode for backward compatibility).
	//
	// Biology: hepatic enzymes clear caffeine at a rate determined
	// by the molecule's half-life, not by whether it was contributing
	// to useful alertness or to jitter. The clearance mechanism is
	// substrate-blind.
	HalfLife uint8
}

// DefaultConfig returns biologically-aligned defaults — half-life driven
// dissolution with contextual threshold as secondary criterion.
func DefaultConfig() Config {
	return Config{
		Threshold: ratio.Half,
		Interval:  100 * time.Millisecond,
		HalfLife:  2, // P=0.5 at age 2 (Sustain phase)
	}
}

// Run starts the continuous dissolution loop. Dissolved elements are sent
// to the returned channel (they return to solution). The events channel
// receives dissolution records. Blocks until context is cancelled.
func Run(ctx context.Context, l *lattice.Lattice, cfg Config, dissolved chan<- axiom.Element, events chan<- Event) {
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan(l, cfg, dissolved, events)
		}
	}
}

// scan evaluates all occupied nodes and dissolves those below threshold.
// Sticky elements (implementing axiom.StickyElement with IsSticky() == true)
// are immune to dissolution regardless of lock-in depth.
func scan(l *lattice.Lattice, cfg Config, dissolved chan<- axiom.Element, events chan<- Event) {
	for _, n := range l.Nodes() {
		if !n.Occupied() {
			continue
		}

		// Sticky elements survive dissolution unconditionally.
		if sticky, ok := n.Occupant().(axiom.StickyElement); ok && sticky.IsSticky() {
			continue
		}

		// Post-hoc cross-layer detection: biology detects cross-system
		// bonds through symptoms (tremor, euphoria) and corrects by
		// dissolution (metabolic clearance). Cross-layer bonds already
		// have reduced lock-in from Bond(); here they also get
		// preferential dissolution when they're weak.
		if n.CrossLayer() {
			lockIn := n.ContextualLockIn()
			// Cross-layer bonds dissolve at a lower threshold than
			// same-layer bonds — they need stronger neighborhood
			// support to survive. Halve the effective threshold.
			crossThreshold := cfg.Threshold.Mul(ratio.New(1, 2))
			if lockIn.Less(crossThreshold) {
				elem := n.Dissolve()
				if elem == nil {
					continue
				}
				l.ReindexVacant(n)
				select {
				case dissolved <- elem:
				default:
				}
				select {
				case events <- Event{
					NodeID:  n.ID(),
					Element: elem,
					LockIn:  lockIn,
					Reason:  ReasonCrossLayer,
				}:
				default:
				}
				continue
			}
		}

		// Half-life dissolution: substrate-blind probabilistic clearance.
		// Biology: hepatic enzymes don't distinguish "useful caffeine"
		// from "jittery caffeine." Clearance probability increases with
		// age: P = age / (halfLife + age). At age == halfLife, P = 0.5.
		if cfg.HalfLife > 0 {
			age := n.Age()
			hl := cfg.HalfLife
			// P = age / (halfLife + age). Integer comparison is exact:
			// rand.IntN(hl+age) < age has probability age/(hl+age).
			if rand.IntN(int(hl+age)) < int(age) {
				lockIn := n.ContextualLockIn()
				elem := n.Dissolve()
				if elem == nil {
					continue
				}
				l.ReindexVacant(n)
				select {
				case dissolved <- elem:
				default:
				}
				select {
				case events <- Event{
					NodeID:  n.ID(),
					Element: elem,
					LockIn:  lockIn,
					Reason:  ReasonSenescent, // half-life is a form of senescence
				}:
				default:
				}
				continue
			}
		}

		// Senescence: hard age limit — dissolve regardless.
		if cfg.MaxAge > 0 && n.Age() >= cfg.MaxAge {
			elem := n.Dissolve()
			if elem == nil {
				continue
			}
			l.ReindexVacant(n)
			select {
			case dissolved <- elem:
			default:
			}
			select {
			case events <- Event{
				NodeID:  n.ID(),
				Element: elem,
				LockIn:  n.ContextualLockIn(),
				Reason:  ReasonSenescent,
			}:
			default:
			}
			continue
		}

		// Use contextual lock-in: accounts for neighborhood occupancy.
		// Isolated elements (few occupied neighbors) have reduced lock-in.
		lockIn := n.ContextualLockIn()
		if !lockIn.Less(cfg.Threshold) {
			continue
		}

		// Below threshold — dissolve.
		elem := n.Dissolve()
		if elem == nil {
			continue // race — someone else dissolved it
		}
		l.ReindexVacant(n)

		// Return element to solution.
		select {
		case dissolved <- elem:
		default:
			// Solution channel full — element is lost.
			// This is natural: not everything gets recycled.
		}

		// Report the event.
		select {
		case events <- Event{
			NodeID:  n.ID(),
			Element: elem,
			LockIn:  lockIn,
			Reason:  ReasonWeakBond,
		}:
		default:
		}
	}
}

// ScanOnce runs a single dissolution pass. Useful for testing and
// for explicit annealing steps.
func ScanOnce(l *lattice.Lattice, cfg Config, dissolved chan<- axiom.Element, events chan<- Event) {
	scan(l, cfg, dissolved, events)
}
