package memory

import "github.com/mlekudev/dendrite/pkg/ratio"

// Trend describes the direction of a metric across generations.
type Trend int

const (
	TrendFlat     Trend = iota // no significant change
	TrendRising                // consistently increasing
	TrendFalling               // consistently decreasing
	TrendStagnant              // flat for 3+ data points
)

// String returns a human-readable trend name.
func (t Trend) String() string {
	switch t {
	case TrendRising:
		return "rising"
	case TrendFalling:
		return "falling"
	case TrendStagnant:
		return "stagnant"
	default:
		return "flat"
	}
}

// Digest summarizes cross-generational patterns from memory.
// Produced by walking the graph: typ→bnd→fit per tag, plus health and hex ops.
type Digest struct {
	// Per-type analysis: which types are signal, which are noise.
	Types map[string]TypeDigest

	// Lattice-wide signals.
	OccupancyTrend  Trend       // rising, falling, flat
	FitnessTrend    Trend       // rising, falling, flat, stagnant
	ExploreRatio    ratio.Ratio // explore_ops / total_ops (last gen)
	OverExtended    bool        // true if occupancy falling AND explore dominant
	GenerationsSeen int         // how many gens of data we have

	// ADSR signals (from most recent generation with data).
	SustainFraction ratio.Ratio // sustain / occupied
	YoungFraction   ratio.Ratio // (attack + decay) / occupied
}

// TypeDigest is the per-type analysis from the graph walk.
type TypeDigest struct {
	Tag          string
	BondRate     ratio.Ratio // bonds / allocated sites (last gen with data)
	MissingDelta int         // change in missing count between last two gens (negative = improving)
}

// Hexagram operation byte constants (matching hexagram.Op enum).
const (
	opNone      byte = 0
	opAccrete   byte = 1
	opDissolve  byte = 2
	opNucleate  byte = 3
	opPrune     byte = 4
	opStrengthen byte = 5
	opExplore   byte = 6
	opCollapse  byte = 7
	opRecycle   byte = 8
)

// WalkDigest produces a Digest by walking the memory graph.
// tags is the list of type tags to analyze (typically from parent spore's TypeSignature).
// lastN is how many recent generations to consider.
// Returns nil if fewer than 2 generations of data exist.
func (d *DB) WalkDigest(tags []string, lastN int) *Digest {
	if lastN < 2 {
		lastN = 2
	}

	// 1. Health history → occupancy trend.
	health := d.QueryHealthHistory(lastN)
	if len(health) < 2 {
		return nil // need at least 2 cycles
	}

	dig := &Digest{
		Types:           make(map[string]TypeDigest),
		GenerationsSeen: len(health),
	}

	// Compute occupancy trend from health snapshots.
	dig.OccupancyTrend = computeOccupancyTrend(health)

	// 2. Fitness trajectory → fitness trend.
	fitness := d.QueryFitnessTrajectory(lastN)
	dig.FitnessTrend = computeFitnessTrend(fitness)

	// 3. Per-tag analysis: walk typ→bnd→mis for each tag.
	for _, tag := range tags {
		td := TypeDigest{Tag: tag}

		// Type counts across generations.
		typTrend := d.QueryTypeTrend(tag, lastN)

		// Bond counts across generations.
		bndHist := d.QueryBondHistory(tag, lastN)

		// Compute BondRate from the most recent generation where both exist.
		td.BondRate = computeBondRate(typTrend, bndHist)

		// Missing site trend.
		misTrend := d.QueryMissingTrend(tag, lastN)
		td.MissingDelta = computeMissingDelta(misTrend)

		dig.Types[tag] = td
	}

	// 4. Hexagram ops → explore ratio (from most recent generation).
	dig.ExploreRatio = computeExploreRatio(d, lastN)

	// 5. OverExtended if either:
	//    (a) occupancy falling AND explore ratio > 80%, or
	//    (b) occupancy falling significantly (occupancy rate halved or worse).
	exploreHeavy := ratio.New(4, 5).Less(dig.ExploreRatio)
	occupancyCollapse := false
	if len(health) >= 2 {
		first := ratio.New(int64(health[0].Occupied), int64(health[0].Total))
		last := ratio.New(int64(health[len(health)-1].Occupied), int64(health[len(health)-1].Total))
		// Occupancy rate halved or worse.
		if !first.IsZero() && last.Less(first.Div(ratio.New(2, 1))) {
			occupancyCollapse = true
		}
	}
	dig.OverExtended = dig.OccupancyTrend == TrendFalling &&
		(exploreHeavy || occupancyCollapse)

	// 6. ADSR distribution from most recent generation.
	adsr := d.QueryADSRHistory(1)
	if len(adsr) > 0 {
		total := adsr[0].Counts[0] + adsr[0].Counts[1] + adsr[0].Counts[2] + adsr[0].Counts[3]
		if total > 0 {
			dig.SustainFraction = ratio.New(int64(adsr[0].Counts[2]), int64(total))
			dig.YoungFraction = ratio.New(int64(adsr[0].Counts[0]+adsr[0].Counts[1]), int64(total))
		}
	}

	return dig
}

