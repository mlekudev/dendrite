package grammar

import (
	"time"

	"github.com/mlekudev/dendrite/pkg/dissolve"
	"github.com/mlekudev/dendrite/pkg/grow"
	"github.com/mlekudev/dendrite/pkg/memory"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// VagusBaseline holds the default parameters that the vagus pathway modulates.
// These are the resting-state values — the parameters when no metabolic
// feedback is present (generation 0, or nil digest).
type VagusBaseline struct {
	DissolveThreshold ratio.Ratio
	DissolveHalfLife  uint8
	GrowMaxSteps      int
	GrowWorkers       int
}

// DefaultBaseline returns a reasonable resting-state configuration.
func DefaultBaseline() VagusBaseline {
	return VagusBaseline{
		DissolveThreshold: ratio.Half,
		DissolveHalfLife:  2,
		GrowMaxSteps:      500,
		GrowWorkers:       3,
	}
}

// VagusSignal carries metabolic feedback from memory to growth parameters.
// This is the rigid feedback pathway — deterministic, not hash-perturbed.
// Named after the vagus nerve: the longest cranial nerve, carrying
// parasympathetic signals from organs to brainstem.
type VagusSignal struct {
	DissolveThreshold ratio.Ratio
	DissolveHalfLife  uint8
	GrowMaxSteps      int
	GrowWorkers       int
	TypeAdjustments   map[string]int // -1 = shrink allocation, +1 = grow allocation
}

// DefaultSignal returns a VagusSignal with baseline parameters and no
// type adjustments. Used when no metabolic feedback is available.
func DefaultSignal() VagusSignal {
	b := DefaultBaseline()
	return VagusSignal{
		DissolveThreshold: b.DissolveThreshold,
		DissolveHalfLife:  b.DissolveHalfLife,
		GrowMaxSteps:      b.GrowMaxSteps,
		GrowWorkers:       b.GrowWorkers,
		TypeAdjustments:   make(map[string]int),
	}
}

// ReadVagus translates a memory.Digest into direct parameter modulations.
// This replaces the hash-based perturbation with a rigid feedback pathway:
// metabolic signals → parameter adjustments, deterministic and immediate.
//
// The threshold multipliers (7/10, 8/10, 13/10) are decimal-denominated
// fractions applied to binary-structured lattice dynamics. This is an
// intentional asymmetry: the vagus pathway measures in decimal (human-
// readable percentages) and actuates in binary (lattice topology). The
// phase drift between these domains is bounded by the epoch relationship
// 10^a × 2^b (see package epoch).
//
// Translation rules (biology analogs):
//   - Fitness falling → more aggressive dissolution (clear failing structure)
//   - Fitness rising → preserve what's working (raise dissolution threshold)
//   - Fitness stagnant → increase exploration (more walkers, longer walks)
//   - Occupancy falling → increase growth effort (double max steps)
//   - High sustain fraction → increase turnover (lower half-life)
//   - High young fraction → reduce churn (raise half-life)
//   - Per-tag bond rate → type allocation adjustment
func ReadVagus(d *memory.Digest, baseline VagusBaseline) VagusSignal {
	sig := VagusSignal{
		DissolveThreshold: baseline.DissolveThreshold,
		DissolveHalfLife:  baseline.DissolveHalfLife,
		GrowMaxSteps:      baseline.GrowMaxSteps,
		GrowWorkers:       baseline.GrowWorkers,
		TypeAdjustments:   make(map[string]int),
	}

	if d == nil {
		return sig
	}

	// Fitness-driven modulation.
	switch d.FitnessTrend {
	case memory.TrendFalling:
		// Failing — clear weak structure more aggressively.
		sig.DissolveThreshold = baseline.DissolveThreshold.Mul(ratio.New(7, 10))
		if sig.DissolveHalfLife > 1 {
			sig.DissolveHalfLife--
		}
	case memory.TrendRising:
		// Improving — preserve what's working.
		sig.DissolveThreshold = baseline.DissolveThreshold.Mul(ratio.New(13, 10))
	case memory.TrendStagnant:
		// Stuck — explore more.
		sig.GrowMaxSteps = int(ratio.New(3, 2).ScaleInt(int64(baseline.GrowMaxSteps)))
		sig.GrowWorkers = baseline.GrowWorkers + 1
	}

	// Occupancy-driven modulation.
	if d.OccupancyTrend == memory.TrendFalling {
		sig.GrowMaxSteps = max(sig.GrowMaxSteps, int(ratio.New(2, 1).ScaleInt(int64(baseline.GrowMaxSteps))))
		if sig.DissolveHalfLife > 1 {
			sig.DissolveHalfLife--
		}
	}

	// OverExtended: occupancy falling + explore dominant.
	// Suppress exploration, increase dissolution.
	if d.OverExtended {
		sig.GrowMaxSteps = int(ratio.New(1, 2).ScaleInt(int64(baseline.GrowMaxSteps)))
		if sig.GrowWorkers > 2 {
			sig.GrowWorkers--
		}
	}

	// ADSR homeostasis.
	// High sustain fraction = too rigid, needs turnover.
	if !d.SustainFraction.IsZero() && ratio.New(8, 10).Less(d.SustainFraction) {
		if sig.DissolveHalfLife > 1 {
			sig.DissolveHalfLife--
		}
	}
	// High young fraction = too much churn, stabilize.
	if !d.YoungFraction.IsZero() && ratio.New(7, 10).Less(d.YoungFraction) {
		sig.DissolveHalfLife++
	}

	// Per-tag bond rate adjustments.
	// Absorbs the logic from rebalanceTypes() in cmd/dendrite/main.go.
	for tag, td := range d.Types {
		if td.BondRate.Less(ratio.New(1, 10)) {
			sig.TypeAdjustments[tag] = -1 // bond rate < 10% → shrink
		} else if ratio.Half.Less(td.BondRate) {
			sig.TypeAdjustments[tag] = 1 // bond rate > 50% → grow
		}
	}

	return sig
}

// DissolveConfig returns a dissolve.Config with vagus-modulated parameters.
func (v VagusSignal) DissolveConfig(interval time.Duration) dissolve.Config {
	return dissolve.Config{
		Threshold: v.DissolveThreshold,
		Interval:  interval,
		HalfLife:  v.DissolveHalfLife,
	}
}

// GrowConfig returns a grow.Config with vagus-modulated parameters.
func (v VagusSignal) GrowConfig() grow.Config {
	return grow.Config{
		MaxSteps: v.GrowMaxSteps,
		Workers:  v.GrowWorkers,
	}
}

// AdjustCounts applies type adjustments to a base count map.
// Shrink (-1) halves the count (minimum 1).
// Grow (+1) increases by 50%.
func (v VagusSignal) AdjustCounts(base map[string]int) map[string]int {
	result := make(map[string]int, len(base))
	for tag, count := range base {
		adj, ok := v.TypeAdjustments[tag]
		if !ok {
			result[tag] = count
			continue
		}
		switch {
		case adj < 0:
			result[tag] = max(1, int(ratio.New(1, 2).ScaleInt(int64(count))))
		case adj > 0:
			result[tag] = int(ratio.New(3, 2).ScaleInt(int64(count)))
		default:
			result[tag] = count
		}
	}
	return result
}
