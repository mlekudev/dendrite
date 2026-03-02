// Package state defines the three bits and eight trigrams — the minimum
// instruction set that generates all lattice dynamics from the axiom pair.
package state

// Trigram is a 3-bit value encoding the eight change vectors.
type Trigram uint8

const (
	Earth    Trigram = 0b000 // ☷ dissolving, free, depleted — substrate
	Thunder  Trigram = 0b001 // ☳ accreting, free, depleted — nucleation
	Water    Trigram = 0b010 // ☵ dissolving, bound, depleted — frozen defect
	Lake     Trigram = 0b011 // ☱ accreting, bound, depleted — ambiguity zone
	Fire     Trigram = 0b100 // ☲ dissolving, free, energized — noisy growth
	Heaven   Trigram = 0b101 // ☰ accreting, free, energized — ideal growth
	Wind     Trigram = 0b110 // ☴ dissolving, bound, energized — coherence pruning
	Mountain Trigram = 0b111 // ☶ accreting, bound, energized — equilibrium
)

// Bit positions.
const (
	BitBonding    = 0 // bottom line
	BitConstraint = 1 // middle line
	BitEnergy     = 2 // top line
)

// Bonding reports whether the accreting bit is set.
func (t Trigram) Bonding() bool { return t&(1<<BitBonding) != 0 }

// Constraint reports whether the bound bit is set.
func (t Trigram) Constraint() bool { return t&(1<<BitConstraint) != 0 }

// Energy reports whether the supersaturated bit is set.
func (t Trigram) Energy() bool { return t&(1<<BitEnergy) != 0 }

// SetBonding returns the trigram with the bonding bit set or cleared.
func (t Trigram) SetBonding(v bool) Trigram {
	if v {
		return t | (1 << BitBonding)
	}
	return t &^ (1 << BitBonding)
}

// SetConstraint returns the trigram with the constraint bit set or cleared.
func (t Trigram) SetConstraint(v bool) Trigram {
	if v {
		return t | (1 << BitConstraint)
	}
	return t &^ (1 << BitConstraint)
}

// SetEnergy returns the trigram with the energy bit set or cleared.
func (t Trigram) SetEnergy(v bool) Trigram {
	if v {
		return t | (1 << BitEnergy)
	}
	return t &^ (1 << BitEnergy)
}

// Flip returns the trigram with the specified bit inverted.
// This is a moving line — a transition between dynamical regimes.
func (t Trigram) Flip(bit uint8) Trigram {
	if bit > 2 {
		return t
	}
	return t ^ (1 << bit)
}

// Hexagram is two trigrams packed into a single byte: inner (the site's
// own state) in the low 3 bits, outer (the environment) in the high 3 bits.
// Values 0-63.
type Hexagram uint8

// Hex constructs a hexagram from inner and outer trigrams.
func Hex(inner, outer Trigram) Hexagram {
	return Hexagram(uint8(inner) | uint8(outer)<<3)
}

// Inner returns the site's own trigram (low 3 bits).
func (h Hexagram) Inner() Trigram { return Trigram(h & 0b111) }

// Outer returns the environment trigram (high 3 bits).
func (h Hexagram) Outer() Trigram { return Trigram(h >> 3 & 0b111) }

// MoveLine returns a new hexagram with the specified line moved.
// inner=true flips an inner line, inner=false flips an outer line.
func (h Hexagram) MoveLine(inner bool, bit uint8) Hexagram {
	if bit > 2 {
		return h
	}
	if inner {
		return Hexagram(uint8(h) ^ (1 << bit))
	}
	return Hexagram(uint8(h) ^ (1 << (bit + 3)))
}

// EncodeBytes converts raw bytes to a slice of Hexagram tokens.
// Every 3 bytes produce 4 hexagram tokens (24 bits = 4 × 6 bits).
// If len(data) is not a multiple of 3, the final group is zero-padded.
func EncodeBytes(data []byte) []Hexagram {
	if len(data) == 0 {
		return nil
	}
	groups := (len(data) + 2) / 3 // ceil(len/3)
	out := make([]Hexagram, groups*4)
	for i := 0; i < len(data); i += 3 {
		var block [3]byte
		copy(block[:], data[i:min(i+3, len(data))])
		bits := uint32(block[0])<<16 | uint32(block[1])<<8 | uint32(block[2])
		j := (i / 3) * 4
		out[j+0] = Hexagram((bits >> 18) & 0x3F)
		out[j+1] = Hexagram((bits >> 12) & 0x3F)
		out[j+2] = Hexagram((bits >> 6) & 0x3F)
		out[j+3] = Hexagram(bits & 0x3F)
	}
	return out
}

// DecodeHexagrams converts hexagram tokens back to raw bytes.
// Every 4 tokens produce 3 bytes. origLen is the original byte count
// (needed because the final group may have been zero-padded).
func DecodeHexagrams(tokens []Hexagram, origLen int) []byte {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]byte, 0, origLen)
	for i := 0; i+3 < len(tokens); i += 4 {
		bits := uint32(tokens[i]&0x3F)<<18 | uint32(tokens[i+1]&0x3F)<<12 |
			uint32(tokens[i+2]&0x3F)<<6 | uint32(tokens[i+3]&0x3F)
		out = append(out, byte(bits>>16), byte(bits>>8), byte(bits))
	}
	if len(out) > origLen {
		out = out[:origLen]
	}
	return out
}
