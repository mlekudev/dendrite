package profile

import (
	"sort"

	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
)

// Stats holds derived statistical measures computed from a Profile.
// All values use exact rational arithmetic for determinism.
type Stats struct {
	// PathEntropy is the Shannon entropy of the path frequency distribution.
	// Higher means more diverse bonding across the lattice.
	PathEntropy ratio.Ratio `json:"path_entropy"`

	// SurprisalVariance is the variance of -log2(p) across bonded paths.
	// Human text has higher variance (bursty surprisal); AI text is smoother.
	SurprisalVariance ratio.Ratio `json:"surprisal_variance"`

	// BurstinessGini is the Gini coefficient of bond counts across nodes.
	// Measures inequality in how bonds distribute across the lattice.
	// Human text is burstier (higher Gini); AI text distributes more evenly.
	BurstinessGini ratio.Ratio `json:"burstiness_gini"`

	// VertexCoverage is the fraction of lattice nodes that received at least
	// one bond during the growth session.
	VertexCoverage ratio.Ratio `json:"vertex_coverage"`

	// AvgWalkDistance is the mean number of walk steps before bonding.
	AvgWalkDistance ratio.Ratio `json:"avg_walk_distance"`

	// BondRate is bonds / tokens ingested.
	BondRate ratio.Ratio `json:"bond_rate"`

	// NewVertexRate is new vertices / tokens ingested.
	NewVertexRate ratio.Ratio `json:"new_vertex_rate"`

	// TransitionEntropy is the Shannon entropy of the bigram transition
	// frequency distribution. Measures sequential diversity.
	TransitionEntropy ratio.Ratio `json:"transition_entropy"`
}

// Compute derives statistics from a raw profile.
// latticeSize is the total number of nodes in the lattice at time of profiling.
func Compute(p *Profile, latticeSize int) Stats {
	var s Stats

	if p.TokensIngested == 0 {
		return s
	}

	// Bond rate.
	s.BondRate = ratio.New(p.BondEvents, p.TokensIngested)

	// New vertex rate.
	s.NewVertexRate = ratio.New(p.NewVertices, p.TokensIngested)

	// Vertex coverage.
	if latticeSize > 0 {
		s.VertexCoverage = ratio.New(int64(len(p.PathFreq)), int64(latticeSize))
	}

	// Average walk distance.
	if p.BondEvents > 0 {
		totalSteps := int64(0)
		for steps, count := range p.WalkDistHist {
			totalSteps += int64(steps) * count
		}
		s.AvgWalkDistance = ratio.New(totalSteps, p.BondEvents)
	}

	// Path entropy and surprisal variance.
	s.PathEntropy, s.SurprisalVariance = computeEntropyAndVariance(p.PathFreq, p.BondEvents)

	// Burstiness (Gini coefficient of path frequencies).
	s.BurstinessGini = computeGini(p.PathFreq)

	// Transition entropy.
	totalTransitions := int64(0)
	transitionCounts := make(map[[2]string]int64, len(p.TransitionFreq))
	for k, v := range p.TransitionFreq {
		transitionCounts[k] = v
		totalTransitions += v
	}
	if totalTransitions > 0 {
		// Convert to a flat frequency map for entropy computation.
		flatFreq := make(map[int]int64, len(transitionCounts))
		idx := 0
		for _, v := range transitionCounts {
			flatFreq[idx] = v
			idx++
		}
		s.TransitionEntropy, _ = computeEntropyAndVarianceGeneric(flatFreq, totalTransitions)
	}

	return s
}

// computeEntropyAndVariance computes Shannon entropy and surprisal variance
// from a frequency distribution. Uses rational arithmetic throughout.
//
// Entropy = -sum(p_i * log2(p_i)) approximated as:
// H = log2(N) - (1/N) * sum(f_i * log2(f_i))
// where f_i are frequencies and N = sum(f_i).
//
// Since exact log2 of rationals produces irrationals, we approximate using
// integer log2 (floor). This introduces bounded error but preserves the
// comparison ordering: distributions with higher true entropy will have
// higher approximated entropy.
func computeEntropyAndVariance(freqs map[lattice.NodeID]int64, total int64) (entropy, variance ratio.Ratio) {
	if total == 0 || len(freqs) == 0 {
		return ratio.Zero, ratio.Zero
	}

	// Compute entropy approximation: log2(total) - (1/total) * sum(f * log2(f))
	totalR := ratio.FromInt(total)
	log2Total := log2Scaled(total)

	sumFLogF := ratio.Zero
	for _, f := range freqs {
		if f > 0 {
			sumFLogF = sumFLogF.Add(ratio.FromInt(f).Mul(log2Scaled(f)))
		}
	}

	entropy = log2Total.Sub(sumFLogF.Div(totalR))
	if entropy.IsNegative() {
		entropy = ratio.Zero
	}

	// Surprisal variance: Var(-log2(p_i)) where p_i = f_i/total.
	// Mean surprisal is entropy H.
	// Var = E[S^2] - H^2 where S_i = -log2(p_i) = log2(total) - log2(f_i).
	sumS2 := ratio.Zero
	for _, f := range freqs {
		if f > 0 {
			s := log2Total.Sub(log2Scaled(f))
			s2 := s.Mul(s)
			p := ratio.New(f, total)
			sumS2 = sumS2.Add(p.Mul(s2))
		}
	}
	variance = sumS2.Sub(entropy.Mul(entropy))
	if variance.IsNegative() {
		variance = ratio.Zero
	}

	return entropy, variance
}

