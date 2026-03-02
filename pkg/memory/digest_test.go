package memory

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/mlekudev/dendrite/pkg/spore"
)

func TestWalkDigestInsufficientData(t *testing.T) {
	db := tmpDB(t)

	// No data at all.
	if dig := db.WalkDigest([]string{"func"}, 5); dig != nil {
		t.Fatal("expected nil digest with no data")
	}

	// One generation only — need at least 2.
	db.RecordHealth(0, 100, 256, ratio.Half)
	if dig := db.WalkDigest([]string{"func"}, 5); dig != nil {
		t.Fatal("expected nil digest with 1 gen")
	}
}

func TestWalkDigestOverExtended(t *testing.T) {
	db := tmpDB(t)

	// Simulate the noise spiral: occupancy falling, explore dominant.
	for gen := uint32(0); gen < 4; gen++ {
		// Occupancy drops each gen: 50%, 40%, 30%, 20%.
		occupied := 100 - gen*20
		total := uint32(200 + gen*100)
		db.RecordHealth(gen, occupied, total, ratio.One)

		// Fitness flat and low.
		db.RecordFitness(gen, ratio.New(1, 100), ratio.Zero, ratio.Zero, ratio.New(1, 100))

		// Type signatures: func has many sites but few bonds.
		db.RecordTypeSig(gen, []spore.TagCount{
			{Tag: "func", Count: int(200 + gen*50)},
			{Tag: "literal", Count: int(100 + gen*20)},
		})

		// Bonds: very few.
		bonds := []BondRecord{
			{Tag: "func", SiteID: gen*10 + 1},
			{Tag: "func", SiteID: gen*10 + 2},
			{Tag: "literal", SiteID: gen*10 + 3},
		}
		db.RecordBonds(gen, bonds)

		// Missing: lots and rising.
		db.RecordMissing(gen, []spore.TagCount{
			{Tag: "func", Count: int(500 + gen*100)},
		})

		// Hexagram: mostly explore + nucleate, very little else.
		ops := map[byte]uint32{
			opExplore:    1000 + gen*500,
			opNucleate:   200 + gen*50,
			opStrengthen: 5,
			opAccrete:    10,
			opPrune:      3,
		}
		db.RecordHexagramOps(gen, ops)
	}

	dig := db.WalkDigest([]string{"func", "literal"}, 5)
	if dig == nil {
		t.Fatal("expected non-nil digest")
	}

	// Should detect overextension.
	if !dig.OverExtended {
		t.Fatalf("expected OverExtended=true, occupancy=%s explore=%.2f",
			dig.OccupancyTrend, dig.ExploreRatio.Float64())
	}
	if dig.OccupancyTrend != TrendFalling {
		t.Fatalf("expected TrendFalling, got %s", dig.OccupancyTrend)
	}
	if dig.GenerationsSeen != 4 {
		t.Fatalf("expected 4 gens, got %d", dig.GenerationsSeen)
	}

	// Explore ratio should be high (explore+nucleate >> others).
	if dig.ExploreRatio.Less(ratio.New(4, 5)) {
		t.Fatalf("expected explore ratio > 0.8, got %.3f", dig.ExploreRatio.Float64())
	}

	// func: allocated 350 sites in gen3, bonded 2 → bond rate ~0.006.
	funcDig, ok := dig.Types["func"]
	if !ok {
		t.Fatal("missing func digest")
	}
	if ratio.New(1, 10).Less(funcDig.BondRate) {
		t.Fatalf("expected func bond rate < 0.1, got %.4f", funcDig.BondRate.Float64())
	}

	// Missing delta should be positive (rising = worsening).
	if funcDig.MissingDelta <= 0 {
		t.Fatalf("expected positive missing delta, got %d", funcDig.MissingDelta)
	}
}

func TestWalkDigestHealthyLattice(t *testing.T) {
	db := tmpDB(t)

	// Simulate a healthy lattice: occupancy rising, fitness rising, low explore.
	for gen := uint32(0); gen < 4; gen++ {
		// Occupancy rises: 20%, 30%, 40%, 50%.
		occupied := 40 + gen*20
		total := uint32(200)
		db.RecordHealth(gen, occupied, total, ratio.One)

		// Fitness rising.
		db.RecordFitness(gen, ratio.New(int64(gen+1), 10), ratio.Zero, ratio.Zero, ratio.New(int64(gen+1), 10))

		// Good bond rates.
		db.RecordTypeSig(gen, []spore.TagCount{
			{Tag: "func", Count: 50},
		})
		bonds := make([]BondRecord, 30)
		for i := range bonds {
			bonds[i] = BondRecord{Tag: "func", SiteID: gen*100 + uint32(i)}
		}
		db.RecordBonds(gen, bonds)

		// Low explore.
		ops := map[byte]uint32{
			opExplore:    10,
			opStrengthen: 500,
			opAccrete:    200,
			opPrune:      50,
		}
		db.RecordHexagramOps(gen, ops)
	}

	dig := db.WalkDigest([]string{"func"}, 5)
	if dig == nil {
		t.Fatal("expected non-nil digest")
	}

	if dig.OverExtended {
		t.Fatal("should not be overextended")
	}
	if dig.OccupancyTrend != TrendRising {
		t.Fatalf("expected TrendRising, got %s", dig.OccupancyTrend)
	}
	if dig.FitnessTrend != TrendRising {
		t.Fatalf("expected TrendRising, got %s", dig.FitnessTrend)
	}

	// func bond rate: 30/50 = 0.6.
	funcDig := dig.Types["func"]
	if funcDig.BondRate.Less(ratio.Half) {
		t.Fatalf("expected func bond rate > 0.5, got %.3f", funcDig.BondRate.Float64())
	}

	// Explore ratio should be low.
	if ratio.New(1, 5).Less(dig.ExploreRatio) {
		t.Fatalf("expected explore ratio < 0.2, got %.3f", dig.ExploreRatio.Float64())
	}
}

