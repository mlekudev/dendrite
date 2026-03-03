package hadamard

import (
	"testing"
)

func TestTransformSelfInverse(t *testing.T) {
	// H_8 * H_8 = 8 * I, so applying Transform twice should give 8*original.
	orig := Vec8{3, 1, 4, 1, 5, 9, 2, 6}
	v := orig
	Transform(&v)
	Transform(&v)
	for i := range v {
		if v[i] != orig[i]*8 {
			t.Errorf("channel %d: got %d, want %d", i, v[i], orig[i]*8)
		}
	}
}

func TestTransformAllOnes(t *testing.T) {
	// H_8 * [1,1,1,1,1,1,1,1]^T = [8,0,0,0,0,0,0,0]^T
	v := Vec8{1, 1, 1, 1, 1, 1, 1, 1}
	Transform(&v)
	if v[0] != 8 {
		t.Errorf("DC component: got %d, want 8", v[0])
	}
	for i := 1; i < 8; i++ {
		if v[i] != 0 {
			t.Errorf("component %d: got %d, want 0", i, v[i])
		}
	}
}

func TestTransformUnitVector(t *testing.T) {
	// H_8 * e_0 = [1,1,1,1,1,1,1,1]^T (first row of H_8 is all +1)
	v := Vec8{1, 0, 0, 0, 0, 0, 0, 0}
	Transform(&v)
	for i := range v {
		if v[i] != 1 {
			t.Errorf("component %d: got %d, want 1", i, v[i])
		}
	}
}

func TestTransformE1(t *testing.T) {
	// H_8 * e_1 should give the second row of H_8: [1,-1,1,-1,1,-1,1,-1]
	v := Vec8{0, 1, 0, 0, 0, 0, 0, 0}
	Transform(&v)
	expected := Vec8{1, -1, 1, -1, 1, -1, 1, -1}
	if v != expected {
		t.Errorf("got %v, want %v", v, expected)
	}
}

func TestTransformOrthogonality(t *testing.T) {
	// Transform of two different unit vectors should produce orthogonal rows.
	// Inner product of any two distinct rows of H_8 should be 0.
	for a := 0; a < 8; a++ {
		for b := a + 1; b < 8; b++ {
			var va, vb Vec8
			va[a] = 1
			vb[b] = 1
			Transform(&va)
			Transform(&vb)
			dot := int64(0)
			for i := 0; i < 8; i++ {
				dot += va[i] * vb[i]
			}
			if dot != 0 {
				t.Errorf("rows %d and %d: inner product = %d, want 0", a, b, dot)
			}
		}
	}
}

func TestTransformIntegerExact(t *testing.T) {
	// Verify no overflow or truncation with large values.
	v := Vec8{1e15, -1e15, 1e15, -1e15, 1e15, -1e15, 1e15, -1e15}
	orig := v
	Transform(&v)
	Transform(&v)
	for i := range v {
		if v[i] != orig[i]*8 {
			t.Errorf("channel %d: large value roundtrip failed", i)
		}
	}
}

func TestDC(t *testing.T) {
	v := Vec8{3, 1, 4, 1, 5, 9, 2, 6}
	if got := DC(v); got != 31 {
		t.Errorf("DC: got %d, want 31", got)
	}
}

func TestDetailEnergy(t *testing.T) {
	// Transform all-ones, detail energy should be 0.
	v := Vec8{1, 1, 1, 1, 1, 1, 1, 1}
	Transform(&v)
	if got := DetailEnergy(v); got != 0 {
		t.Errorf("DetailEnergy of uniform: got %d, want 0", got)
	}

	// Transform a non-uniform vector.
	v = Vec8{10, 0, 0, 0, 0, 0, 0, 0}
	Transform(&v)
	// Walsh = [10, 10, 10, 10, 10, 10, 10, 10] — but that's the e_0 case.
	// Actually: H_8 * [10,0,...,0] = [10,10,10,10,10,10,10,10]
	// So DC = 10, detail = 10*7 = 70
	if got := DetailEnergy(v); got != 70 {
		t.Errorf("DetailEnergy of e_0*10: got %d, want 70", got)
	}
}

func TestL1Norm(t *testing.T) {
	v := Vec8{3, -1, 4, -1, 5, -9, 2, -6}
	if got := L1Norm(v); got != 31 {
		t.Errorf("L1Norm: got %d, want 31", got)
	}
}

func TestAddSub(t *testing.T) {
	a := Vec8{1, 2, 3, 4, 5, 6, 7, 8}
	b := Vec8{8, 7, 6, 5, 4, 3, 2, 1}
	sum := Add(a, b)
	diff := Sub(a, b)
	for i := range sum {
		if sum[i] != 9 {
			t.Errorf("Add[%d]: got %d, want 9", i, sum[i])
		}
	}
	expected := Vec8{-7, -5, -3, -1, 1, 3, 5, 7}
	if diff != expected {
		t.Errorf("Sub: got %v, want %v", diff, expected)
	}
}

func TestChanIndex(t *testing.T) {
	cases := []struct {
		tag  string
		want int
	}{
		{"w1", 0}, {"w2", 1}, {"w3", 2}, {"w4", 3},
		{"w5", 4}, {"punct", 5}, {"space", 6}, {"origin", 7},
		{"unknown", -1}, {"", -1},
	}
	for _, c := range cases {
		if got := ChanIndex(c.tag); got != c.want {
			t.Errorf("ChanIndex(%q): got %d, want %d", c.tag, got, c.want)
		}
	}
}

func TestErrorDetectClean(t *testing.T) {
	ref := Vec8{10, 50, 40, 30, 15, 20, 60, 5}
	ch, res := ErrorDetect(ref, ref)
	if ch != -1 || res != 0 {
		t.Errorf("clean: got channel=%d residual=%d, want -1/0", ch, res)
	}
}

func TestErrorDetectSingleCorruption(t *testing.T) {
	ref := Vec8{10, 50, 40, 30, 15, 20, 60, 5}
	for pos := range NumChans {
		corrupted := ref
		corrupted[pos] += 100 // perturb one channel
		ch, res := ErrorDetect(corrupted, ref)
		if ch != pos {
			t.Errorf("pos %d: detected channel %d, want %d", pos, ch, pos)
		}
		if res != 100 {
			t.Errorf("pos %d: residual %d, want 100", pos, res)
		}
		corrected := Correct(corrupted, ref, ch)
		if corrected != ref {
			t.Errorf("pos %d: correction failed: got %v", pos, corrected)
		}
	}
}

func TestScaleAndIsZero(t *testing.T) {
	v := Vec8{1, 2, 3, 4, 5, 6, 7, 8}
	s := Scale(v, 3)
	expected := Vec8{3, 6, 9, 12, 15, 18, 21, 24}
	if s != expected {
		t.Errorf("Scale: got %v, want %v", s, expected)
	}
	if IsZero(v) {
		t.Error("non-zero vector reported as zero")
	}
	if !IsZero(Vec8{}) {
		t.Error("zero vector not reported as zero")
	}
}

// Benchmark the transform to verify it's fast.
func BenchmarkTransform(b *testing.B) {
	v := Vec8{3, 1, 4, 1, 5, 9, 2, 6}
	for i := 0; i < b.N; i++ {
		Transform(&v)
	}
}
