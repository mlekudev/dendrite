package profile

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/grow"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// mockElement implements axiom.Element for testing.
type mockElement struct {
	tag string
	val any
}

func (e mockElement) Type() string { return e.tag }
func (e mockElement) Value() any   { return e.val }

func TestCollectorRecordsBonds(t *testing.T) {
	c := NewCollector()

	c.RecordGrowEvent(grow.Event{
		Type:    grow.EventBonded,
		NodeID:  1,
		Element: mockElement{"word", "hello"},
		Steps:   3,
	})
	c.RecordGrowEvent(grow.Event{
		Type:    grow.EventBonded,
		NodeID:  2,
		Element: mockElement{"punct", "."},
		Steps:   1,
	})
	c.RecordGrowEvent(grow.Event{
		Type:    grow.EventRejected,
		Element: mockElement{"word", "xyz"},
	})

	snap := c.Snapshot()

	if snap.TokensIngested != 3 {
		t.Errorf("tokens = %d, want 3", snap.TokensIngested)
	}
	if snap.BondEvents != 2 {
		t.Errorf("bonds = %d, want 2", snap.BondEvents)
	}
	if snap.RejectEvents != 1 {
		t.Errorf("rejects = %d, want 1", snap.RejectEvents)
	}
	if snap.PathFreq[1] != 1 || snap.PathFreq[2] != 1 {
		t.Errorf("path freq wrong: %v", snap.PathFreq)
	}
	if snap.BondDist["word"] != 1 || snap.BondDist["punct"] != 1 {
		t.Errorf("bond dist wrong: %v", snap.BondDist)
	}
	if snap.WalkDistHist[3] != 1 || snap.WalkDistHist[1] != 1 {
		t.Errorf("walk dist wrong: %v", snap.WalkDistHist)
	}
}

func TestCollectorTracksTransitions(t *testing.T) {
	c := NewCollector()

	tags := []string{"word", "space", "word", "punct"}
	for i, tag := range tags {
		c.RecordGrowEvent(grow.Event{
			Type:    grow.EventBonded,
			NodeID:  lattice.NodeID(i),
			Element: mockElement{tag, tag},
			Steps:   0,
		})
	}

	snap := c.Snapshot()

	// Should have 3 transitions: word->space, space->word, word->punct.
	if len(snap.TransitionFreq) != 3 {
		t.Errorf("transition count = %d, want 3", len(snap.TransitionFreq))
	}
	if snap.TransitionFreq[[2]string{"word", "space"}] != 1 {
		t.Error("missing word->space transition")
	}
	if snap.TransitionFreq[[2]string{"space", "word"}] != 1 {
		t.Error("missing space->word transition")
	}
	if snap.TransitionFreq[[2]string{"word", "punct"}] != 1 {
		t.Error("missing word->punct transition")
	}
}

func TestProfileClone(t *testing.T) {
	p := New()
	p.TokensIngested = 100
	p.BondEvents = 50
	p.PathFreq[1] = 10
	p.BondDist["word"] = 40

	c := p.Clone()
	c.TokensIngested = 200
	c.PathFreq[1] = 20

	if p.TokensIngested != 100 {
		t.Error("clone modified original tokens")
	}
	if p.PathFreq[1] != 10 {
		t.Error("clone modified original path freq")
	}
}

func TestProfileMarshalRoundtrip(t *testing.T) {
	p := New()
	p.TokensIngested = 42
	p.BondEvents = 10
	p.PathFreq[5] = 3

	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p2, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}

	if p2.TokensIngested != 42 {
		t.Errorf("tokens = %d, want 42", p2.TokensIngested)
	}
	if p2.BondEvents != 10 {
		t.Errorf("bonds = %d, want 10", p2.BondEvents)
	}
}

func TestStatsCompute(t *testing.T) {
	p := New()
	p.TokensIngested = 1000
	p.BondEvents = 800
	p.NewVertices = 5

	// Distribute bonds across some nodes.
	for i := 0; i < 20; i++ {
		p.PathFreq[lattice.NodeID(i)] = int64(i + 1)
	}
	p.WalkDistHist[0] = 400
	p.WalkDistHist[1] = 200
	p.WalkDistHist[5] = 150
	p.WalkDistHist[10] = 50

	s := Compute(p, 100)

	if s.BondRate.IsZero() {
		t.Error("bond rate is zero")
	}
	if !s.BondRate.Equal(ratio.New(800, 1000)) {
		t.Errorf("bond rate = %s, want 4/5", s.BondRate.String())
	}
	if s.NewVertexRate.IsZero() {
		t.Error("new vertex rate is zero")
	}
	if s.PathEntropy.IsZero() {
		t.Error("path entropy is zero")
	}
	if s.VertexCoverage.IsZero() {
		t.Error("vertex coverage is zero")
	}
	if !s.VertexCoverage.Equal(ratio.New(20, 100)) {
		t.Errorf("vertex coverage = %s, want 1/5", s.VertexCoverage.String())
	}
	if s.BurstinessGini.IsZero() {
		t.Error("gini is zero (should be non-zero for non-uniform distribution)")
	}
}

func TestCompareIdentical(t *testing.T) {
	s := Stats{
		PathEntropy:       ratio.New(5, 1),
		SurprisalVariance: ratio.New(3, 1),
		BurstinessGini:    ratio.New(4, 10),
		VertexCoverage:    ratio.New(2, 10),
		AvgWalkDistance:   ratio.New(7, 1),
		BondRate:          ratio.New(8, 10),
		NewVertexRate:     ratio.New(1, 10000),
	}

	v := Compare(s, s)

	if !v.HumanProbability.Equal(ratio.One) {
		t.Errorf("identical stats should give probability 1, got %s", v.HumanProbability.String())
	}
}

func TestCompareDivergent(t *testing.T) {
	baseline := Stats{
		PathEntropy:       ratio.New(10, 1),
		SurprisalVariance: ratio.New(8, 1),
		BurstinessGini:    ratio.New(6, 10),
		VertexCoverage:    ratio.New(3, 10),
		AvgWalkDistance:   ratio.New(5, 1),
		BondRate:          ratio.New(9, 10),
		NewVertexRate:     ratio.New(1, 100000),
	}

	// AI-like: lower entropy, lower variance, lower burstiness.
	aiLike := Stats{
		PathEntropy:       ratio.New(3, 1),
		SurprisalVariance: ratio.New(2, 1),
		BurstinessGini:    ratio.New(1, 10),
		VertexCoverage:    ratio.New(3, 10),
		AvgWalkDistance:   ratio.New(5, 1),
		BondRate:          ratio.New(9, 10),
		NewVertexRate:     ratio.New(1, 100000),
	}

	v := Compare(baseline, aiLike)

	if v.HumanProbability.Greater(ratio.New(8, 10)) {
		t.Errorf("AI-like text should have lower human probability, got %s",
			v.HumanProbability.String())
	}
}
