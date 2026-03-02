// Block-level serialization for demand-paged growth. Each block can be
// frozen to disk and thawed back independently. The node skeleton (id +
// neighbor pointers) is never evicted — only the payload (constraints,
// occupant, candidates, projection state) is serialized and stripped.
//
// Format: binary, one file per block. Fixed header + N node records.
// Strings are uint16 length-prefixed UTF-8. All integers little-endian.
package grow

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/enzyme"
	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/mlekudev/dendrite/pkg/state"
)

const (
	blockMagic   = 0x424C4B30 // "BLK0"
	blockVersion = 1
)

// blockHeader is the fixed-size file header for a block snapshot.
type blockHeader struct {
	Magic     uint32
	Version   uint16
	BlockID   uint32
	NodeCount uint32
}

// nodeFlags encodes boolean fields into a single byte.
const (
	nodeFlagOccupied   = 1 << iota // bit 0: has occupant
	nodeFlagCandidates             // bit 1: has candidates
	nodeFlagCrossLayer             // bit 2: cross-layer bond
)

// blockFilePath returns the path for a block's snapshot file.
func blockFilePath(dir string, blockID int) string {
	return filepath.Join(dir, fmt.Sprintf("blk%06d.dat", blockID))
}

// freezeBlock serializes a resident block's node payloads to disk.
// The node skeleton (id, neighbors) is not written — it persists in memory.
// Caller must have exclusive access to the block.
func freezeBlock(b *Block, dir string) error {
	path := blockFilePath(dir, b.ID)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("freezeBlock %d: %w", b.ID, err)
	}
	w := bufio.NewWriter(f)

	hdr := blockHeader{
		Magic:     blockMagic,
		Version:   blockVersion,
		BlockID:   uint32(b.ID),
		NodeCount: uint32(len(b.Nodes)),
	}
	if err := binary.Write(w, binary.LittleEndian, &hdr); err != nil {
		f.Close()
		return fmt.Errorf("freezeBlock %d header: %w", b.ID, err)
	}

	for _, n := range b.Nodes {
		if err := writeNodePayload(w, n); err != nil {
			f.Close()
			return fmt.Errorf("freezeBlock %d node %d: %w", b.ID, n.ID(), err)
		}
	}

	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// thawBlock loads node payloads from disk and restores them onto the
// existing (stripped) node skeleton. The constraint factory reconstructs
// Constraint objects from tag strings.
// Caller must have exclusive access to the block.
func thawBlock(b *Block, dir string, cf func(string) axiom.Constraint) error {
	path := blockFilePath(dir, b.ID)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("thawBlock %d: %w", b.ID, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)

	var hdr blockHeader
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return fmt.Errorf("thawBlock %d header: %w", b.ID, err)
	}
	if hdr.Magic != blockMagic {
		return fmt.Errorf("thawBlock %d: bad magic %08x", b.ID, hdr.Magic)
	}
	if hdr.Version != blockVersion {
		return fmt.Errorf("thawBlock %d: unsupported version %d", b.ID, hdr.Version)
	}
	if int(hdr.NodeCount) != len(b.Nodes) {
		return fmt.Errorf("thawBlock %d: node count mismatch: file=%d block=%d",
			b.ID, hdr.NodeCount, len(b.Nodes))
	}

	for i, n := range b.Nodes {
		if err := readNodePayload(r, n, cf); err != nil {
			return fmt.Errorf("thawBlock %d node %d: %w", b.ID, i, err)
		}
	}
	return nil
}

