package grammar

import (
	"fmt"
	"strings"

	"github.com/mlekudev/dendrite/pkg/grow"
)

// Verdict is the output of the 8-pass recognition chain.
// It distills per-pass metrics into a single judgment.
type Verdict struct {
	// Human is true if the text appears to be human-written.
	Human bool

	// Confidence ranges from 0 (uncertain) to 1 (certain).
	Confidence float64

	// DeepWalk is the average walk distance at the deepest pass.
	// Human text: ~1.26-1.30, AI text: ~1.34-1.47.
	DeepWalk float64

	// LongMissRate is the fraction of misses at the deepest pass
	// that fall on long words (w4+w5).
	// Human text: ~0.26-0.30, AI text: ~0.32-0.45.
	LongMissRate float64

	// PassHits records the hit rate at each pass.
	PassHits []float64

	// Label is a short human-readable verdict string.
	Label string

	// TrollScore is the structural match rate against the manipulation
	// lattice. Higher values mean the text more closely matches the
	// prosodic patterns of manipulative/trolling writing.
	// Range: 0.0 (no match) to 1.0 (perfect match).
	TrollScore float64

	// TrollLabel is a short human-readable troll verdict string.
	// Empty when troll detection is not active.
	TrollLabel string
}

// PassStats holds per-pass event statistics for scoring.
type PassStats struct {
	Events  []grow.Event
	Bonded  int64
	Expired int64
	Total   int64
}

// Score computes a Verdict from multi-pass detection results.
// passStats[0] is pass 1 (word level), passStats[N-1] is the deepest pass.
func Score(passStats []PassStats) Verdict {
	if len(passStats) == 0 {
		return Verdict{Label: "no data"}
	}

	v := Verdict{
		PassHits: make([]float64, len(passStats)),
	}

	// Record per-pass hit rates.
	for i, ps := range passStats {
		if ps.Total > 0 {
			v.PassHits[i] = float64(ps.Bonded) / float64(ps.Total)
		}
	}

	// Compute deep pass metrics from the last pass.
	deep := passStats[len(passStats)-1]
	v.DeepWalk = avgWalk(deep.Events)
	v.LongMissRate = longMissFraction(deep.Events)

	// Scoring: combine walk distance and long-miss rate.
	//
	// Walk distance boundary: 1.32 is the natural gap between
	// human (1.26-1.30) and AI (1.34-1.47) at pass 8.
	//
	// Long-miss boundary: 0.31 separates human (0.26-0.30)
	// from AI (0.32-0.45).
	//
	// Each metric votes independently. Both agreeing = high confidence.
	walkVote := 0.0  // negative = human, positive = AI
	missVote := 0.0

	walkCenter := 1.32
	if v.DeepWalk < walkCenter {
		walkVote = (walkCenter - v.DeepWalk) / walkCenter * -1 // human direction
	} else {
		walkVote = (v.DeepWalk - walkCenter) / walkCenter // AI direction
	}

	missCenter := 0.31
	if v.LongMissRate < missCenter {
		missVote = (missCenter - v.LongMissRate) / missCenter * -1
	} else {
		missVote = (v.LongMissRate - missCenter) / missCenter
	}

	// Combined score: negative = human, positive = AI.
	combined := (walkVote + missVote) / 2
	v.Human = combined < 0
	v.Confidence = clamp(abs(combined)*3, 0, 1) // scale for readability

	if v.Human {
		v.Label = fmt.Sprintf("HUMAN (%.0f%%)", v.Confidence*100)
	} else {
		v.Label = fmt.Sprintf("AI (%.0f%%)", v.Confidence*100)
	}

	return v
}

// ScoreTroll computes the troll dimension of a verdict from manipulation
// lattice pass stats. Uses baseline-relative scoring:
//
// Normal English prose bonds at ~67% at pass 8 against the manipulation
// lattice. Only text that bonds significantly above this baseline is
// flagged. The score represents how far above baseline the text scores,
// normalized to 0-1.
//
// Texts that collapse before pass 8 (zero events at deep passes) score 0.
func ScoreTroll(v *Verdict, trollStats []PassStats) {
	if len(trollStats) == 0 {
		return
	}

	nPasses := len(trollStats)

	// Find the deepest pass with events.
	lastLivePass := -1
	for i := nPasses - 1; i >= 0; i-- {
		if trollStats[i].Total > 0 {
			lastLivePass = i
			break
		}
	}

	if lastLivePass < 0 {
		return // no events at any pass
	}

	// If text doesn't survive to the final pass, it's structurally
	// dissimilar from manipulation text. Score 0.
	if lastLivePass < nPasses-1 {
		v.TrollScore = 0
		return
	}

	// Deep pass bond rate.
	deep := trollStats[lastLivePass]
	deepRate := float64(deep.Bonded) / float64(deep.Total)

	// Baseline: normal English prose bonds at ~0.67 at pass 8
	// against a 221K-word manipulation lattice. Score is the
	// excess above baseline, scaled so that 0.80 → 100%.
	const baseline = 0.67
	const ceiling = 0.80
	if deepRate <= baseline {
		v.TrollScore = 0
		return
	}

	v.TrollScore = clamp((deepRate-baseline)/(ceiling-baseline), 0, 1)
	v.TrollLabel = fmt.Sprintf("MANIPULATION (%.0f%%)", v.TrollScore*100)
}

// String returns a multi-line verdict summary.
func (v Verdict) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict: %s", v.Label)
	if v.TrollLabel != "" {
		fmt.Fprintf(&b, " | %s", v.TrollLabel)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  deep walk avg:    %.2f", v.DeepWalk)
	if v.DeepWalk <= 1.32 {
		b.WriteString(" (human range)")
	} else {
		b.WriteString(" (AI range)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  long-miss rate:   %.1f%%", v.LongMissRate*100)
	if v.LongMissRate <= 0.31 {
		b.WriteString(" (human range)")
	} else {
		b.WriteString(" (AI range)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  pass hit rates:  ")
	for i, h := range v.PassHits {
		if i > 0 {
			b.WriteString(" → ")
		}
		fmt.Fprintf(&b, "%.0f%%", h*100)
	}
	b.WriteByte('\n')
	if v.TrollLabel != "" {
		fmt.Fprintf(&b, "  troll match:     %.1f%%\n", v.TrollScore*100)
	}
	return b.String()
}

func avgWalk(events []grow.Event) float64 {
	var total int64
	var n int64
	for _, ev := range events {
		if ev.Type == grow.EventBonded {
			total += int64(ev.Steps)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(total) / float64(n)
}

func longMissFraction(events []grow.Event) float64 {
	var totalMiss, longMiss int64
	for _, ev := range events {
		if ev.Type != grow.EventBonded {
			tag := "unk"
			if ev.Element != nil {
				tag = ev.Element.Type()
			}
			// Classify the event to get the miss tag.
			classified := ClassifyEvent(ev)
			ct := classified.Type()
			if strings.HasSuffix(ct, ".miss") {
				totalMiss++
				// Extract base type before .miss
				base := strings.TrimSuffix(ct, ".miss")
				_ = tag // use classified tag, not raw
				if base == "w4" || base == "w5" {
					longMiss++
				}
			}
		}
	}
	if totalMiss == 0 {
		return 0
	}
	return float64(longMiss) / float64(totalMiss)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
