package memory

import (
	"bytes"
	"encoding/json"

	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/dgraph-io/badger/v4"
)

// TypePoint is a single generation's type count for a tag.
type TypePoint struct {
	Gen   uint32
	Count uint32
}

// MissingPoint is a single generation's missing count for a tag.
type MissingPoint struct {
	Gen   uint32
	Count uint32
}

// FitnessPoint is a single generation's fitness score.
type FitnessPoint struct {
	Gen   uint32
	Score ratio.Ratio
}

// HealthPoint is a single generation's health snapshot.
type HealthPoint struct {
	Gen            uint32
	Occupied       uint32
	Total          uint32
	AvgLockIn      ratio.Ratio
}

// HexPoint is a single generation's operation count.
type HexPoint struct {
	Gen   uint32
	Count uint32
}

// QueryTypeTrend returns type counts for a tag across the last N generations,
// ordered by generation (ascending).
func (d *DB) QueryTypeTrend(tag string, lastN int) []TypePoint {
	h := TagHash(tag)
	prefix := TypPrefix(h)
	return collectTypPoints(d.db, prefix, lastN)
}

// QueryMissingTrend returns missing counts for a tag across the last N
// generations, ordered by generation (ascending).
func (d *DB) QueryMissingTrend(tag string, lastN int) []MissingPoint {
	h := TagHash(tag)
	prefix := MisPrefix(h)
	return collectMisPoints(d.db, prefix, lastN)
}

// QueryFitnessTrajectory returns overall fitness scores across the last N
// generations, ordered by generation (ascending).
func (d *DB) QueryFitnessTrajectory(lastN int) []FitnessPoint {
	prefix := FitDimPrefix(DimOverall)
	return collectFitPoints(d.db, prefix, lastN)
}

// QueryFitnessDimension returns scores for a specific fitness dimension
// across the last N generations.
func (d *DB) QueryFitnessDimension(dim byte, lastN int) []FitnessPoint {
	prefix := FitDimPrefix(dim)
	return collectFitPoints(d.db, prefix, lastN)
}

// QueryHealthHistory returns health snapshots across the last N generations,
// ordered by generation (ascending).
func (d *DB) QueryHealthHistory(lastN int) []HealthPoint {
	prefix := PrefixHlt[:]
	var points []HealthPoint

	_ = d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		// Seek to end of prefix range.
		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			gen := DecodeHlt(key)
			var occ, tot uint32
			var num, denom int64
			_ = item.Value(func(val []byte) error {
				occ, tot, num, denom = DecodeHltValue(val)
				return nil
			})
			points = append(points, HealthPoint{
				Gen:       gen,
				Occupied:  occ,
				Total:     tot,
				AvgLockIn: ratio.New(num, denom),
			})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	// Reverse to ascending order.
	reverse(points)
	return points
}

// QueryBondCount returns the number of bonds for a tag in a specific generation.
func (d *DB) QueryBondCount(tag string, gen uint32) int {
	h := TagHash(tag)
	prefix := BndGenPrefix(h, gen)
	count := 0

	_ = d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			if !bytes.HasPrefix(it.Item().Key(), prefix) {
				break
			}
			count++
		}
		return nil
	})
	return count
}

// QueryBondHistory returns bond counts for a tag across the last N generations.
// Uses the bnd prefix to scan all generations and count per-gen entries.
func (d *DB) QueryBondHistory(tag string, lastN int) []TypePoint {
	h := TagHash(tag)
	prefix := BndPrefix(h)

	// Collect all (gen → count) by scanning all bonds for this tag.
	genCounts := make(map[uint32]uint32)

	_ = d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(prefix); it.Valid(); it.Next() {
			key := it.Item().Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_, gen, _ := DecodeBnd(key)
			genCounts[gen]++
		}
		return nil
	})

	// Sort by generation descending, take lastN.
	var points []TypePoint
	for gen, count := range genCounts {
		points = append(points, TypePoint{Gen: gen, Count: count})
	}
	// Sort ascending by gen.
	sortTypePoints(points)

	if lastN > 0 && len(points) > lastN {
		points = points[len(points)-lastN:]
	}
	return points
}

// QueryHexagramOps returns operation counts for a specific operation across
// the last N generations.
func (d *DB) QueryHexagramOps(op byte, lastN int) []HexPoint {
	prefix := make([]byte, 4)
	copy(prefix, PrefixHex[:])
	prefix[3] = op

	var points []HexPoint

	_ = d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_, gen := DecodeHex(key)
			var count uint32
			_ = item.Value(func(val []byte) error {
				count = DecodeU32Value(val)
				return nil
			})
			points = append(points, HexPoint{Gen: gen, Count: count})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	reverse(points)
	return points
}

// QueryGeneration returns generation metadata. Returns nil if not found.
func (d *DB) QueryGeneration(gen uint32) *genMeta {
	key := GenKey(gen)
	var meta genMeta
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &meta)
		})
	})
	if err != nil {
		return nil
	}
	return &meta
}

// ADSRPoint is a single generation's ADSR phase distribution.
type ADSRPoint struct {
	Gen    uint32
	Counts [4]uint32 // [Attack, Decay, Sustain, Release]
}

// QueryADSRHistory returns ADSR distributions across the last N generations,
// ordered by generation (ascending).
func (d *DB) QueryADSRHistory(lastN int) []ADSRPoint {
	prefix := PrefixAdr[:]
	var points []ADSRPoint

	_ = d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			gen := DecodeGen(key)
			var counts [4]uint32
			_ = item.Value(func(val []byte) error {
				counts = DecodeAdrValue(val)
				return nil
			})
			points = append(points, ADSRPoint{Gen: gen, Counts: counts})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	reverse(points)
	return points
}

// --- internal helpers ---

func collectTypPoints(db *badger.DB, prefix []byte, lastN int) []TypePoint {
	var points []TypePoint

	_ = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			key := it.Item().Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_, count, gen := DecodeTyp(key)
			points = append(points, TypePoint{Gen: gen, Count: count})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	reverse(points)
	return points
}

func collectMisPoints(db *badger.DB, prefix []byte, lastN int) []MissingPoint {
	var points []MissingPoint

	_ = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.PrefetchValues = false
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			key := it.Item().Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_, gen, count := DecodeMis(key)
			points = append(points, MissingPoint{Gen: gen, Count: count})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	reverse(points)
	return points
}

func collectFitPoints(db *badger.DB, prefix []byte, lastN int) []FitnessPoint {
	var points []FitnessPoint

	_ = db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Reverse = true
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()

		end := PrefixEnd(prefix)
		it.Seek(end)

		for it.Valid() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_, gen := DecodeFit(key)
			var num, denom int64
			_ = item.Value(func(val []byte) error {
				num, denom = DecodeFitValue(val)
				return nil
			})
			points = append(points, FitnessPoint{
				Gen:   gen,
				Score: ratio.New(num, denom),
			})
			if lastN > 0 && len(points) >= lastN {
				break
			}
			it.Next()
		}
		return nil
	})

	reverse(points)
	return points
}

// reverse reverses a slice in place. Works with any slice type via generics.
func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// sortTypePoints sorts by generation ascending (insertion sort, small slices).
func sortTypePoints(pts []TypePoint) {
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0 && pts[j].Gen < pts[j-1].Gen; j-- {
			pts[j], pts[j-1] = pts[j-1], pts[j]
		}
	}
}
