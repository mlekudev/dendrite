package memory

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/options"
)

// TagCount pairs a tag name with a count.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// TagRatio pairs a tag name with a rational value.
type TagRatio struct {
	Tag   string      `json:"tag"`
	Value ratio.Ratio `json:"value"`
}

// DB is the persistent cross-generation memory store.
type DB struct {
	db *badger.DB
}

// Open creates or opens a memory database at the given directory.
func Open(dir string) (*DB, error) {
	opts := badger.DefaultOptions(dir)
	opts.CompactL0OnClose = true
	opts.LmaxCompaction = true
	opts.Compression = options.None
	opts.Logger = nil // silent
	opts.MemTableSize = 16 << 20        // 16MB memtable (default 64MB)
	opts.ValueLogFileSize = 64 << 20    // 64MB vlog files (default 1GB)
	opts.NumMemtables = 2               // reduce from default 5
	opts.NumLevelZeroTables = 2         // reduce from default 5
	opts.NumLevelZeroTablesStall = 5    // reduce from default 15
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	if d.db == nil {
		return nil
	}
	return d.db.Close()
}

// genMeta is the JSON value stored in generation records.
type genMeta struct {
	Timestamp  int64  `json:"ts"`
	ParentHash string `json:"parent,omitempty"`
	InstanceID uint32 `json:"inst"`
	ConfigHash string `json:"cfg,omitempty"`
}

// RecordGeneration writes generation metadata.
func (d *DB) RecordGeneration(gen uint32, parentHash string, instanceID uint32, ts time.Time) error {
	meta := genMeta{
		Timestamp:  ts.UnixMilli(),
		ParentHash: parentHash,
		InstanceID: instanceID,
	}
	val, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(GenKey(gen), val)
	})
}

// BondRecord is a single bond event: an element type bonded at a site.
type BondRecord struct {
	Tag    string
	SiteID uint32
}

