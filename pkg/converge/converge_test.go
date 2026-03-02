package converge

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/ratio"
)

func TestTrackerConverges(t *testing.T) {
	// Window size of 100, threshold of 1/100.
	tr := NewTracker(100, ratio.New(1, 100))

	// First window: 100 tokens, 50 new vertices (interleaved).
	for i := range 100 {
		tr.RecordToken()
		if i < 50 {
			tr.RecordNewVertex()
		}
	}

	if tr.IsConverged() {
		t.Error("should not converge after 1 window")
	}

	// Next 3 windows: 100 tokens each, 0 new vertices. Rate = 0.
	for range 300 {
		tr.RecordToken()
	}

	if !tr.IsConverged() {
		r := tr.Report()
		t.Errorf("should converge after 3 windows with zero vertex creation; windows=%d rate=%s",
			r.WindowCount, r.CurrentRate.String())
		for i, w := range r.RecentWindows {
			t.Logf("  window %d: tokens=%d vertices=%d rate=%s",
				i, w.TokensProcessed, w.NewVertices, w.VertexRate.String())
		}
	}
}

func TestTrackerNotConvergedEarly(t *testing.T) {
	tr := NewTracker(100, ratio.New(1, 100))

	// Only 2 empty windows — need 3.
	for range 200 {
		tr.RecordToken()
	}

	if tr.IsConverged() {
		t.Error("should not converge with only 2 windows")
	}
}

func TestTrackerReportAccuracy(t *testing.T) {
	tr := NewTracker(50, ratio.New(1, 100))

	for range 100 {
		tr.RecordToken()
	}
	for range 10 {
		tr.RecordNewVertex()
	}

	r := tr.Report()
	if r.TotalTokens != 100 {
		t.Errorf("total tokens = %d, want 100", r.TotalTokens)
	}
	if r.TotalNewVertices != 10 {
		t.Errorf("total vertices = %d, want 10", r.TotalNewVertices)
	}
	if r.WindowCount != 2 {
		t.Errorf("window count = %d, want 2", r.WindowCount)
	}
	expected := ratio.New(10, 100)
	if !r.CurrentRate.Equal(expected) {
		t.Errorf("rate = %s, want %s", r.CurrentRate.String(), expected.String())
	}
}

func TestTrackerMarshal(t *testing.T) {
	tr := NewTracker(100, ratio.New(1, 1000))
	for range 200 {
		tr.RecordToken()
	}
	tr.RecordNewVertex()

	data, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("marshal produced empty data")
	}
}