// writeNodePayload serializes one node's payload fields.
func writeNodePayload(w io.Writer, n interface {
	Occupant() axiom.Element
	Candidates() []axiom.Element
	Constraints() []axiom.Constraint
	Hexagram() state.Hexagram
	LockIn() ratio.Ratio
	BondCount() int
	CrossLayer() bool
	Permutation() uint8
	ProjectionVertex() uint8
	ProjectionKey() uint8
	ProjectionPath() uint16
	Age() uint8
}) error {
	// Gather state via public accessors (locked, but no contention between rounds).
	occ := n.Occupant()
	cands := n.Candidates()
	constraints := n.Constraints()
	hex := n.Hexagram()
	lockIn := n.LockIn()
	bondCount := n.BondCount()
	crossLayer := n.CrossLayer()
	perm := n.Permutation()
	projVertex := n.ProjectionVertex()
	projKey := n.ProjectionKey()
	projPath := n.ProjectionPath()
	age := n.Age()

	// Flags byte.
	var flags uint8
	if occ != nil {
		flags |= nodeFlagOccupied
	}
	if len(cands) > 0 {
		flags |= nodeFlagCandidates
	}
	if crossLayer {
		flags |= nodeFlagCrossLayer
	}

	// Fixed fields: flags, hex, perm, projVertex, projKey, projPath, age,
	// bondCount, lockIn (num + den).
	fixed := []any{
		flags,
		uint8(hex),
		perm,
		projVertex,
		projKey,
		projPath,
		age,
		uint8(bondCount),
		lockIn.Num,
		lockIn.Denom,
	}
	for _, v := range fixed {
		if err := binary.Write(w, binary.LittleEndian, v); err != nil {
			return err
		}
	}

	// Constraint tags.
	if err := writeUint16(w, uint16(len(constraints))); err != nil {
		return err
	}
	for _, c := range constraints {
		if err := writeString(w, c.Tag()); err != nil {
			return err
		}
	}

	// Occupant.
	if occ != nil {
		if err := writeString(w, occ.Type()); err != nil {
			return err
		}
		if err := writeString(w, fmt.Sprintf("%v", occ.Value())); err != nil {
			return err
		}
	}

	// Candidates.
	if len(cands) > 0 {
		if err := writeUint16(w, uint16(len(cands))); err != nil {
			return err
		}
		for _, c := range cands {
			if err := writeString(w, c.Type()); err != nil {
				return err
			}
			if err := writeString(w, fmt.Sprintf("%v", c.Value())); err != nil {
				return err
			}
		}
	}

	return nil
}

// readNodePayload deserializes one node's payload fields and restores them.
func readNodePayload(r io.Reader, n interface {
	RestoreConstraintsUnsafe([]axiom.Constraint)
	ForceOccupant(axiom.Element, int)
	AddCandidate(axiom.Element) bool
	RestoreAge(uint8)
	SetPermutation(uint8)
	SetProjection(uint8, uint8, uint16)
	SetEnergy(bool)
	SetOuterTrigram(state.Trigram)
}, cf func(string) axiom.Constraint) error {
	var flags, hex, perm, projVertex, projKey, age, bondCount uint8
	var projPath uint16
	var lockInNum, lockInDen int64

	for _, v := range []any{
		&flags, &hex, &perm, &projVertex, &projKey, &projPath, &age,
		&bondCount, &lockInNum, &lockInDen,
	} {
		if err := binary.Read(r, binary.LittleEndian, v); err != nil {
			return err
		}
	}

	// Constraints.
	tagCount, err := readUint16(r)
	if err != nil {
		return err
	}
	constraints := make([]axiom.Constraint, tagCount)
	for i := range constraints {
		tag, err := readString(r)
		if err != nil {
			return err
		}
		constraints[i] = cf(tag)
	}
	n.RestoreConstraintsUnsafe(constraints)

	// Occupant.
	if flags&nodeFlagOccupied != 0 {
		typ, err := readString(r)
		if err != nil {
			return err
		}
		val, err := readString(r)
		if err != nil {
			return err
		}
		n.ForceOccupant(enzyme.Elem(typ, val), int(bondCount))
	}

	// Candidates.
	if flags&nodeFlagCandidates != 0 {
		candCount, err := readUint16(r)
		if err != nil {
			return err
		}
		for range candCount {
			typ, err := readString(r)
			if err != nil {
				return err
			}
			val, err := readString(r)
			if err != nil {
				return err
			}
			n.AddCandidate(enzyme.Elem(typ, val))
		}
	}

	// Restore scalar state.
	n.RestoreAge(age)
	n.SetPermutation(perm)
	n.SetProjection(projVertex, projKey, projPath)

	// Restore hexagram.
	h := state.Hexagram(hex)
	n.SetEnergy(h.Inner().Energy())
	n.SetOuterTrigram(h.Outer())

	return nil
}

// writeString writes a uint16 length-prefixed UTF-8 string.
func writeString(w io.Writer, s string) error {
	if len(s) > 0xFFFF {
		s = s[:0xFFFF]
	}
	if err := writeUint16(w, uint16(len(s))); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

// readString reads a uint16 length-prefixed UTF-8 string.
func readString(r io.Reader) (string, error) {
	n, err := readUint16(r)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(r, buf)
	return string(buf), err
}

// writeUint16 writes a little-endian uint16.
func writeUint16(w io.Writer, v uint16) error {
	return binary.Write(w, binary.LittleEndian, v)
}

// readUint16 reads a little-endian uint16.
func readUint16(r io.Reader) (uint16, error) {
	var v uint16
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

// removeBlockFile deletes a block's snapshot from disk.
func removeBlockFile(dir string, blockID int) error {
	return os.Remove(blockFilePath(dir, blockID))
}
