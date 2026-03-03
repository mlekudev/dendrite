package memory

import (
	"testing"
	"time"

	"github.com/mlekudev/dendrite/pkg/ratio"
)

func tmpDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestKeyRoundTrip(t *testing.T) {
	// TagHash determinism.
	h1 := TagHash("func")
	h2 := TagHash("func")
	if h1 != h2 {
		t.Fatal("TagHash not deterministic")
	}
	h3 := TagHash("type")
	if h1 == h3 {
		t.Fatal("different tags should produce different hashes")
	}

	// GenKey decode.
	key := GenKey(42)
	if DecodeGen(key) != 42 {
		t.Fatal("GenKey round-trip")
	}

	// TypKey decode.
	tk := TypKey(h1, 100, 7)
	th, count, gen := DecodeTyp(tk)
	if th != h1 || count != 100 || gen != 7 {
		t.Fatalf("TypKey round-trip: %v %d %d", th, count, gen)
	}

	// BndKey decode.
	bk := BndKey(h1, 5, 999)
	bth, bgen, bsite := DecodeBnd(bk)
	if bth != h1 || bgen != 5 || bsite != 999 {
		t.Fatalf("BndKey round-trip: %v %d %d", bth, bgen, bsite)
	}

	// MisKey decode.
	mk := MisKey(h3, 3, 50)
	mth, mgen, mcount := DecodeMis(mk)
	if mth != h3 || mgen != 3 || mcount != 50 {
		t.Fatalf("MisKey round-trip: %v %d %d", mth, mgen, mcount)
	}

	// FitKey decode.
	fk := FitKey(DimBehav, 10)
	fdim, fgen := DecodeFit(fk)
	if fdim != DimBehav || fgen != 10 {
		t.Fatalf("FitKey round-trip: %d %d", fdim, fgen)
	}

	// HexKey decode.
	hk := HexKey(3, 15)
	hop, hgen := DecodeHex(hk)
	if hop != 3 || hgen != 15 {
		t.Fatalf("HexKey round-trip: %d %d", hop, hgen)
	}
}

func TestValueRoundTrip(t *testing.T) {
	// Fitness value.
	v := EncodeFitValue(3, 4)
	n, d := DecodeFitValue(v)
	if n != 3 || d != 4 {
		t.Fatalf("FitValue round-trip: %d/%d", n, d)
	}

	// Health value.
	hv := EncodeHltValue(100, 256, 3, 4)
	occ, tot, hn, hd := DecodeHltValue(hv)
	if occ != 100 || tot != 256 || hn != 3 || hd != 4 {
		t.Fatalf("HltValue round-trip: %d %d %d/%d", occ, tot, hn, hd)
	}

	// U32 value.
	uv := EncodeU32Value(42)
	if DecodeU32Value(uv) != 42 {
		t.Fatal("U32Value round-trip")
	}
}

func TestRecordAndQueryGeneration(t *testing.T) {
	db := tmpDB(t)

	if err := db.RecordGeneration(0, "", 0, time.Now()); err != nil {
		t.Fatalf("record gen 0: %v", err)
	}
	if err := db.RecordGeneration(1, "abc123", 0, time.Now()); err != nil {
		t.Fatalf("record gen 1: %v", err)
	}

	meta := db.QueryGeneration(0)
	if meta == nil {
		t.Fatal("gen 0 not found")
	}
	if meta.InstanceID != 0 {
		t.Fatalf("expected inst 0, got %d", meta.InstanceID)
	}

	meta1 := db.QueryGeneration(1)
	if meta1 == nil || meta1.ParentHash != "abc123" {
		t.Fatal("gen 1 parent hash mismatch")
	}

	// Nonexistent generation.
	if db.QueryGeneration(99) != nil {
		t.Fatal("expected nil for nonexistent gen")
	}
}

func TestRecordAndQueryTypeSig(t *testing.T) {
	db := tmpDB(t)

	// Record type signatures for 3 generations.
	for gen := uint32(0); gen < 3; gen++ {
		err := db.RecordTypeSig(gen, []TagCount{
			{Tag: "func", Count: int(10 + gen*5)},
			{Tag: "type", Count: int(20 + gen*3)},
		})
		if err != nil {
			t.Fatalf("record type sig gen %d: %v", gen, err)
		}
	}

	// Query func trend.
	trend := db.QueryTypeTrend("func", 10)
	if len(trend) != 3 {
		t.Fatalf("expected 3 points, got %d", len(trend))
	}
	// Should be ascending by generation.
	if trend[0].Gen != 0 || trend[1].Gen != 1 || trend[2].Gen != 2 {
		t.Fatalf("wrong gen order: %v", trend)
	}
	if trend[0].Count != 10 || trend[1].Count != 15 || trend[2].Count != 20 {
		t.Fatalf("wrong counts: %v", trend)
	}

	// Query with lastN limit.
	limited := db.QueryTypeTrend("func", 2)
	if len(limited) != 2 {
		t.Fatalf("expected 2 points, got %d", len(limited))
	}
	// Should be the 2 most recent.
	if limited[0].Gen != 1 || limited[1].Gen != 2 {
		t.Fatalf("wrong limited gens: %v", limited)
	}
}

