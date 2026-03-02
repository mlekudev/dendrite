// Package memory provides persistent cross-generation knowledge storage
// using Badger v4 with composite binary keys and vector table indexes.
//
// Key schema follows the ORLY relay pattern: 3-byte ASCII prefixes,
// fixed-width binary components, nil-value secondary indexes, and
// sorted range iteration for graph traversal.
package memory

import (
	"crypto/sha256"
	"encoding/binary"
)

// 3-byte ASCII prefixes for each index table.
var (
	PrefixGen  = [3]byte{'g', 'e', 'n'} // generation metadata
	PrefixTyp  = [3]byte{'t', 'y', 'p'} // type signature snapshots
	PrefixBnd  = [3]byte{'b', 'n', 'd'} // bond events
	PrefixMis  = [3]byte{'m', 'i', 's'} // missing site records
	PrefixCon  = [3]byte{'c', 'o', 'n'} // connectivity statistics
	PrefixFit  = [3]byte{'f', 'i', 't'} // fitness scores
	PrefixHlt  = [3]byte{'h', 'l', 't'} // health snapshots
	PrefixHex  = [3]byte{'h', 'e', 'x'} // hexagram operation counts
	PrefixLck  = [3]byte{'l', 'c', 'k'} // lock-in depth distribution
	PrefixMnd  = [3]byte{'m', 'n', 'd'} // mindsicle snapshots
	PrefixEwm  = [3]byte{'e', 'w', 'm'} // EWMA detector state
	PrefixAdr  = [3]byte{'a', 'd', 'r'} // ADSR phase distribution
	PrefixFsc  = [3]byte{'f', 's', 'c'} // file accretion scores
	PrefixWlk  = [3]byte{'w', 'l', 'k'} // walker checkpoint (singleton)
	PrefixOrc  = [3]byte{'o', 'r', 'c'} // oracle state per generation
	PrefixOhx  = [3]byte{'o', 'h', 'x'} // oracle reading history
	PrefixOsr  = [3]byte{'o', 's', 'r'} // oracle directive results
	PrefixPrf  = [3]byte{'p', 'r', 'f'} // recognition profile snapshots
	PrefixMdl  = [3]byte{'m', 'd', 'l'} // model fingerprint spores
	PrefixCvg  = [3]byte{'c', 'v', 'g'} // convergence state
)

// Fitness dimension constants.
const (
	DimSource  byte = 0
	DimBinary  byte = 1
	DimBehav   byte = 2
	DimOverall byte = 3
)

// TagHash produces a truncated 8-byte SHA-256 hash of a tag string.
// Same principle as ORLY's PubHash/IdHash: deterministic, fixed-width,
// collision-acceptable (used for key prefix, not identification).
func TagHash(tag string) [8]byte {
	h := sha256.Sum256([]byte(tag))
	var out [8]byte
	copy(out[:], h[:8])
	return out
}

// --- Key builders ---
// All keys are built by concatenating: prefix(3) + components.
// Generation number is always uint32 big-endian (4 bytes).