// RecordBonds writes bond events for a generation.
// Uses nil values — the key IS the index.
func (d *DB) RecordBonds(gen uint32, bonds []BondRecord) error {
	if len(bonds) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for _, b := range bonds {
			h := TagHash(b.Tag)
			key := BndKey(h, gen, b.SiteID)
			if err := txn.Set(key, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordMissing writes missing site records (negative space) for a generation.
func (d *DB) RecordMissing(gen uint32, missing []TagCount) error {
	if len(missing) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for _, m := range missing {
			h := TagHash(m.Tag)
			key := MisKey(h, gen, uint32(m.Count))
			if err := txn.Set(key, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordTypeSig writes type signature snapshot for a generation.
func (d *DB) RecordTypeSig(gen uint32, typeSig []TagCount) error {
	if len(typeSig) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for _, tc := range typeSig {
			h := TagHash(tc.Tag)
			key := TypKey(h, uint32(tc.Count), gen)
			if err := txn.Set(key, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordConnectivity writes per-type connectivity for a generation.
func (d *DB) RecordConnectivity(gen uint32, connectivity []TagRatio) error {
	if len(connectivity) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for _, c := range connectivity {
			h := TagHash(c.Tag)
			key := ConKey(h, gen)
			val := EncodeConValue(c.Value.Num, c.Value.Denom)
			if err := txn.Set(key, val); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordHealth writes a health snapshot for a generation.
func (d *DB) RecordHealth(gen uint32, occupied, total uint32, avgLockIn ratio.Ratio) error {
	key := HltKey(gen)
	val := EncodeHltValue(occupied, total, avgLockIn.Num, avgLockIn.Denom)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
}

// RecordLockInDist writes lock-in depth distribution for a generation.
// buckets maps quantized bucket (0-255) → count.
func (d *DB) RecordLockInDist(gen uint32, buckets map[byte]uint32) error {
	if len(buckets) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for bucket, count := range buckets {
			key := LckKey(bucket, gen)
			val := EncodeU32Value(count)
			if err := txn.Set(key, val); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordHexagramOps writes hexagram operation counts for a generation.
// ops maps operation code (0-7) → count.
func (d *DB) RecordHexagramOps(gen uint32, ops map[byte]uint32) error {
	if len(ops) == 0 {
		return nil
	}
	return d.db.Update(func(txn *badger.Txn) error {
		for op, count := range ops {
			key := HexKey(op, gen)
			val := EncodeU32Value(count)
			if err := txn.Set(key, val); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordMindsicle stores a mindsicle (frozen lattice) for a generation.
func (d *DB) RecordMindsicle(gen uint32, data []byte) error {
	key := MndKey(gen)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// LoadMindsicle retrieves a mindsicle for a specific generation.
func (d *DB) LoadMindsicle(gen uint32) ([]byte, error) {
	key := MndKey(gen)
	var val []byte
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		return err
	})
	return val, err
}

// LatestMindsicle returns the highest generation for which a mindsicle exists,
// along with its data. Returns (0, nil, ErrKeyNotFound) if none.
func (d *DB) LatestMindsicle() (uint32, []byte, error) {
	var gen uint32
	var data []byte
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = PrefixMnd[:]
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()
		// Seek to the end of the mnd prefix range.
		it.Seek(PrefixEnd(PrefixMnd[:]))
		if !it.Valid() {
			// Try seeking to just the prefix for the first item.
			it.Rewind()
			if !it.Valid() {
				return badger.ErrKeyNotFound
			}
		}
		item := it.Item()
		key := item.Key()
		if len(key) < 7 || key[0] != PrefixMnd[0] || key[1] != PrefixMnd[1] || key[2] != PrefixMnd[2] {
			return badger.ErrKeyNotFound
		}
		gen = DecodeGen(key)
		var err error
		data, err = item.ValueCopy(nil)
		return err
	})
	return gen, data, err
}

// RecordEWMAState stores serialized EWMA detector state for a generation.
func (d *DB) RecordEWMAState(gen uint32, state []byte) error {
	key := EwmKey(gen)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, state)
	})
}

// LoadLatestEWMAState returns the highest generation for which EWMA state
// exists, along with its data. Returns (0, nil, ErrKeyNotFound) if none.
func (d *DB) LoadLatestEWMAState() (uint32, []byte, error) {
	var gen uint32
	var data []byte
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = PrefixEwm[:]
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()
		it.Seek(PrefixEnd(PrefixEwm[:]))
		if !it.Valid() {
			it.Rewind()
			if !it.Valid() {
				return badger.ErrKeyNotFound
			}
		}
		item := it.Item()
		key := item.Key()
		if len(key) < 7 || key[0] != PrefixEwm[0] || key[1] != PrefixEwm[1] || key[2] != PrefixEwm[2] {
			return badger.ErrKeyNotFound
		}
		gen = DecodeGen(key)
		var err error
		data, err = item.ValueCopy(nil)
		return err
	})
	return gen, data, err
}

// RecordADSR writes the ADSR phase distribution for a generation.
func (d *DB) RecordADSR(gen uint32, counts [4]uint32) error {
	key := AdrKey(gen)
	val := EncodeAdrValue(counts)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
}

// RecordFitness writes fitness scores for a generation.
func (d *DB) RecordFitness(gen uint32, source, binary, behav, overall ratio.Ratio) error {
	return d.db.Update(func(txn *badger.Txn) error {
		dims := []struct {
			dim byte
			r   ratio.Ratio
		}{
			{DimSource, source},
			{DimBinary, binary},
			{DimBehav, behav},
			{DimOverall, overall},
		}
		for _, d := range dims {
			key := FitKey(d.dim, gen)
			val := EncodeFitValue(d.r.Num, d.r.Denom)
			if err := txn.Set(key, val); err != nil {
				return err
			}
		}
		return nil
	})
}

// WalkerCheckpoint is the JSON value stored in the singleton wlk key.
// Contains everything needed to resume the walker at the exact position.
type WalkerCheckpoint struct {
	Epoch    uint32   `json:"epoch"`
	Seed     uint64   `json:"seed"`
	Position int      `json:"position"`
	GenNum   uint32   `json:"gen_num"`
	Files    []string `json:"files"`
	Root     string   `json:"root"`
}

// RecordFileScore accumulates raw and accreted counts for a file.
// Uses read-modify-write within a single transaction.
func (d *DB) RecordFileScore(filePath string, rawDelta, accretedDelta int64) error {
	h := TagHash(filePath)
	key := FscKey(h)
	return d.db.Update(func(txn *badger.Txn) error {
		var prevAccreted, prevRaw int64
		item, err := txn.Get(key)
		if err == nil {
			_ = item.Value(func(val []byte) error {
				prevAccreted, prevRaw = DecodeFitValue(val)
				return nil
			})
		}
		return txn.Set(key, EncodeFitValue(prevAccreted+accretedDelta, prevRaw+rawDelta))
	})
}

// LoadFileScores loads all per-file accretion scores.
// Returns a map of file path hash → [accreted, raw].
func (d *DB) LoadFileScores() (map[[8]byte][2]int64, error) {
	scores := make(map[[8]byte][2]int64)
	prefix := PrefixFsc[:]
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			if len(key) < 11 {
				continue
			}
			var h [8]byte
			copy(h[:], key[3:11])
			_ = item.Value(func(val []byte) error {
				accreted, raw := DecodeFitValue(val)
				scores[h] = [2]int64{accreted, raw}
				return nil
			})
		}
		return nil
	})
	return scores, err
}

// RecordWalkerCheckpoint persists the walker state for exact resume.
func (d *DB) RecordWalkerCheckpoint(cp WalkerCheckpoint) error {
	val, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(WlkKey(), val)
	})
}

// LoadWalkerCheckpoint loads the persisted walker checkpoint.
func (d *DB) LoadWalkerCheckpoint() (WalkerCheckpoint, error) {
	var cp WalkerCheckpoint
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(WlkKey())
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &cp)
		})
	})
	return cp, err
}