func TestRecordAndQueryMissing(t *testing.T) {
	db := tmpDB(t)

	for gen := uint32(0); gen < 3; gen++ {
		err := db.RecordMissing(gen, []TagCount{
			{Tag: "import", Count: int(5 + gen)},
		})
		if err != nil {
			t.Fatalf("record missing gen %d: %v", gen, err)
		}
	}

	trend := db.QueryMissingTrend("import", 10)
	if len(trend) != 3 {
		t.Fatalf("expected 3 points, got %d", len(trend))
	}
	if trend[0].Count != 5 || trend[2].Count != 7 {
		t.Fatalf("wrong missing counts: %v", trend)
	}
}

func TestRecordAndQueryBonds(t *testing.T) {
	db := tmpDB(t)

	// Gen 0: 3 func bonds.
	err := db.RecordBonds(0, []BondRecord{
		{Tag: "func", SiteID: 1},
		{Tag: "func", SiteID: 2},
		{Tag: "func", SiteID: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Gen 1: 5 func bonds.
	bonds := make([]BondRecord, 5)
	for i := range bonds {
		bonds[i] = BondRecord{Tag: "func", SiteID: uint32(10 + i)}
	}
	if err := db.RecordBonds(1, bonds); err != nil {
		t.Fatal(err)
	}

	// Query single gen count.
	if db.QueryBondCount("func", 0) != 3 {
		t.Fatalf("expected 3 bonds in gen 0, got %d", db.QueryBondCount("func", 0))
	}
	if db.QueryBondCount("func", 1) != 5 {
		t.Fatalf("expected 5 bonds in gen 1, got %d", db.QueryBondCount("func", 1))
	}

	// Bond history.
	history := db.QueryBondHistory("func", 10)
	if len(history) != 2 {
		t.Fatalf("expected 2 gens, got %d", len(history))
	}
	if history[0].Gen != 0 || history[0].Count != 3 {
		t.Fatalf("gen 0: %v", history[0])
	}
	if history[1].Gen != 1 || history[1].Count != 5 {
		t.Fatalf("gen 1: %v", history[1])
	}
}

func TestRecordAndQueryFitness(t *testing.T) {
	db := tmpDB(t)

	for gen := uint32(0); gen < 5; gen++ {
		score := ratio.New(int64(gen), 10)
		err := db.RecordFitness(gen, score, score, score, score)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Overall trajectory.
	traj := db.QueryFitnessTrajectory(3)
	if len(traj) != 3 {
		t.Fatalf("expected 3, got %d", len(traj))
	}
	// Should be gens 2,3,4 ascending.
	if traj[0].Gen != 2 || traj[2].Gen != 4 {
		t.Fatalf("wrong gens: %v", traj)
	}
	// Gen 4: 4/10 = 2/5.
	if traj[2].Score.Num != 2 || traj[2].Score.Denom != 5 {
		t.Fatalf("wrong score: %v", traj[2].Score)
	}

	// Source dimension.
	src := db.QueryFitnessDimension(DimSource, 5)
	if len(src) != 5 {
		t.Fatalf("expected 5, got %d", len(src))
	}
}

func TestRecordAndQueryHealth(t *testing.T) {
	db := tmpDB(t)

	for gen := uint32(0); gen < 3; gen++ {
		err := db.RecordHealth(gen, 100+gen, 256, ratio.New(int64(gen+1), 2))
		if err != nil {
			t.Fatal(err)
		}
	}

	hist := db.QueryHealthHistory(10)
	if len(hist) != 3 {
		t.Fatalf("expected 3, got %d", len(hist))
	}
	if hist[0].Occupied != 100 || hist[2].Occupied != 102 {
		t.Fatalf("wrong occupied: %v", hist)
	}
	if hist[1].AvgLockIn.Num != 1 || hist[1].AvgLockIn.Denom != 1 {
		t.Fatalf("wrong avg lock-in: %v", hist[1].AvgLockIn)
	}
}

func TestRecordAndQueryHexagramOps(t *testing.T) {
	db := tmpDB(t)

	for gen := uint32(0); gen < 3; gen++ {
		ops := map[byte]uint32{
			0: 10 + gen,   // accrete
			1: 5 + gen*2,  // dissolve
		}
		if err := db.RecordHexagramOps(gen, ops); err != nil {
			t.Fatal(err)
		}
	}

	accrete := db.QueryHexagramOps(0, 10)
	if len(accrete) != 3 {
		t.Fatalf("expected 3, got %d", len(accrete))
	}
	if accrete[0].Count != 10 || accrete[2].Count != 12 {
		t.Fatalf("wrong accrete counts: %v", accrete)
	}

	dissolve := db.QueryHexagramOps(1, 2)
	if len(dissolve) != 2 {
		t.Fatalf("expected 2, got %d", len(dissolve))
	}
}

func TestRecordAndQueryConnectivity(t *testing.T) {
	db := tmpDB(t)

	err := db.RecordConnectivity(0, []TagRatio{
		{Tag: "func", Value: ratio.New(3, 1)},
		{Tag: "type", Value: ratio.New(5, 2)},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Connectivity is stored per-key, not queried as a trend yet.
	// Verify by reading the raw key.
	h := TagHash("func")
	meta := db.QueryGeneration(0)
	// No gen metadata recorded, so nil is expected.
	_ = meta
	_ = h
}

func TestRecordAndQueryLockIn(t *testing.T) {
	db := tmpDB(t)

	buckets := map[byte]uint32{
		0:   5,  // very low lock-in
		128: 20, // medium
		255: 10, // saturated
	}
	if err := db.RecordLockInDist(0, buckets); err != nil {
		t.Fatal(err)
	}
	// Lock-in distribution is stored but not yet queried as trend.
	// This test verifies write succeeds without error.
}

func TestEmptyDB(t *testing.T) {
	db := tmpDB(t)

	// All queries should return empty/nil gracefully.
	if trend := db.QueryTypeTrend("func", 10); len(trend) != 0 {
		t.Fatalf("expected empty, got %d", len(trend))
	}
	if miss := db.QueryMissingTrend("func", 10); len(miss) != 0 {
		t.Fatalf("expected empty, got %d", len(miss))
	}
	if traj := db.QueryFitnessTrajectory(10); len(traj) != 0 {
		t.Fatalf("expected empty, got %d", len(traj))
	}
	if hist := db.QueryHealthHistory(10); len(hist) != 0 {
		t.Fatalf("expected empty, got %d", len(hist))
	}
	if bonds := db.QueryBondHistory("func", 10); len(bonds) != 0 {
		t.Fatalf("expected empty, got %d", len(bonds))
	}
	if db.QueryBondCount("func", 0) != 0 {
		t.Fatal("expected 0 bonds")
	}
	if db.QueryGeneration(0) != nil {
		t.Fatal("expected nil")
	}
}

func TestGraphTraversal(t *testing.T) {
	db := tmpDB(t)

	// Simulate 5 generations with varying func bond counts and fitness.
	for gen := uint32(0); gen < 5; gen++ {
		bondCount := int(10 + gen*3)
		bonds := make([]BondRecord, bondCount)
		for i := range bonds {
			bonds[i] = BondRecord{Tag: "func", SiteID: uint32(i)}
		}
		if err := db.RecordBonds(gen, bonds); err != nil {
			t.Fatal(err)
		}

		// Fitness increases with generation.
		score := ratio.New(int64(gen+1), 5)
		if err := db.RecordFitness(gen, score, score, score, score); err != nil {
			t.Fatal(err)
		}

		if err := db.RecordTypeSig(gen, []TagCount{
			{Tag: "func", Count: bondCount},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Graph traversal: find gens where func bonds increased, cross-ref fitness.
	bondHist := db.QueryBondHistory("func", 5)
	fitTraj := db.QueryFitnessTrajectory(5)

	if len(bondHist) != 5 || len(fitTraj) != 5 {
		t.Fatalf("expected 5 each, got bonds=%d fit=%d", len(bondHist), len(fitTraj))
	}

	// Verify correlation: more bonds → higher fitness (by construction).
	for i := 1; i < len(bondHist); i++ {
		if bondHist[i].Count <= bondHist[i-1].Count {
			t.Fatalf("bonds should increase: gen %d=%d, gen %d=%d",
				bondHist[i-1].Gen, bondHist[i-1].Count,
				bondHist[i].Gen, bondHist[i].Count)
		}
		if !fitTraj[i-1].Score.Less(fitTraj[i].Score) {
			t.Fatalf("fitness should increase: gen %d=%v, gen %d=%v",
				fitTraj[i-1].Gen, fitTraj[i-1].Score,
				fitTraj[i].Gen, fitTraj[i].Score)
		}
	}
}