// computeOccupancyTrend determines whether occupancy is rising, falling, or flat.
// Uses the occupancy rate (occupied/total) across health snapshots.
func computeOccupancyTrend(health []HealthPoint) Trend {
	if len(health) < 2 {
		return TrendFlat
	}
	rising := 0
	falling := 0
	for i := 1; i < len(health); i++ {
		prev := ratio.New(int64(health[i-1].Occupied), int64(health[i-1].Total))
		curr := ratio.New(int64(health[i].Occupied), int64(health[i].Total))
		if prev.Less(curr) {
			rising++
		} else if curr.Less(prev) {
			falling++
		}
	}
	transitions := len(health) - 1
	if falling > transitions/2 {
		return TrendFalling
	}
	if rising > transitions/2 {
		return TrendRising
	}
	// Flat for 3+ points = stagnant.
	if transitions >= 3 && rising == 0 && falling == 0 {
		return TrendStagnant
	}
	return TrendFlat
}

// computeFitnessTrend determines whether fitness is rising, falling, flat, or stagnant.
func computeFitnessTrend(fitness []FitnessPoint) Trend {
	if len(fitness) < 2 {
		return TrendFlat
	}
	rising := 0
	falling := 0
	for i := 1; i < len(fitness); i++ {
		if fitness[i-1].Score.Less(fitness[i].Score) {
			rising++
		} else if fitness[i].Score.Less(fitness[i-1].Score) {
			falling++
		}
	}
	transitions := len(fitness) - 1
	if falling > transitions/2 {
		return TrendFalling
	}
	if rising > transitions/2 {
		return TrendRising
	}
	if transitions >= 3 && rising == 0 && falling == 0 {
		return TrendStagnant
	}
	return TrendFlat
}

// computeBondRate computes bonds/allocated_sites for the most recent generation
// where both type signature and bond data exist.
func computeBondRate(typ []TypePoint, bnd []TypePoint) ratio.Ratio {
	if len(typ) == 0 || len(bnd) == 0 {
		return ratio.Zero
	}
	// Build gen→count maps.
	typMap := make(map[uint32]uint32)
	for _, p := range typ {
		typMap[p.Gen] = p.Count
	}
	bndMap := make(map[uint32]uint32)
	for _, p := range bnd {
		bndMap[p.Gen] = p.Count
	}
	// Find most recent gen with both.
	for i := len(typ) - 1; i >= 0; i-- {
		gen := typ[i].Gen
		tc := typMap[gen]
		bc := bndMap[gen]
		if tc > 0 {
			return ratio.New(int64(bc), int64(tc))
		}
	}
	return ratio.Zero
}

// computeMissingDelta returns the change in missing count between the two most
// recent generations. Negative means improving (fewer missing sites).
func computeMissingDelta(mis []MissingPoint) int {
	if len(mis) < 2 {
		return 0
	}
	prev := mis[len(mis)-2].Count
	curr := mis[len(mis)-1].Count
	return int(curr) - int(prev)
}

// computeExploreRatio computes explore_ops / total_ops from the most recent
// generation's hexagram operation counts.
func computeExploreRatio(d *DB, lastN int) ratio.Ratio {
	// Query each op type for 1 generation (the most recent).
	var exploreCount uint32
	var totalCount uint32

	for op := opNone; op <= opRecycle; op++ {
		pts := d.QueryHexagramOps(op, 1)
		if len(pts) > 0 {
			totalCount += pts[0].Count
			if op == opExplore || op == opNucleate {
				exploreCount += pts[0].Count
			}
		}
	}

	if totalCount == 0 {
		return ratio.Zero
	}
	return ratio.New(int64(exploreCount), int64(totalCount))
}