func TestWalkDigestStagnantFitness(t *testing.T) {
	db := tmpDB(t)

	// Same fitness for 5 generations.
	for gen := uint32(0); gen < 5; gen++ {
		db.RecordHealth(gen, 100, 200, ratio.One)
		db.RecordFitness(gen, ratio.New(1, 10), ratio.Zero, ratio.Zero, ratio.New(1, 10))
		db.RecordHexagramOps(gen, map[byte]uint32{opStrengthen: 100})
	}

	dig := db.WalkDigest(nil, 5)
	if dig == nil {
		t.Fatal("expected non-nil digest")
	}
	if dig.FitnessTrend != TrendStagnant {
		t.Fatalf("expected TrendStagnant, got %s", dig.FitnessTrend)
	}
}

func TestWalkDigestOccupancyCollapse(t *testing.T) {
	db := tmpDB(t)

	// Occupancy rate halves: 50% → 20% — should trigger OverExtended
	// even with low explore ratio.
	for gen := uint32(0); gen < 3; gen++ {
		occupied := 100 - gen*30 // 100, 70, 40
		total := uint32(200)
		db.RecordHealth(gen, occupied, total, ratio.One)
		db.RecordFitness(gen, ratio.New(1, 10), ratio.Zero, ratio.Zero, ratio.New(1, 10))
		// Low explore ratio — mostly strengthen.
		db.RecordHexagramOps(gen, map[byte]uint32{
			opStrengthen: 1000,
			opExplore:    10,
		})
	}

	dig := db.WalkDigest(nil, 5)
	if dig == nil {
		t.Fatal("expected non-nil digest")
	}
	if !dig.OverExtended {
		t.Fatalf("expected OverExtended=true (occupancy collapse), occupancy=%s explore=%.2f",
			dig.OccupancyTrend, dig.ExploreRatio.Float64())
	}
}

func TestComputeBondRate(t *testing.T) {
	// No data.
	if r := computeBondRate(nil, nil); !r.IsZero() {
		t.Fatal("expected zero")
	}

	// Gen 0: 100 allocated, 30 bonded = 0.3.
	typ := []TypePoint{{Gen: 0, Count: 100}}
	bnd := []TypePoint{{Gen: 0, Count: 30}}
	r := computeBondRate(typ, bnd)
	if r.Num != 3 || r.Denom != 10 {
		t.Fatalf("expected 3/10, got %d/%d", r.Num, r.Denom)
	}

	// Multiple gens: should use most recent.
	typ = []TypePoint{{Gen: 0, Count: 100}, {Gen: 1, Count: 200}}
	bnd = []TypePoint{{Gen: 0, Count: 30}, {Gen: 1, Count: 120}}
	r = computeBondRate(typ, bnd)
	// Gen 1: 120/200 = 3/5.
	if r.Num != 3 || r.Denom != 5 {
		t.Fatalf("expected 3/5, got %d/%d", r.Num, r.Denom)
	}
}

func TestComputeMissingDelta(t *testing.T) {
	// No data.
	if d := computeMissingDelta(nil); d != 0 {
		t.Fatal("expected 0")
	}

	// Rising: 100 → 150 = +50.
	mis := []MissingPoint{{Gen: 0, Count: 100}, {Gen: 1, Count: 150}}
	if d := computeMissingDelta(mis); d != 50 {
		t.Fatalf("expected 50, got %d", d)
	}

	// Falling: 200 → 180 = -20.
	mis = []MissingPoint{{Gen: 0, Count: 200}, {Gen: 1, Count: 180}}
	if d := computeMissingDelta(mis); d != -20 {
		t.Fatalf("expected -20, got %d", d)
	}
}

func TestTrendString(t *testing.T) {
	cases := []struct {
		trend Trend
		want  string
	}{
		{TrendFlat, "flat"},
		{TrendRising, "rising"},
		{TrendFalling, "falling"},
		{TrendStagnant, "stagnant"},
	}
	for _, c := range cases {
		if got := c.trend.String(); got != c.want {
			t.Errorf("Trend(%d).String() = %q, want %q", c.trend, got, c.want)
		}
	}
}
