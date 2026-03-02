package grammar

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/memory"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// testElement is a minimal axiom.Element for testing.
type testElement struct {
	tag string
	val string
}

func (e testElement) Type() string  { return e.tag }
func (e testElement) Value() any    { return e.val }

func TestGoASTCanNeighbor(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Functions contain body statements.
		{"func", "assign", true},
		{"func", "return", true},
		{"func", "if", true},
		{"func", "for", true},
		{"func", "ident:func-name", true},

		// Functions don't directly neighbor package/import.
		{"func", "package", false},
		{"func", "import", false},

		// Package neighbors import and declarations.
		{"package", "import", true},
		{"package", "func", true},
		{"package", "type", true},

		// Type contains struct/interface/field.
		{"type", "struct", true},
		{"type", "interface", true},
		{"type", "field", true},

		// Body statements neighbor each other and declaration-level idents.
		{"assign", "return", true},
		{"if", "for", true},
		{"assign", "ident:var-name", true},

		// Text fallback.
		{"word", "punct", true},
		{"word", "word", true},
		{"punct", "punct", true},

		// Cross-domain: import doesn't neighbor body statements.
		{"import", "assign", false},
		{"import", "return", false},

		// Struct doesn't directly neighbor import.
		{"struct", "import", false},

		// Ident subtypes have role-specific adjacency.
		{"ident:func-name", "func", true},
		{"ident:type-name", "type", true},
		{"ident:field-name", "struct", true},
		{"ident:receiver", "method", true},
		{"ident:param", "func", true},
	}

	for _, tt := range tests {
		got := GoAST.CanNeighbor(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("GoAST.CanNeighbor(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestGoEmitTighter(t *testing.T) {
	// GoEmit should be tighter than GoAST in some cases.
	// func should only neighbor body statements in GoEmit (not ident subtypes).
	if GoEmit.CanNeighbor("func", "ident:func-name") {
		t.Error("GoEmit: func should not neighbor ident:func-name (tighter scoping)")
	}
	// But func should still neighbor assign.
	if !GoEmit.CanNeighbor("func", "assign") {
		t.Error("GoEmit: func should neighbor assign")
	}
	// import should only neighbor package.
	if !GoEmit.CanNeighbor("import", "package") {
		t.Error("GoEmit: import should neighbor package")
	}
	if GoEmit.CanNeighbor("import", "func") {
		t.Error("GoEmit: import should not neighbor func (tighter)")
	}
}

func TestGrammarTags(t *testing.T) {
	tags := GoAST.Tags()
	if len(tags) == 0 {
		t.Fatal("GoAST.Tags() returned empty")
	}
	// Should include core Go types.
	want := map[string]bool{
		"func": true, "assign": true, "return": true,
		"type": true, "struct": true, "ident:var-name": true,
		"word": true, "punct": true, "package": true,
	}
	tagSet := make(map[string]bool)
	for _, tag := range tags {
		tagSet[tag] = true
	}
	for tag := range want {
		if !tagSet[tag] {
			t.Errorf("GoAST.Tags() missing %q", tag)
		}
	}
}

func TestGrammarNeighborsOf(t *testing.T) {
	nbs := GoAST.NeighborsOf("func")
	if len(nbs) == 0 {
		t.Fatal("GoAST.NeighborsOf(func) returned empty")
	}
	// func should have assign, return, if, for among neighbors.
	nbSet := make(map[string]bool)
	for _, nb := range nbs {
		nbSet[nb] = true
	}
	for _, want := range []string{"assign", "return", "if", "for"} {
		if !nbSet[want] {
			t.Errorf("GoAST.NeighborsOf(func) missing %q", want)
		}
	}
}

func TestGrammarConstraintAdmitsBasic(t *testing.T) {
	c := NewConstraint("func", GoAST)

	if !c.Admits(testElement{"func", "main"}) {
		t.Error("should admit matching type")
	}
	if c.Admits(testElement{"assign", "x := 1"}) {
		t.Error("should reject non-matching type")
	}
}

func TestGrammarConstraintAdmitsInContext(t *testing.T) {
	c := NewConstraint("assign", GoAST)
	elem := testElement{"assign", "x := 1"}

	// No neighbors — seed bonding, should admit.
	if !c.AdmitsInContext(elem, nil) {
		t.Error("should admit with nil neighbors (seed)")
	}
	if !c.AdmitsInContext(elem, []axiom.Element{nil, nil}) {
		t.Error("should admit with all-nil neighbors (seed)")
	}

	// Neighbor is func — grammar-adjacent, should admit.
	funcNeighbor := testElement{"func", "main"}
	if !c.AdmitsInContext(elem, []axiom.Element{funcNeighbor}) {
		t.Error("should admit with func neighbor (grammar-adjacent)")
	}

	// Neighbor is ident:var-name — grammar-adjacent to assign, should admit.
	identNeighbor := testElement{"ident:var-name", "x"}
	if !c.AdmitsInContext(elem, []axiom.Element{identNeighbor}) {
		t.Error("should admit with ident:var-name neighbor (grammar-adjacent)")
	}

	// Neighbor is package — NOT grammar-adjacent to assign, should reject.
	pkgNeighbor := testElement{"package", "main"}
	if c.AdmitsInContext(elem, []axiom.Element{pkgNeighbor}) {
		t.Error("should reject with package neighbor (not grammar-adjacent)")
	}

	// Wrong element type — should reject regardless of neighbors.
	wrongElem := testElement{"func", "main"}
	if c.AdmitsInContext(wrongElem, []axiom.Element{funcNeighbor}) {
		t.Error("should reject wrong element type")
	}
}

func TestBuildGrammarLattice(t *testing.T) {
	counts := map[string]int{
		"func":           4,
		"assign":         6,
		"ident:var-name": 8,
	}

	seed := [32]byte{1, 2, 3}
	l := BuildGrammarLattice(GoAST, counts, seed, func(tag string) axiom.Constraint {
		return NewConstraint(tag, GoAST)
	})

	// Should have 18 nodes total.
	if l.Size() != 18 {
		t.Errorf("lattice size = %d, want 18", l.Size())
	}

	// All nodes should have at least one neighbor (ring connectivity).
	for _, n := range l.Nodes() {
		if len(n.Neighbors()) == 0 {
			t.Errorf("node %d has no neighbors", n.ID())
		}
	}
}

func TestBuildGrammarLatticeSeedDifferentiation(t *testing.T) {
	counts := map[string]int{
		"func":           4,
		"assign":         6,
		"ident:var-name": 8,
		"return":         4,
	}

	seed1 := [32]byte{1}
	seed2 := [32]byte{2}

	factory := func(tag string) axiom.Constraint {
		return NewConstraint(tag, GoAST)
	}

	l1 := BuildGrammarLattice(GoAST, counts, seed1, factory)
	l2 := BuildGrammarLattice(GoAST, counts, seed2, factory)

	// Same size.
	if l1.Size() != l2.Size() {
		t.Errorf("sizes differ: %d vs %d", l1.Size(), l2.Size())
	}

	// But different neighbor sets (at least some nodes should differ).
	// Compare neighbor counts per node — different seeds should produce
	// different bridge selections, leading to different degree distributions.
	degrees1 := make([]int, l1.Size())
	degrees2 := make([]int, l2.Size())
	for i, n := range l1.Nodes() {
		degrees1[i] = len(n.Neighbors())
	}
	for i, n := range l2.Nodes() {
		degrees2[i] = len(n.Neighbors())
	}

	identical := true
	for i := range degrees1 {
		if degrees1[i] != degrees2[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("two different seeds produced identical degree distributions — differentiation failed")
	}
}

func TestDefaultCounts(t *testing.T) {
	counts := GoAST.DefaultCounts(100, ratio.New(3, 5))

	// Should have entries for all grammar tags that have weight > 0.
	if counts["func"] == 0 {
		t.Error("func should have > 0 nodes")
	}
	if counts["assign"] == 0 {
		t.Error("assign should have > 0 nodes")
	}
	if counts["word"] == 0 {
		t.Error("word should have > 0 nodes")
	}
	if counts["punct"] == 0 {
		t.Error("punct should have > 0 nodes")
	}

	// Total should be close to targetSize.
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 100 {
		t.Errorf("total nodes = %d, want 100", total)
	}
}

func TestReadVagusNil(t *testing.T) {
	sig := ReadVagus(nil, DefaultBaseline())
	b := DefaultBaseline()

	if sig.DissolveHalfLife != b.DissolveHalfLife {
		t.Error("nil digest should return baseline half-life")
	}
	if sig.GrowMaxSteps != b.GrowMaxSteps {
		t.Error("nil digest should return baseline max steps")
	}
}

func TestReadVagusFitnessFalling(t *testing.T) {
	d := &memory.Digest{
		FitnessTrend: memory.TrendFalling,
		Types:        make(map[string]memory.TypeDigest),
	}
	b := DefaultBaseline()
	sig := ReadVagus(d, b)

	// Dissolve threshold should be lower (more aggressive).
	if !sig.DissolveThreshold.Less(b.DissolveThreshold) {
		t.Error("falling fitness should lower dissolve threshold")
	}
	// Half-life should decrease (faster turnover).
	if sig.DissolveHalfLife >= b.DissolveHalfLife {
		t.Error("falling fitness should decrease half-life")
	}
}

func TestReadVagusFitnessRising(t *testing.T) {
	d := &memory.Digest{
		FitnessTrend: memory.TrendRising,
		Types:        make(map[string]memory.TypeDigest),
	}
	b := DefaultBaseline()
	sig := ReadVagus(d, b)

	// Dissolve threshold should be higher (preserve what's working).
	if !b.DissolveThreshold.Less(sig.DissolveThreshold) {
		t.Error("rising fitness should raise dissolve threshold")
	}
}

func TestReadVagusFitnessStagnant(t *testing.T) {
	d := &memory.Digest{
		FitnessTrend: memory.TrendStagnant,
		Types:        make(map[string]memory.TypeDigest),
	}
	b := DefaultBaseline()
	sig := ReadVagus(d, b)

	// More exploration: higher max steps, more workers.
	if sig.GrowMaxSteps <= b.GrowMaxSteps {
		t.Error("stagnant fitness should increase grow max steps")
	}
	if sig.GrowWorkers <= b.GrowWorkers {
		t.Error("stagnant fitness should increase grow workers")
	}
}

func TestReadVagusTypeAdjustments(t *testing.T) {
	d := &memory.Digest{
		Types: map[string]memory.TypeDigest{
			"func":           {Tag: "func", BondRate: ratio.New(6, 10)},           // > 50% → grow
			"assign":         {Tag: "assign", BondRate: ratio.New(5, 100)},         // < 10% → shrink
			"ident:var-name": {Tag: "ident:var-name", BondRate: ratio.New(3, 10)},  // 30% → no change
		},
	}
	sig := ReadVagus(d, DefaultBaseline())

	if sig.TypeAdjustments["func"] != 1 {
		t.Errorf("func adjustment = %d, want 1 (grow)", sig.TypeAdjustments["func"])
	}
	if sig.TypeAdjustments["assign"] != -1 {
		t.Errorf("assign adjustment = %d, want -1 (shrink)", sig.TypeAdjustments["assign"])
	}
	if sig.TypeAdjustments["ident:var-name"] != 0 {
		t.Errorf("ident:var-name adjustment = %d, want 0 (no change)", sig.TypeAdjustments["ident:var-name"])
	}
}

func TestVagusAdjustCounts(t *testing.T) {
	sig := VagusSignal{
		TypeAdjustments: map[string]int{
			"func":   1,  // grow
			"assign": -1, // shrink
		},
	}

	base := map[string]int{
		"func":           10,
		"assign":         10,
		"ident:var-name": 10,
	}

	result := sig.AdjustCounts(base)

	// func should grow by 50%: 10 → 15
	if result["func"] != 15 {
		t.Errorf("func count = %d, want 15", result["func"])
	}
	// assign should shrink by 50%: 10 → 5
	if result["assign"] != 5 {
		t.Errorf("assign count = %d, want 5", result["assign"])
	}
	// ident:var-name unchanged: 10
	if result["ident:var-name"] != 10 {
		t.Errorf("ident:var-name count = %d, want 10", result["ident:var-name"])
	}
}

func TestVagusDissolveConfig(t *testing.T) {
	sig := DefaultSignal()
	cfg := sig.DissolveConfig(15 * 1e6) // 15ms in nanoseconds
	if cfg.HalfLife != sig.DissolveHalfLife {
		t.Error("DissolveConfig should use signal half-life")
	}
}

func TestVagusGrowConfig(t *testing.T) {
	sig := DefaultSignal()
	cfg := sig.GrowConfig()
	if cfg.MaxSteps != sig.GrowMaxSteps {
		t.Error("GrowConfig should use signal max steps")
	}
	if cfg.Workers != sig.GrowWorkers {
		t.Error("GrowConfig should use signal workers")
	}
}