// GenKey builds: gen | gen(4)
func GenKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixGen[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// TypKey builds: typ | tag_hash(8) | count(4) | gen(4)
func TypKey(tagHash [8]byte, count uint32, gen uint32) []byte {
	k := make([]byte, 3+8+4+4)
	copy(k, PrefixTyp[:])
	copy(k[3:], tagHash[:])
	binary.BigEndian.PutUint32(k[11:], count)
	binary.BigEndian.PutUint32(k[15:], gen)
	return k
}

// TypPrefix builds the seek prefix for a specific tag: typ | tag_hash(8)
func TypPrefix(tagHash [8]byte) []byte {
	k := make([]byte, 3+8)
	copy(k, PrefixTyp[:])
	copy(k[3:], tagHash[:])
	return k
}

// BndKey builds: bnd | tag_hash(8) | gen(4) | site_id(4)
func BndKey(tagHash [8]byte, gen uint32, siteID uint32) []byte {
	k := make([]byte, 3+8+4+4)
	copy(k, PrefixBnd[:])
	copy(k[3:], tagHash[:])
	binary.BigEndian.PutUint32(k[11:], gen)
	binary.BigEndian.PutUint32(k[15:], siteID)
	return k
}

// BndPrefix builds the seek prefix for bonds of a tag: bnd | tag_hash(8)
func BndPrefix(tagHash [8]byte) []byte {
	k := make([]byte, 3+8)
	copy(k, PrefixBnd[:])
	copy(k[3:], tagHash[:])
	return k
}

// BndGenPrefix builds: bnd | tag_hash(8) | gen(4) for counting bonds in a gen.
func BndGenPrefix(tagHash [8]byte, gen uint32) []byte {
	k := make([]byte, 3+8+4)
	copy(k, PrefixBnd[:])
	copy(k[3:], tagHash[:])
	binary.BigEndian.PutUint32(k[11:], gen)
	return k
}

// MisKey builds: mis | tag_hash(8) | gen(4) | count(4)
func MisKey(tagHash [8]byte, gen uint32, count uint32) []byte {
	k := make([]byte, 3+8+4+4)
	copy(k, PrefixMis[:])
	copy(k[3:], tagHash[:])
	binary.BigEndian.PutUint32(k[11:], gen)
	binary.BigEndian.PutUint32(k[15:], count)
	return k
}

// MisPrefix builds: mis | tag_hash(8)
func MisPrefix(tagHash [8]byte) []byte {
	k := make([]byte, 3+8)
	copy(k, PrefixMis[:])
	copy(k[3:], tagHash[:])
	return k
}

// ConKey builds: con | tag_hash(8) | gen(4)
func ConKey(tagHash [8]byte, gen uint32) []byte {
	k := make([]byte, 3+8+4)
	copy(k, PrefixCon[:])
	copy(k[3:], tagHash[:])
	binary.BigEndian.PutUint32(k[11:], gen)
	return k
}

// FitKey builds: fit | dimension(1) | gen(4)
func FitKey(dim byte, gen uint32) []byte {
	k := make([]byte, 3+1+4)
	copy(k, PrefixFit[:])
	k[3] = dim
	binary.BigEndian.PutUint32(k[4:], gen)
	return k
}

// FitDimPrefix builds: fit | dimension(1)
func FitDimPrefix(dim byte) []byte {
	k := make([]byte, 3+1)
	copy(k, PrefixFit[:])
	k[3] = dim
	return k
}

// HltKey builds: hlt | gen(4)
func HltKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixHlt[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// HexKey builds: hex | operation(1) | gen(4)
func HexKey(op byte, gen uint32) []byte {
	k := make([]byte, 3+1+4)
	copy(k, PrefixHex[:])
	k[3] = op
	binary.BigEndian.PutUint32(k[4:], gen)
	return k
}

// LckKey builds: lck | bucket(1) | gen(4)
func LckKey(bucket byte, gen uint32) []byte {
	k := make([]byte, 3+1+4)
	copy(k, PrefixLck[:])
	k[3] = bucket
	binary.BigEndian.PutUint32(k[4:], gen)
	return k
}

// MndKey builds: mnd | gen(4)
func MndKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixMnd[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// EwmKey builds: ewm | gen(4)
func EwmKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixEwm[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// AdrKey builds: adr | gen(4)
func AdrKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixAdr[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// FscKey builds: fsc | file_hash(8)
func FscKey(fileHash [8]byte) []byte {
	k := make([]byte, 3+8)
	copy(k, PrefixFsc[:])
	copy(k[3:], fileHash[:])
	return k
}

// WlkKey builds: wlk (singleton key, no suffix)
func WlkKey() []byte {
	return PrefixWlk[:]
}

// OrcKey builds: orc (singleton key — always the latest oracle state).
func OrcKey() []byte {
	return PrefixOrc[:]
}

// OhxKey builds: ohx | seq(4) | gen(4)
func OhxKey(seq, gen uint32) []byte {
	k := make([]byte, 3+4+4)
	copy(k, PrefixOhx[:])
	binary.BigEndian.PutUint32(k[3:], seq)
	binary.BigEndian.PutUint32(k[7:], gen)
	return k
}

// OsrKey builds: osr | seq(4) | directive_hash(8)
func OsrKey(seq uint32, hash [8]byte) []byte {
	k := make([]byte, 3+4+8)
	copy(k, PrefixOsr[:])
	binary.BigEndian.PutUint32(k[3:], seq)
	copy(k[7:], hash[:])
	return k
}

// --- Recognition key builders ---

// PrfKey builds: prf | gen(4) — profile snapshot for a generation.
func PrfKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixPrf[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// MdlKey builds: mdl | name_hash(8) — model fingerprint spore.
func MdlKey(nameHash [8]byte) []byte {
	k := make([]byte, 3+8)
	copy(k, PrefixMdl[:])
	copy(k[3:], nameHash[:])
	return k
}

// CvgKey builds: cvg | gen(4) — convergence state for a generation.
func CvgKey(gen uint32) []byte {
	k := make([]byte, 3+4)
	copy(k, PrefixCvg[:])
	binary.BigEndian.PutUint32(k[3:], gen)
	return k
}

// DecodeOhx extracts seq and gen from an ohx-prefixed key.
func DecodeOhx(key []byte) (seq, gen uint32) {
	if len(key) < 11 {
		return
	}
	seq = binary.BigEndian.Uint32(key[3:7])
	gen = binary.BigEndian.Uint32(key[7:11])
	return
}

// --- Key decoders ---

// DecodeGen extracts generation from a gen-prefixed key.
func DecodeGen(key []byte) uint32 {
	if len(key) < 7 {
		return 0
	}
	return binary.BigEndian.Uint32(key[3:7])
}

// DecodeTyp extracts tag_hash, count, gen from a typ-prefixed key.
func DecodeTyp(key []byte) (tagHash [8]byte, count uint32, gen uint32) {
	if len(key) < 19 {
		return
	}
	copy(tagHash[:], key[3:11])
	count = binary.BigEndian.Uint32(key[11:15])
	gen = binary.BigEndian.Uint32(key[15:19])
	return
}

// DecodeBnd extracts tag_hash, gen, site_id from a bnd-prefixed key.
func DecodeBnd(key []byte) (tagHash [8]byte, gen uint32, siteID uint32) {
	if len(key) < 19 {
		return
	}
	copy(tagHash[:], key[3:11])
	gen = binary.BigEndian.Uint32(key[11:15])
	siteID = binary.BigEndian.Uint32(key[15:19])
	return
}

// DecodeMis extracts tag_hash, gen, count from a mis-prefixed key.
func DecodeMis(key []byte) (tagHash [8]byte, gen uint32, count uint32) {
	if len(key) < 19 {
		return
	}
	copy(tagHash[:], key[3:11])
	gen = binary.BigEndian.Uint32(key[11:15])
	count = binary.BigEndian.Uint32(key[15:19])
	return
}

// DecodeFit extracts dimension and gen from a fit-prefixed key.
func DecodeFit(key []byte) (dim byte, gen uint32) {
	if len(key) < 8 {
		return
	}
	dim = key[3]
	gen = binary.BigEndian.Uint32(key[4:8])
	return
}

// DecodeHlt extracts gen from a hlt-prefixed key.
func DecodeHlt(key []byte) uint32 {
	if len(key) < 7 {
		return 0
	}
	return binary.BigEndian.Uint32(key[3:7])
}

// DecodeHex extracts operation and gen from a hex-prefixed key.
func DecodeHex(key []byte) (op byte, gen uint32) {
	if len(key) < 8 {
		return
	}
	op = key[3]
	gen = binary.BigEndian.Uint32(key[4:8])
	return
}

// --- Value encoders/decoders ---

// EncodeFitValue encodes a numerator/denominator pair.
func EncodeFitValue(num, denom int64) []byte {
	v := make([]byte, 16)
	binary.BigEndian.PutUint64(v[0:], uint64(num))
	binary.BigEndian.PutUint64(v[8:], uint64(denom))
	return v
}

// DecodeFitValue decodes a numerator/denominator pair.
func DecodeFitValue(v []byte) (num, denom int64) {
	if len(v) < 16 {
		return 0, 1
	}
	num = int64(binary.BigEndian.Uint64(v[0:]))
	denom = int64(binary.BigEndian.Uint64(v[8:]))
	if denom == 0 {
		denom = 1
	}
	return
}

// EncodeHltValue encodes health snapshot.
func EncodeHltValue(occupied, total uint32, avgLockInNum, avgLockInDenom int64) []byte {
	v := make([]byte, 4+4+8+8)
	binary.BigEndian.PutUint32(v[0:], occupied)
	binary.BigEndian.PutUint32(v[4:], total)
	binary.BigEndian.PutUint64(v[8:], uint64(avgLockInNum))
	binary.BigEndian.PutUint64(v[16:], uint64(avgLockInDenom))
	return v
}

// DecodeHltValue decodes health snapshot.
func DecodeHltValue(v []byte) (occupied, total uint32, avgLockInNum, avgLockInDenom int64) {
	if len(v) < 24 {
		return 0, 0, 0, 1
	}
	occupied = binary.BigEndian.Uint32(v[0:])
	total = binary.BigEndian.Uint32(v[4:])
	avgLockInNum = int64(binary.BigEndian.Uint64(v[8:]))
	avgLockInDenom = int64(binary.BigEndian.Uint64(v[16:]))
	if avgLockInDenom == 0 {
		avgLockInDenom = 1
	}
	return
}

// EncodeConValue encodes connectivity as fixed-point (num/denom).
func EncodeConValue(num, denom int64) []byte {
	v := make([]byte, 16)
	binary.BigEndian.PutUint64(v[0:], uint64(num))
	binary.BigEndian.PutUint64(v[8:], uint64(denom))
	return v
}

// DecodeConValue decodes connectivity fixed-point.
func DecodeConValue(v []byte) (num, denom int64) {
	return DecodeFitValue(v) // same encoding
}

// EncodeU32Value encodes a single uint32 (for hex/lck counts).
func EncodeU32Value(n uint32) []byte {
	v := make([]byte, 4)
	binary.BigEndian.PutUint32(v, n)
	return v
}

// DecodeU32Value decodes a single uint32.
func DecodeU32Value(v []byte) uint32 {
	if len(v) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}

// EncodeAdrValue encodes 4 ADSR phase counts: [attack, decay, sustain, release].
func EncodeAdrValue(counts [4]uint32) []byte {
	v := make([]byte, 16)
	for i := range 4 {
		binary.BigEndian.PutUint32(v[i*4:], counts[i])
	}
	return v
}

// DecodeAdrValue decodes 4 ADSR phase counts.
func DecodeAdrValue(v []byte) [4]uint32 {
	var counts [4]uint32
	if len(v) < 16 {
		return counts
	}
	for i := range 4 {
		counts[i] = binary.BigEndian.Uint32(v[i*4:])
	}
	return counts
}

// PrefixEnd returns the key that is one past the end of all keys
// with the given prefix. Used as the upper bound for range scans.
func PrefixEnd(prefix []byte) []byte {
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end
		}
	}
	return nil // overflow: prefix was all 0xFF
}