// computeEntropyAndVarianceGeneric is the same as computeEntropyAndVariance
// but works with int-keyed maps (for transition frequencies).
func computeEntropyAndVarianceGeneric(freqs map[int]int64, total int64) (entropy, variance ratio.Ratio) {
	if total == 0 || len(freqs) == 0 {
		return ratio.Zero, ratio.Zero
	}

	totalR := ratio.FromInt(total)
	log2Total := log2Scaled(total)

	sumFLogF := ratio.Zero
	for _, f := range freqs {
		if f > 0 {
			sumFLogF = sumFLogF.Add(ratio.FromInt(f).Mul(log2Scaled(f)))
		}
	}

	entropy = log2Total.Sub(sumFLogF.Div(totalR))
	if entropy.IsNegative() {
		entropy = ratio.Zero
	}

	sumS2 := ratio.Zero
	for _, f := range freqs {
		if f > 0 {
			s := log2Total.Sub(log2Scaled(f))
			s2 := s.Mul(s)
			p := ratio.New(f, total)
			sumS2 = sumS2.Add(p.Mul(s2))
		}
	}
	variance = sumS2.Sub(entropy.Mul(entropy))
	if variance.IsNegative() {
		variance = ratio.Zero
	}

	return entropy, variance
}

// computeGini computes the Gini coefficient of a frequency distribution.
// Gini = (2 * sum_i(i * x_i)) / (n * sum(x_i)) - (n+1)/n
// where x_i are the sorted values and i is 1-based rank.
func computeGini(freqs map[lattice.NodeID]int64) ratio.Ratio {
	if len(freqs) == 0 {
		return ratio.Zero
	}

	// Extract and sort values.
	vals := make([]int64, 0, len(freqs))
	for _, v := range freqs {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	n := int64(len(vals))
	total := int64(0)
	weightedSum := int64(0)
	for i, v := range vals {
		total += v
		weightedSum += int64(i+1) * v
	}

	if total == 0 || n == 0 {
		return ratio.Zero
	}

	// Gini = (2 * weightedSum) / (n * total) - (n + 1) / n
	term1 := ratio.New(2*weightedSum, n*total)
	term2 := ratio.New(n+1, n)
	g := term1.Sub(term2)
	if g.IsNegative() {
		return ratio.Zero
	}
	return g
}

// log2Scale is the fixed-point scaling factor for log2 computations.
// Using 1024 (2^10) gives 10 bits of fractional precision while
// keeping all arithmetic in exact rationals.
const log2Scale = 1024

// log2Scaled returns an approximation of log2(n) as a ratio with
// denominator log2Scale. For n > 0, computes floor(log2(n)) as the
// integer part, then refines the fractional part using bisection
// on the residual: after extracting the integer bits k such that
// 2^k <= n < 2^(k+1), the fractional part is log2(n / 2^k) which
// lies in [0, 1). We approximate this by testing whether
// n^2 >= 2^(2k+1) repeatedly at increasing resolution.
//
// Returns ratio.Zero for n <= 0.
func log2Scaled(n int64) ratio.Ratio {
	if n <= 0 {
		return ratio.Zero
	}
	if n == 1 {
		return ratio.Zero
	}

	// Integer part: floor(log2(n)).
	k := int64(0)
	v := n
	for v > 1 {
		v >>= 1
		k++
	}

	// Fractional part via repeated squaring / bisection.
	// We compute frac * log2Scale as an integer.
	// Start with remainder r = n, base = 2^k.
	// At each step, square r, check if r^2 >= base*2, if so
	// the next bit of the fractional log is 1.
	frac := int64(0)
	// r represents n / 2^k as a fraction in [1, 2).
	// We track r_num / r_den where initially r = n / 2^k.
	rNum := n
	rDen := int64(1) << k
	if rDen <= 0 {
		// Overflow protection for very large k.
		return ratio.New(k*log2Scale, log2Scale)
	}

	for bit := int64(log2Scale / 2); bit > 0; bit >>= 1 {
		// Square: r = r^2 / 2 (normalize to keep in [1, 2) range).
		// Actually: r' = r^2. If r' >= 2, then this fractional bit is 1
		// and r' = r' / 2.
		rNum = rNum * rNum
		rDen = rDen * rDen

		// Check overflow — if numbers get too big, stop refining.
		if rNum < 0 || rDen < 0 {
			break
		}

		// If r^2 >= 2 (i.e., rNum/rDen >= 2, i.e., rNum >= 2*rDen):
		if rNum >= 2*rDen {
			frac += bit
			// r = r / 2: keep rNum, double rDen.
			rDen *= 2
		}
	}

	return ratio.New(k*log2Scale+frac, log2Scale)
}
