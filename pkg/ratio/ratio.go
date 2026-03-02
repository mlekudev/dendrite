// Package ratio implements exact rational arithmetic using integer
// numerator/denominator pairs. All values are stored in canonical form:
// GCD-reduced with positive denominator. This guarantees deterministic
// JSON serialization — two Ratios representing the same value always
// produce identical bytes.
package ratio

import "fmt"

// Ratio is an exact rational number Num/Denom.
// The zero value (0/0) is not valid; use Zero instead.
// All constructors and arithmetic operations return normalized values.
type Ratio struct {
	Num   int64 `json:"num"`
	Denom int64 `json:"denom"`
}

// Predefined constants.
var (
	Zero = Ratio{0, 1}
	One  = Ratio{1, 1}
	Half = Ratio{1, 2}
)

// New creates a normalized Ratio. Panics if denom is zero.
func New(num, denom int64) Ratio {
	if denom == 0 {
		panic("ratio: zero denominator")
	}
	return normalize(num, denom)
}

// FromInt returns n/1.
func FromInt(n int64) Ratio {
	return Ratio{n, 1}
}

// normalize reduces num/denom by GCD and ensures positive denominator.
func normalize(num, denom int64) Ratio {
	if num == 0 {
		return Ratio{0, 1}
	}
	if denom < 0 {
		num = -num
		denom = -denom
	}
	g := gcd(abs(num), denom)
	return Ratio{num / g, denom / g}
}

// Add returns r + other.
func (r Ratio) Add(other Ratio) Ratio {
	return normalize(
		r.Num*other.Denom+other.Num*r.Denom,
		r.Denom*other.Denom,
	)
}

// Sub returns r - other.
func (r Ratio) Sub(other Ratio) Ratio {
	return normalize(
		r.Num*other.Denom-other.Num*r.Denom,
		r.Denom*other.Denom,
	)
}

// Mul returns r * other.
func (r Ratio) Mul(other Ratio) Ratio {
	return normalize(
		r.Num*other.Num,
		r.Denom*other.Denom,
	)
}

// Div returns r / other. Panics if other is zero.
func (r Ratio) Div(other Ratio) Ratio {
	if other.Num == 0 {
		panic("ratio: division by zero")
	}
	return normalize(
		r.Num*other.Denom,
		r.Denom*other.Num,
	)
}

// Neg returns -r.
func (r Ratio) Neg() Ratio {
	return Ratio{-r.Num, r.Denom}
}

// Abs returns |r|.
func (r Ratio) Abs() Ratio {
	if r.Num < 0 {
		return Ratio{-r.Num, r.Denom}
	}
	return r
}

// Less returns true if r < other.
// Safe because denominators are always positive after normalization.
func (r Ratio) Less(other Ratio) bool {
	return r.Num*other.Denom < other.Num*r.Denom
}

// LessEq returns true if r <= other.
func (r Ratio) LessEq(other Ratio) bool {
	return r.Num*other.Denom <= other.Num*r.Denom
}

// Greater returns true if r > other.
func (r Ratio) Greater(other Ratio) bool {
	return other.Less(r)
}

// Equal returns true if r == other.
// Both must be normalized for this to work (they always are).
func (r Ratio) Equal(other Ratio) bool {
	return r.Num == other.Num && r.Denom == other.Denom
}

// IsZero returns true if r == 0.
func (r Ratio) IsZero() bool {
	return r.Num == 0
}

// IsPositive returns true if r > 0.
func (r Ratio) IsPositive() bool {
	return r.Num > 0
}

// IsNegative returns true if r < 0.
func (r Ratio) IsNegative() bool {
	return r.Num < 0
}

// Float64 converts to float64 for display purposes only.
// Never use the result for computation.
func (r Ratio) Float64() float64 {
	if r.Denom == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Denom)
}

// ScaleInt computes (n * Num) / Denom using integer arithmetic.
// This replaces patterns like int(float64(n) * proportion).
func (r Ratio) ScaleInt(n int64) int64 {
	return (n * r.Num) / r.Denom
}

// Max returns the larger of r and other.
func Max(a, b Ratio) Ratio {
	if b.Less(a) {
		return a
	}
	return b
}

// Min returns the smaller of r and other.
func Min(a, b Ratio) Ratio {
	if a.Less(b) {
		return a
	}
	return b
}

// Clamp returns r clamped to [lo, hi].
func (r Ratio) Clamp(lo, hi Ratio) Ratio {
	if r.Less(lo) {
		return lo
	}
	if hi.Less(r) {
		return hi
	}
	return r
}

// String returns "Num/Denom" for debugging.
func (r Ratio) String() string {
	return fmt.Sprintf("%d/%d", r.Num, r.Denom)
}

// gcd returns the greatest common divisor of a and b.
// Both arguments must be non-negative.
func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// abs returns the absolute value of n.
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
