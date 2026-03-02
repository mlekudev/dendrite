package ratio

import (
	"encoding/json"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		num, denom int64
		wantNum    int64
		wantDenom  int64
	}{
		{6, 4, 3, 2},
		{-6, 4, -3, 2},
		{6, -4, -3, 2},
		{-6, -4, 3, 2},
		{0, 5, 0, 1},
		{7, 1, 7, 1},
		{100, 100, 1, 1},
		{15, 100, 3, 20},
		{5, 100, 1, 20},
		{80, 100, 4, 5},
	}
	for _, tt := range tests {
		r := New(tt.num, tt.denom)
		if r.Num != tt.wantNum || r.Denom != tt.wantDenom {
			t.Errorf("New(%d, %d) = %d/%d, want %d/%d",
				tt.num, tt.denom, r.Num, r.Denom, tt.wantNum, tt.wantDenom)
		}
	}
}

func TestNewPanicsOnZeroDenom(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New(1, 0) did not panic")
		}
	}()
	New(1, 0)
}

func TestFromInt(t *testing.T) {
	r := FromInt(42)
	if r.Num != 42 || r.Denom != 1 {
		t.Errorf("FromInt(42) = %d/%d, want 42/1", r.Num, r.Denom)
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		a, b Ratio
		want Ratio
	}{
		{New(1, 2), New(1, 3), New(5, 6)},
		{New(1, 4), New(1, 4), New(1, 2)},
		{New(1, 2), Zero, New(1, 2)},
		{New(1, 2), New(-1, 2), Zero},
		{New(3, 7), New(2, 7), New(5, 7)},
	}
	for _, tt := range tests {
		got := tt.a.Add(tt.b)
		if !got.Equal(tt.want) {
			t.Errorf("%s + %s = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSub(t *testing.T) {
	tests := []struct {
		a, b Ratio
		want Ratio
	}{
		{New(1, 2), New(1, 3), New(1, 6)},
		{New(1, 2), New(1, 2), Zero},
		{New(1, 4), New(3, 4), New(-1, 2)},
	}
	for _, tt := range tests {
		got := tt.a.Sub(tt.b)
		if !got.Equal(tt.want) {
			t.Errorf("%s - %s = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMul(t *testing.T) {
	tests := []struct {
		a, b Ratio
		want Ratio
	}{
		{New(2, 3), New(3, 4), New(1, 2)},
		{New(1, 2), One, New(1, 2)},
		{New(5, 7), Zero, Zero},
		{New(-1, 3), New(-1, 3), New(1, 9)},
		{New(3, 20), New(7, 10), New(21, 200)},
	}
	for _, tt := range tests {
		got := tt.a.Mul(tt.b)
		if !got.Equal(tt.want) {
			t.Errorf("%s * %s = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDiv(t *testing.T) {
	tests := []struct {
		a, b Ratio
		want Ratio
	}{
		{New(1, 2), New(1, 3), New(3, 2)},
		{One, New(2, 1), New(1, 2)},
		{New(3, 4), One, New(3, 4)},
	}
	for _, tt := range tests {
		got := tt.a.Div(tt.b)
		if !got.Equal(tt.want) {
			t.Errorf("%s / %s = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDivPanicsOnZero(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Div by zero did not panic")
		}
	}()
	One.Div(Zero)
}

func TestLess(t *testing.T) {
	tests := []struct {
		a, b Ratio
		want bool
	}{
		{New(1, 3), New(1, 2), true},
		{New(1, 2), New(1, 3), false},
		{New(1, 2), New(1, 2), false},
		{New(-1, 2), New(1, 2), true},
		{Zero, New(1, 1000), true},
	}
	for _, tt := range tests {
		got := tt.a.Less(tt.b)
		if got != tt.want {
			t.Errorf("%s < %s = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestEqual(t *testing.T) {
	if !New(2, 4).Equal(New(1, 2)) {
		t.Error("2/4 should equal 1/2")
	}
	if !New(15, 100).Equal(New(3, 20)) {
		t.Error("15/100 should equal 3/20")
	}
	if New(1, 2).Equal(New(1, 3)) {
		t.Error("1/2 should not equal 1/3")
	}
}

func TestScaleInt(t *testing.T) {
	tests := []struct {
		r    Ratio
		n    int64
		want int64
	}{
		{New(1, 2), 100, 50},
		{New(1, 3), 100, 33},
		{New(2, 3), 99, 66},
		{One, 42, 42},
		{Zero, 100, 0},
		{New(3, 20), 1000, 150},
	}
	for _, tt := range tests {
		got := tt.r.ScaleInt(tt.n)
		if got != tt.want {
			t.Errorf("%s.ScaleInt(%d) = %d, want %d", tt.r, tt.n, got, tt.want)
		}
	}
}

func TestFloat64(t *testing.T) {
	tests := []struct {
		r    Ratio
		want float64
	}{
		{New(1, 2), 0.5},
		{One, 1.0},
		{Zero, 0.0},
		{New(1, 4), 0.25},
	}
	for _, tt := range tests {
		got := tt.r.Float64()
		if got != tt.want {
			t.Errorf("%s.Float64() = %f, want %f", tt.r, got, tt.want)
		}
	}
}

func TestNeg(t *testing.T) {
	r := New(3, 4)
	neg := r.Neg()
	if neg.Num != -3 || neg.Denom != 4 {
		t.Errorf("Neg(3/4) = %s, want -3/4", neg)
	}
	if !neg.Neg().Equal(r) {
		t.Error("double negation should be identity")
	}
}

func TestAbs(t *testing.T) {
	if !New(-3, 4).Abs().Equal(New(3, 4)) {
		t.Error("Abs(-3/4) should be 3/4")
	}
	if !New(3, 4).Abs().Equal(New(3, 4)) {
		t.Error("Abs(3/4) should be 3/4")
	}
}

func TestClamp(t *testing.T) {
	lo := New(1, 5)  // 0.2
	hi := New(9, 10) // 0.9

	below := New(1, 10) // 0.1
	if !below.Clamp(lo, hi).Equal(lo) {
		t.Error("0.1 clamped to [0.2, 0.9] should be 0.2")
	}

	above := One
	if !above.Clamp(lo, hi).Equal(hi) {
		t.Error("1.0 clamped to [0.2, 0.9] should be 0.9")
	}

	mid := Half
	if !mid.Clamp(lo, hi).Equal(Half) {
		t.Error("0.5 clamped to [0.2, 0.9] should be 0.5")
	}
}

func TestMaxMin(t *testing.T) {
	a := New(1, 3)
	b := New(1, 2)
	if !Max(a, b).Equal(b) {
		t.Error("Max(1/3, 1/2) should be 1/2")
	}
	if !Min(a, b).Equal(a) {
		t.Error("Min(1/3, 1/2) should be 1/3")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	original := New(3, 20)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Ratio
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if !decoded.Equal(original) {
		t.Errorf("JSON round-trip: %s != %s", decoded, original)
	}

	// Re-serialize and verify byte-identical output.
	data2, _ := json.Marshal(decoded)
	if string(data) != string(data2) {
		t.Errorf("JSON not deterministic:\n  %s\n  %s", data, data2)
	}
}

func TestFitnessWeights(t *testing.T) {
	// Verify 3/20 + 1/20 + 16/20 = 1
	w := New(3, 20).Add(New(1, 20)).Add(New(16, 20))
	if !w.Equal(One) {
		t.Errorf("fitness weights sum to %s, want 1/1", w)
	}
}

func TestComputeFitness(t *testing.T) {
	// Source=1, Binary=1, Behav=1 → Overall should be 1
	s := New(3, 20).Mul(One).Add(New(1, 20).Mul(One)).Add(New(16, 20).Mul(One))
	if !s.Equal(One) {
		t.Errorf("perfect fitness = %s, want 1/1", s)
	}

	// Source=1/2, Binary=0, Behav=1 → 3/40 + 0 + 16/20 = 3/40 + 32/40 = 35/40 = 7/8
	s2 := New(3, 20).Mul(Half).Add(New(1, 20).Mul(Zero)).Add(New(16, 20).Mul(One))
	if !s2.Equal(New(7, 8)) {
		t.Errorf("mixed fitness = %s, want 7/8", s2)
	}
}
