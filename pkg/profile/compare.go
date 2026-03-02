package profile

import (
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// Verdict is the output of comparing a sample's stats against a baseline.
type Verdict struct {
	// HumanProbability estimates how likely the text is human-written.
	// Range [0, 1] as a rational number.
	HumanProbability ratio.Ratio `json:"human_probability"`

	// Confidence measures how much signal the comparison found.
	// Low confidence means the sample was too short or the baseline
	// was insufficiently trained.
	Confidence ratio.Ratio `json:"confidence"`

	// Breakdown shows each metric's contribution to the verdict.
	Breakdown map[string]ratio.Ratio `json:"breakdown"`

	// ModelMatch is the name of the best-matching model fingerprint,
	// if model comparison was performed. Empty if no model match.
	ModelMatch string `json:"model_match,omitempty"`
}

// metricWeight defines the weight of a single metric in the comparison.
type metricWeight struct {
	name   string
	weight ratio.Ratio
}

// defaultWeights are the initial metric weights for comparison.
// Only scale-independent metrics are used: metrics that are already
// normalized per-token or per-bond, so a 200-token sample can be
// compared against a 2M-token baseline without distortion.
//
// Scale-dependent metrics (vertex_coverage, new_vertex_rate) are excluded
// because they grow with sample size and produce false divergence when
// comparing short samples against long baselines.
var defaultWeights = []metricWeight{
	{"bond_rate", ratio.New(25, 100)},          // bonds/tokens — does the text fit the lattice?
	{"avg_walk_distance", ratio.New(25, 100)},  // steps/bond — how easily does it bond?
	{"transition_entropy", ratio.New(25, 100)}, // bigram entropy — are transitions natural?
	{"path_entropy", ratio.New(15, 100)},       // node entropy — diverse bonding?
	{"surprisal_variance", ratio.New(10, 100)}, // surprisal spread — bursty or smooth?
}

// Compare compares a sample's stats against a baseline to produce a verdict.
//
// The comparison works by measuring how far each metric deviates from the
// baseline. Human text on a human-trained lattice should produce stats close
// to the baseline. AI text should deviate systematically: lower entropy,
// lower surprisal variance, lower burstiness, etc.
//
// The deviation direction matters:
//   - path_entropy: lower than baseline → more AI-like
//   - surprisal_variance: lower → more AI-like
//   - burstiness_gini: lower → more AI-like
//   - vertex_coverage: deviation in either direction → suspicious
//   - new_vertex_rate: higher → text contains patterns not in baseline
//   - avg_walk_distance: higher → text doesn't fit the baseline well
func Compare(baseline, sample Stats) Verdict {
	v := Verdict{
		Breakdown: make(map[string]ratio.Ratio, len(defaultWeights)),
	}

	// For each metric, compute a similarity score in [0, 1].
	// 1 = identical to baseline, 0 = maximally divergent.
	scores := make(map[string]ratio.Ratio, len(defaultWeights))

	scores["bond_rate"] = similarity(baseline.BondRate, sample.BondRate)
	scores["avg_walk_distance"] = similarity(baseline.AvgWalkDistance, sample.AvgWalkDistance)
	scores["transition_entropy"] = similarity(baseline.TransitionEntropy, sample.TransitionEntropy)
	scores["path_entropy"] = similarity(baseline.PathEntropy, sample.PathEntropy)
	scores["surprisal_variance"] = similarity(baseline.SurprisalVariance, sample.SurprisalVariance)

	// Weighted sum.
	weightedSum := ratio.Zero
	for _, mw := range defaultWeights {
		score := scores[mw.name]
		contribution := mw.weight.Mul(score)
		v.Breakdown[mw.name] = score
		weightedSum = weightedSum.Add(contribution)
	}

	v.HumanProbability = weightedSum.Clamp(ratio.Zero, ratio.One)

	// Confidence is based on whether the baseline had enough data.
	// More tokens in the baseline → higher confidence.
	// This is a rough heuristic; calibration will refine it.
	v.Confidence = ratio.One // placeholder until calibration

	return v
}

// CompareModels compares a sample against multiple model fingerprints.
// Returns the best-matching model name and its similarity score.
func CompareModels(sample Stats, models map[string]Stats) (bestModel string, bestScore ratio.Ratio) {
	bestScore = ratio.Zero
	for name, modelStats := range models {
		// For model matching, we want HIGH similarity to the model's profile.
		score := modelSimilarity(sample, modelStats)
		if score.Greater(bestScore) {
			bestScore = score
			bestModel = name
		}
	}
	return
}

// similarity computes a similarity score between two metric values.
// Returns a ratio in [0, 1] where 1 means identical.
// Uses absolute relative difference: 1 - |a - b| / max(|a|, |b|, 1).
func similarity(baseline, sample ratio.Ratio) ratio.Ratio {
	diff := baseline.Sub(sample).Abs()
	denom := ratio.Max(baseline.Abs(), sample.Abs())
	denom = ratio.Max(denom, ratio.One) // avoid division by zero
	relDiff := diff.Div(denom)
	result := ratio.One.Sub(relDiff)
	return result.Clamp(ratio.Zero, ratio.One)
}

// modelSimilarity computes overall similarity between sample and model stats.
func modelSimilarity(sample, model Stats) ratio.Ratio {
	total := ratio.Zero
	n := ratio.Zero
	total = total.Add(similarity(sample.PathEntropy, model.PathEntropy))
	total = total.Add(similarity(sample.SurprisalVariance, model.SurprisalVariance))
	total = total.Add(similarity(sample.BurstinessGini, model.BurstinessGini))
	total = total.Add(similarity(sample.VertexCoverage, model.VertexCoverage))
	total = total.Add(similarity(sample.AvgWalkDistance, model.AvgWalkDistance))
	total = total.Add(similarity(sample.TransitionEntropy, model.TransitionEntropy))
	n = ratio.FromInt(6)
	return total.Div(n)
}