// RecordOracleState stores the current oracle state (JSON), overwriting any previous.
// Uses a singleton key — only the latest state matters; per-gen history is in ohx keys.
func (d *DB) RecordOracleState(data []byte) error {
	key := OrcKey()
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// LoadOracleState returns the current oracle state, or ErrKeyNotFound if none exists.
func (d *DB) LoadOracleState() ([]byte, error) {
	var data []byte
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(OrcKey())
		if err != nil {
			return err
		}
		data, err = item.ValueCopy(nil)
		return err
	})
	return data, err
}

// RecordOracleReading stores an individual reading by sequence and generation.
func (d *DB) RecordOracleReading(seq, gen uint32, data []byte) error {
	key := OhxKey(seq, gen)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// LoadOracleHistory loads the last N oracle readings, ordered by sequence.
func (d *DB) LoadOracleHistory(lastN int) ([][]byte, error) {
	var results [][]byte
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = PrefixOhx[:]
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()
		it.Seek(PrefixEnd(PrefixOhx[:]))
		count := 0
		for ; it.Valid() && count < lastN; it.Next() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, PrefixOhx[:]) {
				break
			}
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			results = append(results, val)
			count++
		}
		return nil
	})
	// Reverse to get chronological order (earliest first).
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	return results, err
}

// --- Recognition methods ---

// RecordProfile stores a recognition profile snapshot for a generation.
func (d *DB) RecordProfile(gen uint32, data []byte) error {
	key := PrfKey(gen)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// LoadLatestProfile returns the highest generation for which a profile exists.
func (d *DB) LoadLatestProfile() (uint32, []byte, error) {
	var gen uint32
	var data []byte
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = PrefixPrf[:]
		opts.Reverse = true
		it := txn.NewIterator(opts)
		defer it.Close()
		it.Seek(PrefixEnd(PrefixPrf[:]))
		if !it.Valid() {
			it.Rewind()
			if !it.Valid() {
				return badger.ErrKeyNotFound
			}
		}
		item := it.Item()
		key := item.Key()
		if len(key) < 7 || key[0] != PrefixPrf[0] || key[1] != PrefixPrf[1] || key[2] != PrefixPrf[2] {
			return badger.ErrKeyNotFound
		}
		gen = DecodeGen(key)
		var err error
		data, err = item.ValueCopy(nil)
		return err
	})
	return gen, data, err
}

// RecordConvergence stores convergence state for a generation.
func (d *DB) RecordConvergence(gen uint32, data []byte) error {
	key := CvgKey(gen)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}

// RecordModelSpore stores a model fingerprint spore by name.
func (d *DB) RecordModelSpore(modelName string, data []byte) error {
	h := TagHash(modelName)
	key := MdlKey(h)
	// Store the name alongside the data so we can recover it.
	val := append([]byte(modelName+"\x00"), data...)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, val)
	})
}

// LoadModelSpore retrieves a model fingerprint spore by name.
func (d *DB) LoadModelSpore(modelName string) ([]byte, error) {
	h := TagHash(modelName)
	key := MdlKey(h)
	var data []byte
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		// Skip the name prefix.
		idx := bytes.IndexByte(val, 0)
		if idx >= 0 && idx < len(val)-1 {
			data = val[idx+1:]
		} else {
			data = val
		}
		return nil
	})
	return data, err
}

// ListModelSpores returns the names of all stored model fingerprints.
func (d *DB) ListModelSpores() ([]string, error) {
	var names []string
	prefix := PrefixMdl[:]
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Seek(prefix); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()
			if !bytes.HasPrefix(key, prefix) {
				break
			}
			_ = item.Value(func(val []byte) error {
				idx := bytes.IndexByte(val, 0)
				if idx > 0 {
					names = append(names, string(val[:idx]))
				}
				return nil
			})
		}
		return nil
	})
	return names, err
}

// RecordDirectiveResult stores the execution result of an oracle directive.
func (d *DB) RecordDirectiveResult(seq uint32, hash [8]byte, data []byte) error {
	key := OsrKey(seq, hash)
	return d.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, data)
	})
}
