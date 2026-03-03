package grammar

import (
	"testing"
)

func TestNaturalTextAdjacency(t *testing.T) {
	g := NaturalText

	tests := []struct {
		a, b string
		want bool
	}{
		// Word length classes neighbor each other and punct/space.
		{"w2", "w3", true},
		{"w3", "punct", true},
		{"w1", "space", true},
		{"punct", "w2", true},
		{"punct", "punct", true},
		{"punct", "space", true},
		{"space", "w3", true},
		{"space", "punct", true},
		{"space", "space", false}, // spaces don't neighbor spaces
		// w5 doesn't neighbor w5 (long words rarely follow long words).
		{"w5", "w5", false},
	}

	for _, tt := range tests {
		if got := g.CanNeighbor(tt.a, tt.b); got != tt.want {
			t.Errorf("CanNeighbor(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNaturalTextTags(t *testing.T) {
	tags := NaturalText.Tags()
	if len(tags) != 8 {
		t.Errorf("got %d tags, want 8: %v", len(tags), tags)
	}
	expected := map[string]bool{
		"w1": true, "w2": true, "w3": true, "w4": true, "w5": true,
		"punct": true, "space": true, "origin": true,
	}
	for _, tag := range tags {
		if !expected[tag] {
			t.Errorf("unexpected tag: %s", tag)
		}
	}
}

func TestTextDefaultCounts(t *testing.T) {
	counts := TextDefaultCounts(1000)

	total := 0
	for _, v := range counts {
		total += v
	}
	if total != 1000 {
		t.Errorf("total = %d, want 1000", total)
	}

	// Word classes combined should be ~65% (5+20+20+15+5).
	wordTotal := counts["w1"] + counts["w2"] + counts["w3"] + counts["w4"] + counts["w5"]
	if wordTotal < 500 || wordTotal > 750 {
		t.Errorf("word total = %d, expected ~650", wordTotal)
	}
}

func TestTextDefaultCountsMinimum(t *testing.T) {
	counts := TextDefaultCounts(1)
	for tag, n := range counts {
		if n < 1 {
			t.Errorf("%s count = %d, want >= 1", tag, n)
		}
	}
}

