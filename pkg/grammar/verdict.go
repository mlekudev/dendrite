package grammar

import (
	"fmt"
	"strings"
)

// Verdict is the output of the 8-pass recognition chain.
// It distills per-pass metrics into a single judgment.
type Verdict struct {
	// Human is true if the text appears to be human-written.
	Human bool

	// Confidence ranges from 0 (uncertain) to 1 (certain).
	Confidence float64

	// DeepWalk is the average normalized Walsh shape distance at the deepest
	// pass. This measures how the probe text's Walsh distribution pattern
	// deviates from the trained human text profile.
	// Human text: median ~0.009, AI text: median ~0.012.
	DeepWalk float64

	// LongMissRate is the fraction of missed channels at the deepest pass
	// that are long-word channels (w4+w5).
	// Human text: median ~0.00, AI text: median ~0.25.
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

// PassStats holds per-pass detection statistics for scoring.
type PassStats struct {
	Bonded        int64
	Missed        int64
	Total         int64
	LongMisses    int64   // misses on w4+w5
	TotalWalkDist float64 // sum of walk distances (proportional, ~1.0-2.0 each)
}

// Score computes a Verdict from multi-pass detection results.
// passStats[0] is pass 1 (leaf/word level), passStats[N-1] is the deepest
// pass (root level). DeepWalk and LongMissRate come from the deepest pass.
func Score(passStats []PassStats) Verdict {
	if len(passStats) == 0 {
		return Verdict{Label: "no data"}
	}

	v := Verdict{
		PassHits: make([]float64, len(passStats)),
	}

	for i, ps := range passStats {
		if ps.Total > 0 {
			v.PassHits[i] = float64(ps.Bonded) / float64(ps.Total)
		}
	}

	deep := passStats[len(passStats)-1]
	if deep.Total > 0 {
		v.DeepWalk = deep.TotalWalkDist / float64(deep.Total)
	}
	if deep.Missed > 0 {
		v.LongMissRate = float64(deep.LongMisses) / float64(deep.Missed)
	}

	// Walk distance boundary: 0.110 (empirically calibrated).
	walkVote := 0.0
	walkCenter := 0.110
	if v.DeepWalk < walkCenter {
		walkVote = (walkCenter - v.DeepWalk) / walkCenter * -1
	} else {
		walkVote = (v.DeepWalk - walkCenter) / walkCenter
	}

	// Long-miss boundary: 0.18 (empirically calibrated).
	missVote := 0.0
	missCenter := 0.18
	if v.LongMissRate < missCenter {
		missVote = (missCenter - v.LongMissRate) / missCenter * -1
	} else {
		missVote = (v.LongMissRate - missCenter) / missCenter
	}

	combined := (walkVote + missVote) / 2
	v.Human = combined < 0
	v.Confidence = clamp(abs(combined)*3, 0, 1)

	if v.Human {
		v.Label = fmt.Sprintf("HUMAN (%.0f%%)", v.Confidence*100)
	} else {
		v.Label = fmt.Sprintf("AI (%.0f%%)", v.Confidence*100)
	}

	return v
}

// ProbeFeatures holds features extracted from the probe text that
// contribute to the scoring decision alongside per-level statistics.
type ProbeFeatures struct {
	// PunctRatio is the fraction of tokens that are punctuation.
	// Human text: mean ~0.105, AI text: mean ~0.068.
	PunctRatio float64

	// LongWordRatio is the fraction of tokens that are long words (w4+w5).
	// Human text: mean ~0.134, AI text: mean ~0.180.
	LongWordRatio float64
}

// Per-level walk distance boundaries (midpoint of human/AI medians).
// passStats index 0 = pass 1 = depth 7 (leaf), index 3 = pass 4 = depth 4.
var levelWalkBoundary = [8]float64{
	0.093, // pass 1 (depth 7, leaf): human 0.086, AI 0.101
	0.125, // pass 2 (depth 6): human 0.111, AI 0.139
	0.130, // pass 3 (depth 5): human 0.116, AI 0.144
	0.138, // pass 4 (depth 4): human 0.129, AI 0.147
	0.140, // passes 5-8: rarely populated, use conservative value
	0.140,
	0.140,
	0.140,
}

// Per-level bond rate boundaries (midpoint of human/AI means).
var levelBondBoundary = [8]float64{
	0.733, // pass 1 (depth 7): human 0.779, AI 0.688
	0.697, // pass 2 (depth 6): human 0.746, AI 0.649
	0.673, // pass 3 (depth 5): human 0.725, AI 0.622
	0.638, // pass 4 (depth 4): human 0.677, AI 0.600
	0.650, // passes 5-8: conservative
	0.650,
	0.650,
	0.650,
}

// ScoreWalsh computes a Verdict using four voters:
//  1. Multi-level walk distance (per-level boundaries)
//  2. Multi-level bond rate (channel match rate at each level)
//  3. Punctuation ratio (human text has more punctuation)
//  4. Long-miss rate (fraction of misses on long-word channels)
func ScoreWalsh(passStats []PassStats, features ProbeFeatures) Verdict {
	if len(passStats) == 0 {
		return Verdict{Label: "no data"}
	}

	v := Verdict{
		PassHits: make([]float64, len(passStats)),
	}

	for i, ps := range passStats {
		if ps.Total > 0 {
			v.PassHits[i] = float64(ps.Bonded) / float64(ps.Total)
		}
	}

	// Per-level walk and bond voting.
	var walkVoteSum, bondVoteSum float64
	var missSum, longMissSum int64
	var nLevels int
	var walkSum float64

	for i, ps := range passStats {
		if ps.Total == 0 {
			continue
		}
		nLevels++
		walkSum += ps.TotalWalkDist
		missSum += ps.Missed
		longMissSum += ps.LongMisses

		// Walk vote: below boundary → human (negative).
		wb := levelWalkBoundary[i]
		if ps.TotalWalkDist < wb {
			walkVoteSum += (wb - ps.TotalWalkDist) / wb * -1
		} else {
			walkVoteSum += (ps.TotalWalkDist - wb) / wb
		}

		// Bond vote: above boundary → human (negative).
		// Higher bond rates mean the probe matches training better.
		bondRate := float64(ps.Bonded) / float64(ps.Total)
		bb := levelBondBoundary[i]
		if bondRate > bb {
			bondVoteSum += (bondRate - bb) / bb * -1
		} else {
			bondVoteSum += (bb - bondRate) / bb
		}
	}

	// Average votes across populated levels.
	walkVote := 0.0
	bondVote := 0.0
	if nLevels > 0 {
		walkVote = walkVoteSum / float64(nLevels)
		bondVote = bondVoteSum / float64(nLevels)
		v.DeepWalk = walkSum / float64(nLevels)
	}
	if missSum > 0 {
		v.LongMissRate = float64(longMissSum) / float64(missSum)
	}

	// Long-miss boundary: 0.11 (optimized; human median 0.118, AI median 0.250).
	missVote := 0.0
	missCenter := 0.11
	if v.LongMissRate < missCenter {
		missVote = (missCenter - v.LongMissRate) / missCenter * -1
	} else {
		missVote = (v.LongMissRate - missCenter) / missCenter
	}

	// Punctuation ratio: human ~0.105, AI ~0.068. Boundary ~0.080.
	// Higher punct → human (negative vote).
	punctVote := 0.0
	punctCenter := 0.080
	if features.PunctRatio > punctCenter {
		punctVote = (features.PunctRatio - punctCenter) / punctCenter * -1
	} else {
		punctVote = (punctCenter - features.PunctRatio) / punctCenter
	}

	// Four-voter combined score. Punct is the strongest discriminator.
	// Walk: 20%, Bond: 20%, Punct: 55%, Miss: 5%.
	combined := walkVote*0.20 + bondVote*0.20 + punctVote*0.55 + missVote*0.05
	v.Human = combined < 0
	v.Confidence = clamp(abs(combined)*3, 0, 1)

	if v.Human {
		v.Label = fmt.Sprintf("HUMAN (%.0f%%)", v.Confidence*100)
	} else {
		v.Label = fmt.Sprintf("AI (%.0f%%)", v.Confidence*100)
	}

	return v
}

// ScoreTroll computes the troll dimension of a verdict from manipulation
// detection statistics.
func ScoreTroll(v *Verdict, trollStats []PassStats) {
	if len(trollStats) == 0 {
		return
	}

	deep := trollStats[len(trollStats)-1]
	if deep.Total == 0 {
		return
	}

	deepRate := float64(deep.Bonded) / float64(deep.Total)
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
	fmt.Fprintf(&b, "  deep walk avg:    %.4f", v.DeepWalk)
	if v.DeepWalk <= 0.120 {
		b.WriteString(" (human range)")
	} else {
		b.WriteString(" (AI range)")
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  long-miss rate:   %.1f%%", v.LongMissRate*100)
	if v.LongMissRate <= 0.18 {
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
