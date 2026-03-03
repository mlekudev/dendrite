// Package hadamard implements the 8-point Walsh-Hadamard Transform using
// the butterfly decomposition of H_8 = H_2 ⊗ H_2 ⊗ H_2.
//
// All operations use integer arithmetic. The transform consists of
// 3 stages of paired additions/subtractions (24 total), no multiplications.
// H_8 is self-inverse up to a scale factor of 8: H_8 * H_8 = 8 * I.
//
// The 8 channels correspond to the 8 trigrams of the I Ching, which
// encode 3 binary axes (Bonding, Constraint, Energy). This is the same
// structure as the 3 butterfly stages of H_8.
package hadamard

// Vec8 is an 8-element integer vector aligned to the 8 token channels.
type Vec8 [8]int64

// Channel indices. The mapping to token types and trigrams:
//
//	0: w1     (Earth    000) — 1-char words
//	1: w2     (Thunder  001) — 2-3 char words
//	2: w3     (Water    010) — 4-5 char words
//	3: w4     (Lake     011) — 6-8 char words
//	4: w5     (Fire     100) — 9+ char words
//	5: punct  (Heaven   101) — punctuation
//	6: space  (Wind     110) — whitespace
//	7: origin (Mountain 111) — author identity/provenance
const (
	ChanW1     = 0
	ChanW2     = 1
	ChanW3     = 2
	ChanW4     = 3
	ChanW5     = 4
	ChanPunct  = 5
	ChanSpace  = 6
	ChanOrigin = 7
	NumChans   = 8
)

// ChanName maps channel index to tag string.
var ChanName = [NumChans]string{
	"w1", "w2", "w3", "w4", "w5", "punct", "space", "origin",
}

// ChanIndex returns the channel index for a token type tag, or -1.
func ChanIndex(tag string) int {
	switch tag {
	case "w1":
		return ChanW1
	case "w2":
		return ChanW2
	case "w3":
		return ChanW3
	case "w4":
		return ChanW4
	case "w5":
		return ChanW5
	case "punct":
		return ChanPunct
	case "space":
		return ChanSpace
	case "origin":
		return ChanOrigin
	}
	return -1
}

// Transform applies H_8 in-place via the butterfly decomposition.
//
// Three stages, each applying H_2 = [[1,1],[1,-1]] to pairs at
// increasing stride. 24 additions/subtractions, 0 multiplications.
//
//	Stage 1: stride 1 (H_2 on bit axis 0: Bonding)
//	Stage 2: stride 2 (H_2 on bit axis 1: Constraint)
//	Stage 3: stride 4 (H_2 on bit axis 2: Energy)
func Transform(v *Vec8) {
	// Stage 1: pairs (0,1), (2,3), (4,5), (6,7)
	for i := 0; i < 8; i += 2 {
		a, b := v[i], v[i+1]
		v[i] = a + b
		v[i+1] = a - b
	}
	// Stage 2: pairs (0,2), (1,3), (4,6), (5,7)
	for i := 0; i < 8; i += 4 {
		for j := 0; j < 2; j++ {
			a, b := v[i+j], v[i+j+2]
			v[i+j] = a + b
			v[i+j+2] = a - b
		}
	}
	// Stage 3: pairs (0,4), (1,5), (2,6), (3,7)
	for j := 0; j < 4; j++ {
		a, b := v[j], v[j+4]
		v[j] = a + b
		v[j+4] = a - b
	}
}

// InverseTransform applies H_8^{-1}. Since H_8^2 = 8*I, the inverse
// is (1/8)*H_8. We apply Transform and leave the caller to account
// for the factor of 8 (integer division or tracking the scale).
func InverseTransform(v *Vec8) {
	Transform(v)
}

// Add returns a + b element-wise.
func Add(a, b Vec8) Vec8 {
	return Vec8{
		a[0] + b[0], a[1] + b[1], a[2] + b[2], a[3] + b[3],
		a[4] + b[4], a[5] + b[5], a[6] + b[6], a[7] + b[7],
	}
}

// Sub returns a - b element-wise.
func Sub(a, b Vec8) Vec8 {
	return Vec8{
		a[0] - b[0], a[1] - b[1], a[2] - b[2], a[3] - b[3],
		a[4] - b[4], a[5] - b[5], a[6] - b[6], a[7] - b[7],
	}
}

// L1Norm returns the sum of absolute values.
func L1Norm(v Vec8) int64 {
	var s int64
	for _, x := range v {
		if x < 0 {
			s -= x
		} else {
			s += x
		}
	}
	return s
}

// DC returns the sum of all elements (Walsh component 0) without
// performing the full transform.
func DC(v Vec8) int64 {
	return v[0] + v[1] + v[2] + v[3] + v[4] + v[5] + v[6] + v[7]
}

// DetailEnergy returns the L1 norm of Walsh components 1..7 (everything
// except the DC component). Requires an already-transformed vector.
func DetailEnergy(walsh Vec8) int64 {
	var s int64
	for i := 1; i < NumChans; i++ {
		if walsh[i] < 0 {
			s -= walsh[i]
		} else {
			s += walsh[i]
		}
	}
	return s
}

// ErrorDetect checks whether an observed vector deviates from a reference
// (trained) vector and identifies which channel was perturbed.
//
// This uses the Hadamard transform's error-concentrating property: a single-
// channel perturbation in the spatial domain spreads evenly across all Walsh
// coefficients, but the difference vector has energy concentrated in one
// position. By comparing observed vs reference in spatial domain, the
// corrupted channel is the one with the largest absolute residual.
//
// Returns the index of the suspected corrupted channel (-1 if clean)
// and the magnitude of the residual.
func ErrorDetect(observed, reference Vec8) (channel int, residual int64) {
	diff := Sub(observed, reference)
	if IsZero(diff) {
		return -1, 0
	}

	maxIdx := 0
	maxAbs := int64(0)
	for i := range NumChans {
		a := diff[i]
		if a < 0 {
			a = -a
		}
		if a > maxAbs {
			maxAbs = a
			maxIdx = i
		}
	}
	return maxIdx, maxAbs
}

// Correct returns a copy of observed with the identified channel replaced
// by the reference value for that channel.
func Correct(observed, reference Vec8, channel int) Vec8 {
	corrected := observed
	if channel >= 0 && channel < NumChans {
		corrected[channel] = reference[channel]
	}
	return corrected
}

// Scale multiplies every element by s.
func Scale(v Vec8, s int64) Vec8 {
	return Vec8{
		v[0] * s, v[1] * s, v[2] * s, v[3] * s,
		v[4] * s, v[5] * s, v[6] * s, v[7] * s,
	}
}

// IsZero returns true if all elements are zero.
func IsZero(v Vec8) bool {
	return v == Vec8{}
}
