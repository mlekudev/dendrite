package grammar

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/grow"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// BondEvent is the grammar for pass-2+ detection: structural patterns
// in how text bonds to a trained lattice.
//
// Event tags encode both the element type and the bond outcome:
//   - w1.hit, w2.hit, ..., w5.hit, punct.hit, space.hit  (bonded)
//   - w1.miss, w2.miss, ..., w5.miss, punct.miss, space.miss (expired)
//
// This preserves which TYPE of token bonded or missed, carrying stylometric
// information through the cascade. AI text has different hit/miss patterns
// per word-length class than human text.
//
// Adjacency: any hit can neighbor any hit or miss. Misses can neighbor
// anything. The topology encodes transition patterns.
var BondEvent = &Grammar{Rules: func() []Rule {
	types := []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}
	var allTags []string
	for _, t := range types {
		allTags = append(allTags, t+".hit", t+".miss")
	}

	var rules []Rule
	for _, tag := range allTags {
		rules = append(rules, Rule{Tag: tag, Neighbors: allTags})
	}
	return rules
}()}

// EventDefaultCounts returns node allocation for a bond-event lattice.
// Allocates nodes proportionally to expected hit/miss rates per type.
func EventDefaultCounts(targetSize int) map[string]int {
	types := []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}
	n := len(types) * 2 // hit + miss per type
	if targetSize < n {
		targetSize = n
	}

	// Proportions based on expected English text + typical bond rates.
	// Hit types get more nodes (most tokens bond).
	weights := map[string]int64{
		"w1.hit": 4, "w1.miss": 1,
		"w2.hit": 18, "w2.miss": 2,
		"w3.hit": 18, "w3.miss": 2,
		"w4.hit": 13, "w4.miss": 2,
		"w5.hit": 4, "w5.miss": 1,
		"punct.hit": 9, "punct.miss": 1,
		"space.hit": 22, "space.miss": 3,
	}

	var totalWeight int64
	for _, w := range weights {
		totalWeight += w
	}

	counts := make(map[string]int, n)
	allocated := 0
	for tag, w := range weights {
		c := int(ratio.New(w, totalWeight).ScaleInt(int64(targetSize)))
		if c < 1 {
			c = 1
		}
		counts[tag] = c
		allocated += c
	}

	// Distribute remainder to the largest bucket.
	if allocated < targetSize {
		maxTag := ""
		maxCount := 0
		for tag, c := range counts {
			if c > maxCount {
				maxCount = c
				maxTag = tag
			}
		}
		counts[maxTag] += targetSize - allocated
	}

	return counts
}

// eventElement wraps a grow.Event as an axiom.Element for pass 2+.
type eventElement struct {
	tag   string
	steps int
}

func (e eventElement) Type() string { return e.tag }
func (e eventElement) Value() any   { return e.steps }

// ClassifyEvent converts a grow.Event into a pass-2+ element.
// The tag encodes both the original element type and the bond outcome.
//
// For multi-pass chains, the element type may already be a compound tag
// (e.g., "w3.hit" from a previous pass). We normalize by extracting the
// base type (everything before the first dot) so every pass produces the
// same 14-tag vocabulary. Each successive pass captures a different
// structural octave while speaking the same language.
func ClassifyEvent(ev grow.Event) axiom.Element {
	elemType := "unk"
	if ev.Element != nil {
		elemType = ev.Element.Type()
	}

	// Strip compound suffixes: "w3.hit" → "w3", "w3.hit.miss" → "w3".
	if idx := strings.IndexByte(elemType, '.'); idx >= 0 {
		elemType = elemType[:idx]
	}

	switch ev.Type {
	case grow.EventBonded:
		return eventElement{tag: elemType + ".hit", steps: ev.Steps}
	default:
		return eventElement{tag: elemType + ".miss", steps: ev.Steps}
	}
}

// EventStream converts a channel of grow.Events into a channel of
// pass-2 elements suitable for feeding into a BondEvent lattice.
func EventStream(events <-chan grow.Event) <-chan axiom.Element {
	out := make(chan axiom.Element, cap(events))
	go func() {
		defer close(out)
		for ev := range events {
			out <- ClassifyEvent(ev)
		}
	}()
	return out
}

// FormatEventTag returns a human-readable label for event stats.
func FormatEventTag(tag string, steps int) string {
	return fmt.Sprintf("%s(%d)", tag, steps)
}

// EventTags returns all event tags sorted for consistent display.
func EventTags() []string {
	types := []string{"w1", "w2", "w3", "w4", "w5", "punct", "space"}
	var tags []string
	for _, t := range types {
		tags = append(tags, t+".hit", t+".miss")
	}
	sort.Strings(tags)
	return tags
}
