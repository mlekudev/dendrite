package state

import "testing"

func TestTrigramBits(t *testing.T) {
	tests := []struct {
		tri                            Trigram
		name                           string
		bonding, constraint, energy    bool
	}{
		{Earth, "Earth", false, false, false},
		{Thunder, "Thunder", true, false, false},
		{Water, "Water", false, true, false},
		{Lake, "Lake", true, true, false},
		{Fire, "Fire", false, false, true},
		{Heaven, "Heaven", true, false, true},
		{Wind, "Wind", false, true, true},
		{Mountain, "Mountain", true, true, true},
	}
	for _, tt := range tests {
		if tt.tri.Bonding() != tt.bonding {
			t.Errorf("%s: Bonding() = %v, want %v", tt.name, tt.tri.Bonding(), tt.bonding)
		}
		if tt.tri.Constraint() != tt.constraint {
			t.Errorf("%s: Constraint() = %v, want %v", tt.name, tt.tri.Constraint(), tt.constraint)
		}
		if tt.tri.Energy() != tt.energy {
			t.Errorf("%s: Energy() = %v, want %v", tt.name, tt.tri.Energy(), tt.energy)
		}
	}
}

func TestTrigramSetters(t *testing.T) {
	// Build Mountain from Earth by setting all bits.
	tri := Earth
	tri = tri.SetBonding(true)
	tri = tri.SetConstraint(true)
	tri = tri.SetEnergy(true)
	if tri != Mountain {
		t.Errorf("expected Mountain (111), got %03b", tri)
	}

	// Clear back to Earth.
	tri = tri.SetBonding(false)
	tri = tri.SetConstraint(false)
	tri = tri.SetEnergy(false)
	if tri != Earth {
		t.Errorf("expected Earth (000), got %03b", tri)
	}
}

func TestFlip(t *testing.T) {
	// Earth (000) flip bonding -> Thunder (001)
	if Earth.Flip(BitBonding) != Thunder {
		t.Error("Earth flip bonding should be Thunder")
	}
	// Heaven (101) flip constraint -> Mountain (111)
	if Heaven.Flip(BitConstraint) != Mountain {
		t.Error("Heaven flip constraint should be Mountain")
	}
	// Mountain (111) flip energy -> Lake (011)
	if Mountain.Flip(BitEnergy) != Lake {
		t.Error("Mountain flip energy should be Lake")
	}
}

func TestHexagramRoundTrip(t *testing.T) {
	for i := uint8(0); i < 64; i++ {
		h := Hexagram(i)
		got := uint8(Hex(h.Inner(), h.Outer()))
		if got != i {
			t.Errorf("hexagram roundtrip failed: %d -> inner=%03b outer=%03b -> %d", i, h.Inner(), h.Outer(), got)
		}
	}
}

func TestMoveLine(t *testing.T) {
	// Creative (Heaven/Heaven) move inner bonding line
	// Heaven = 101, flip bit 0 (bonding) -> 100 = Fire
	creative := Hex(Heaven, Heaven)
	moved := creative.MoveLine(true, BitBonding)
	if moved.Inner() != Fire {
		t.Errorf("expected Fire inner, got %03b", moved.Inner())
	}
	if moved.Outer() != Heaven {
		t.Errorf("outer should be unchanged, got %03b", moved.Outer())
	}
}
