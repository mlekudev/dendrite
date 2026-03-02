package nostr

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// Nevent returns the NIP-19 bech32-encoded nevent string for an event,
// including relay hints. This is the shareable identifier that Nostr
// clients use to locate and display an event.
func Nevent(eventIDHex string, relays []string, authorHex string, kind int) (string, error) {
	id, err := hex.DecodeString(eventIDHex)
	if err != nil || len(id) != 32 {
		return "", fmt.Errorf("invalid event id hex")
	}

	var tlv []byte

	// TLV type 0: event id (32 bytes).
	tlv = append(tlv, 0, 32)
	tlv = append(tlv, id...)

	// TLV type 1: relay URL(s).
	for _, r := range relays {
		b := []byte(r)
		tlv = append(tlv, 1, byte(len(b)))
		tlv = append(tlv, b...)
	}

	// TLV type 2: author pubkey (32 bytes).
	if authorHex != "" {
		pub, err := hex.DecodeString(authorHex)
		if err == nil && len(pub) == 32 {
			tlv = append(tlv, 2, 32)
			tlv = append(tlv, pub...)
		}
	}

	// TLV type 3: kind (4 bytes big-endian).
	var kb [4]byte
	binary.BigEndian.PutUint32(kb[:], uint32(kind))
	tlv = append(tlv, 3, 4)
	tlv = append(tlv, kb[:]...)

	return bech32Encode("nevent", tlv)
}

// --- bech32 encoding (BIP-173) ---

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Encode(hrp string, data []byte) (string, error) {
	// Convert 8-bit data to 5-bit groups.
	conv, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}

	// Compute checksum.
	chk := bech32Checksum(hrp, conv)

	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, d := range conv {
		b.WriteByte(bech32Charset[d])
	}
	for _, d := range chk {
		b.WriteByte(bech32Charset[d])
	}
	return b.String(), nil
}

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range 5 {
			if (top>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	ret := make([]byte, 0, len(hrp)*2+1)
	for _, c := range hrp {
		ret = append(ret, byte(c>>5))
	}
	ret = append(ret, 0)
	for _, c := range hrp {
		ret = append(ret, byte(c&31))
	}
	return ret
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := append(bech32HRPExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	polymod := bech32Polymod(values) ^ 1
	chk := make([]byte, 6)
	for i := range 6 {
		chk[i] = byte((polymod >> uint(5*(5-i))) & 31)
	}
	return chk
}

func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	maxv := uint32((1 << toBits) - 1)
	var ret []byte

	for _, d := range data {
		acc = (acc << fromBits) | uint32(d)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			ret = append(ret, byte((acc>>bits)&maxv))
		}
	}

	if pad {
		if bits > 0 {
			ret = append(ret, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits {
		return nil, fmt.Errorf("excess padding")
	} else if (acc<<(toBits-bits))&maxv != 0 {
		return nil, fmt.Errorf("non-zero padding")
	}

	return ret, nil
}
