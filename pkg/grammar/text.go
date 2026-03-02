package grammar

import (
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// NaturalText is the grammar for natural language text recognition.
//
// Word tokens are sub-classified by length bucket (w1..w5) to create a
// richer constraint envelope. The adjacency rules encode which word length
// classes can neighbor each other, punctuation, and spaces. The lattice
// topology shaped by these rules captures word-length transition patterns
// that differ between human and AI text.
//
// Word length buckets:
//
//	w1: 1 char      (a, I)
//	w2: 2-3 chars   (the, is, an)
//	w3: 4-5 chars   (from, with, about)
//	w4: 6-8 chars   (between, another)
//	w5: 9+ chars    (restructured, acknowledging)
var NaturalText = &Grammar{Rules: []Rule{
	// Short words connect to everything — they're the glue of English.
	{Tag: "w1", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}},
	{Tag: "w2", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}},
	// Medium words: most flexible.
	{Tag: "w3", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}},
	// Longer words tend to precede short connectors or punctuation.
	{Tag: "w4", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}},
	{Tag: "w5", Neighbors: []string{"w1", "w2", "w3", "w4", "punct", "space"}},
	// Punctuation bridges words and other punctuation.
	{Tag: "punct", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}},
	// Spaces always lead to words.
	{Tag: "space", Neighbors: []string{"w1", "w2", "w3", "w4", "w5", "punct"}},
}}

// TextDefaultCounts returns a tag count map for building a natural language
// lattice. Distributes targetSize nodes across the seven text tags using
// proportions derived from typical English prose word-length distributions:
//
//	w1: ~5%   (single-char words are rare)
//	w2: ~20%  (very common: the, is, an, to, of)
//	w3: ~20%  (common: from, with, that, about)
//	w4: ~15%  (content words: between, another)
//	w5: ~5%   (long formal words)
//	punct: ~10%
//	space: ~25%
func TextDefaultCounts(targetSize int) map[string]int {
	if targetSize < 7 {
		targetSize = 7
	}
	w1 := int(ratio.New(5, 100).ScaleInt(int64(targetSize)))
	w2 := int(ratio.New(20, 100).ScaleInt(int64(targetSize)))
	w3 := int(ratio.New(20, 100).ScaleInt(int64(targetSize)))
	w4 := int(ratio.New(15, 100).ScaleInt(int64(targetSize)))
	w5 := int(ratio.New(5, 100).ScaleInt(int64(targetSize)))
	punct := int(ratio.New(10, 100).ScaleInt(int64(targetSize)))
	space := targetSize - w1 - w2 - w3 - w4 - w5 - punct

	// Ensure each type has at least 1 node.
	counts := map[string]int{
		"w1": w1, "w2": w2, "w3": w3, "w4": w4, "w5": w5,
		"punct": punct, "space": space,
	}
	for k, v := range counts {
		if v < 1 {
			counts[k] = 1
		}
	}
	return counts
}
