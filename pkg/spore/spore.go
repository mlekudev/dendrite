// Package spore implements sporulation — the mature lattice producing
// compact, portable seeds that can bootstrap new lattice instances.
//
// A spore captures the symmetry group of the lattice: the constraint
// types, their frequency distribution, and the connectivity pattern.
// It does not store individual elements (they are the lattice, not the
// seed). It stores the shape of the negative space — what kinds of
// things can bond and how they relate.
package spore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"sort"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
	"github.com/mlekudev/dendrite/pkg/ratio"
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

// PeerMark pairs a peer instance ID with its birthmark.
type PeerMark struct {
	ID   uint32 `json:"id"`
	Mark uint64 `json:"mark"`
}

// PeerMemEntry is a single entry in long-term peer memory.
type PeerMemEntry struct {
	ID              uint32 `json:"id"`
	Birthmark       uint64 `json:"birthmark"`
	LastSeen        int    `json:"last_seen"`
	FingerprintHash string `json:"fingerprint_hash,omitempty"` // SporeFingerprint.Hash for cross-generation verification
	Accumulator     string `json:"accumulator,omitempty"`      // base64-encoded Cayley accumulator (96 bytes)
}

// Spore is a minimal, portable representation of a lattice's structure.
// It carries enough information to nucleate a new lattice with the same
// orientation but adapted to new input.
type Spore struct {
	// TypeSignature records constraint tags and their frequency in the
	// source lattice. This is the symmetry group's fingerprint —
	// what types exist and in what proportion.
	TypeSignature []TagCount `json:"type_signature"`

	// Connectivity records the average neighbor count per type.
	// How densely connected each type layer is.
	Connectivity []TagRatio `json:"connectivity"`

	// Occupied is the number of sites that had bonded elements.
	Occupied int `json:"occupied"`

	// TotalNodes in the source lattice at sporulation time.
	TotalNodes int `json:"total_nodes"`

	// ElementTypes records element type tags and their count.
	// What actually bonded, not just what was available.
	ElementTypes []TagCount `json:"element_types"`

	// ParentHash is the SHA-256 of the parent spore's JSON encoding.
	// Empty string for first-generation (abiogenesis) spores.
	ParentHash string `json:"parent_hash,omitempty"`

	// Generation counts how many sporulation cycles preceded this one.
	Generation int `json:"generation"`

	// Fitness records how well this generation's emitted code
	// reproduces the original. Three dimensions:
	//   Source:  structural AST similarity (0..1)
	//   Binary:  compiled binary similarity (0..1)
	//   Behav:   behavioral equivalence (0..1)
	//   Overall: weighted combination (0..1)
	Fitness *FitnessScore `json:"fitness,omitempty"`

	// OwnBirthmark is this instance's fingerprint — a uint64 derived
	// from system entropy XOR'd with a hash of the emitted source.
	// Zero when running in single-instance mode.
	OwnBirthmark uint64 `json:"own_birthmark,omitempty"`

	// PeerBirthmarks records the birthmarks received from other
	// instances in the colony. Sorted by ID.
	PeerBirthmarks []PeerMark `json:"peer_birthmarks,omitempty"`

	// PeerMemory is long-term memory: accumulated across generations.
	// Each entry records the last known birthmark and the generation
	// it was last seen. This persists through sporulation and
	// germination, giving the organism continuity of relationships.
	// Sorted by ID.
	PeerMemory []PeerMemEntry `json:"peer_memory,omitempty"`

	// OrganManifests records the organs loaded in this generation.
	// Carried across sporulation so offspring know what organs the
	// parent had available.
	OrganManifests []OrganManifestRecord `json:"organ_manifests,omitempty"`

	// DirectiveHistory records operator directives applied to this
	// lineage. Sticky directives persist across generations.
	DirectiveHistory []DirectiveRecord `json:"directive_history,omitempty"`

	// MissingSites records element types that failed to bond during
	// growth — the negative space of the lattice. Maps type tag to
	// rejection count. This tells the organism what structures it
	// lacks to absorb the input.
	MissingSites []TagCount `json:"missing_sites,omitempty"`

	// StubNames records the identifiers that the compiler flagged as
	// undefined during this generation's compile-repair cycle.
	// Fed back during the next generation's growth as targeted injection.
	StubNames []string `json:"stub_names,omitempty"`

	// PermDist records how many nodes use each of the 6 S_3
	// permutations (projection angles). Index is permutation.Perm
	// value (0=Identity through 5=Cycle021).
	PermDist [6]int `json:"perm_dist,omitempty"`

	// ProjDist records how many nodes use each of the 64 projection
	// configurations (3-bit vertex + 3-bit key). Index is the packed
	// 6-bit value: vertex (low 3) | key (high 3).
	ProjDist [64]int `json:"proj_dist,omitempty"`

	// PathDist records the distribution of rendering path indices
	// across nodes. Key is the path index, value is the count.
	// Only non-zero paths are stored to keep the spore compact.
	PathDist []PathCount `json:"path_dist,omitempty"`

	// AgeDist records how many occupied nodes are at each age (0-3).
	// Index is the age value: 0=newborn, 1=young, 2=mature, 3=senescent.
	AgeDist [4]int `json:"age_dist,omitempty"`

	// CryptoTags stores the constraint type tags used to generate
	// the lattice keypair. Together with the constraint factory
	// (provided at runtime), these allow regenerating the private key.
	// Empty when crypto is not enabled.
	CryptoTags []string `json:"crypto_tags,omitempty"`

	// CryptoParamsLevel stores the security level used for keypair
	// generation: 0 = Security128, 1 = Security192, 2 = Security256.
	CryptoParamsLevel uint8 `json:"crypto_params_level,omitempty"`

	// DissolutionNoise is a 32-byte hash digest of dissolution events
	// from the generation that produced this spore. Fed back as salt
	// for the next generation's input, breaking input monotony.
	DissolutionNoise [32]byte `json:"dissolution_noise,omitempty"`

	// OwnAccumulator is the Cayley hash accumulator chain for this
	// instance, base64-encoded (96 bytes → 128 chars). Each generation
	// extends it: A_n = A_{n-1} * CayleyHash(source_n). Used for
	// stable peer recognition without leaking identity.
	OwnAccumulator string `json:"own_accumulator,omitempty"`
}

// PathCount pairs a rendering path index with a count of nodes using it.
type PathCount struct {
	Path  uint16 `json:"path"`
	Count int    `json:"count"`
}

// FitnessScore captures the three-dimensional fitness evaluation.
// All fields are exact rationals — no floating-point nondeterminism.
type FitnessScore struct {
	Source  ratio.Ratio `json:"source"`
	Binary  ratio.Ratio `json:"binary"`
	Behav   ratio.Ratio `json:"behav"`
	Overall ratio.Ratio `json:"overall"`
}

// OrganManifestRecord is a lightweight record of an organ carried in
// a spore. It omits the WASM bytes — those are obtained from peers.
type OrganManifestRecord struct {
	ID        string      `json:"id"`         // hex-encoded OrganID
	Type      string      `json:"type"`       // enzyme, emitter, etc.
	Version   uint64      `json:"version"`
	Size      int         `json:"size"`       // WASM byte count
	Fitness   ratio.Ratio `json:"fitness"`
	SourceGen int         `json:"source_gen"` // generation that produced this organ
}

// DirectiveRecord captures a directive applied to this lineage.
type DirectiveRecord struct {
	Text       string      `json:"text"`
	Priority   ratio.Ratio `json:"priority"`
	Sticky     bool        `json:"sticky"`
	Generation int         `json:"generation"` // when it was applied
}

// Hash returns the SHA-256 hex digest of this spore's JSON encoding.
// This is the spore's identity — its fingerprint in the lineage.
func (s *Spore) Hash() string {
	data, _ := json.Marshal(s)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// addTagCount increments the count for the given tag, or appends a new entry.
func AddTagCount(s *[]TagCount, tag string) {
	for i := range *s {
		if (*s)[i].Tag == tag {
			(*s)[i].Count++
			return
		}
	}
	*s = append(*s, TagCount{Tag: tag, Count: 1})
}

// addTagCountN adds n to the count for the given tag, or appends a new entry.
func AddTagCountN(s *[]TagCount, tag string, n int) {
	for i := range *s {
		if (*s)[i].Tag == tag {
			(*s)[i].Count += n
			return
		}
	}
	*s = append(*s, TagCount{Tag: tag, Count: n})
}

// tagCountValue returns the count for the given tag, or 0 if not found.
func tagCountValue(s []TagCount, tag string) int {
	for _, tc := range s {
		if tc.Tag == tag {
			return tc.Count
		}
	}
	return 0
}

// tagRatioValue returns the value for the given tag, or zero if not found.
func tagRatioValue(s []TagRatio, tag string) ratio.Ratio {
	for _, tr := range s {
		if tr.Tag == tag {
			return tr.Value
		}
	}
	return ratio.Zero
}

// addPathCount increments the count for the given path index.
func addPathCount(s *[]PathCount, path uint16) {
	for i := range *s {
		if (*s)[i].Path == path {
			(*s)[i].Count++
			return
		}
	}
	*s = append(*s, PathCount{Path: path, Count: 1})
}

// OccupancyRate returns the fraction of occupied sites as a Ratio.
func (s *Spore) OccupancyRate() ratio.Ratio {
	if s.TotalNodes == 0 {
		return ratio.Zero
	}
	return ratio.New(int64(s.Occupied), int64(s.TotalNodes))
}

// sortedTagCountTags returns the tags from a TagCount slice, sorted.
func sortedTagCountTags(s []TagCount) []string {
	tags := make([]string, len(s))
	for i, tc := range s {
		tags[i] = tc.Tag
	}
	sort.Strings(tags)
	return tags
}

// PeerMemByID returns the PeerMemEntry for the given ID, or ok=false.
func PeerMemByID(s []PeerMemEntry, id uint32) (PeerMemEntry, bool) {
	for _, e := range s {
		if e.ID == id {
			return e, true
		}
	}
	return PeerMemEntry{}, false
}

// Extract produces a spore from a mature lattice. This is sporulation —
// the lattice compressing its own structure into a portable seed.
// If parent is non-nil, the new spore records the parent's hash and
// increments the generation counter.
func Extract(l *lattice.Lattice, parent ...*Spore) *Spore {
	s := &Spore{
		TotalNodes: l.Size(),
	}

	occupied := 0

	// Temporary accumulator for neighbor counts per tag.
	type tagCounts struct {
		tag    string
		counts []int
	}
	var neighborCounts []tagCounts

	findOrAdd := func(tag string) *tagCounts {
		for i := range neighborCounts {
			if neighborCounts[i].tag == tag {
				return &neighborCounts[i]
			}
		}
		neighborCounts = append(neighborCounts, tagCounts{tag: tag})
		return &neighborCounts[len(neighborCounts)-1]
	}

	for _, n := range l.Nodes() {
		// Constraint types.
		for _, c := range n.Constraints() {
			AddTagCount(&s.TypeSignature, c.Tag())
		}

		// Connectivity per type.
		nbs := n.Neighbors()
		for _, c := range n.Constraints() {
			tc := findOrAdd(c.Tag())
			tc.counts = append(tc.counts, len(nbs))
		}

		// Element types.
		if n.Occupied() {
			occupied++
			e := n.Occupant()
			AddTagCount(&s.ElementTypes, e.Type())
		}

		// Permutation distribution.
		if p := n.Permutation(); p < 6 {
			s.PermDist[p]++
		}

		// Projection distribution (6-bit: vertex | key<<3).
		proj6 := n.Projection6Bit()
		if proj6 < 64 {
			s.ProjDist[proj6]++
		}

		// Path distribution.
		if path := n.ProjectionPath(); path > 0 {
			addPathCount(&s.PathDist, path)
		}

		// Age distribution.
		if n.Occupied() {
			age := n.Age()
			if age < 4 {
				s.AgeDist[age]++
			}
		}
	}

	s.Occupied = occupied

	// Average connectivity per type.
	for _, tc := range neighborCounts {
		sum := 0
		for _, c := range tc.counts {
			sum += c
		}
		s.Connectivity = append(s.Connectivity, TagRatio{
			Tag:   tc.tag,
			Value: ratio.New(int64(sum), int64(len(tc.counts))),
		})
	}

	// Lineage.
	if len(parent) > 0 && parent[0] != nil {
		s.ParentHash = parent[0].Hash()
		s.Generation = parent[0].Generation + 1
	}

	return s
}

// Nucleate creates a new lattice from a spore, scaled to the given size.
// The new lattice has the same type proportions and connectivity pattern
// as the source, but is empty — ready for new input.
func (s *Spore) Nucleate(targetSize int, constraintFactory func(tag string) axiom.Constraint) *lattice.Lattice {
	l := lattice.New()

	if targetSize <= 0 || len(s.TypeSignature) == 0 {
		return l
	}

	// Calculate proportional allocation.
	totalConstraints := 0
	for _, tc := range s.TypeSignature {
		totalConstraints += tc.Count
	}

	// Sort tags for deterministic ordering.
	tags := sortedTagCountTags(s.TypeSignature)

	// Allocate nodes proportionally.
	type tagNodes struct {
		tag   string
		nodes []*lattice.Node
	}
	var nodesByTag []tagNodes

	allocated := 0
	for _, tag := range tags {
		count := tagCountValue(s.TypeSignature, tag)
		n := int(ratio.New(int64(count), int64(totalConstraints)).ScaleInt(int64(targetSize)))
		if n < 1 {
			n = 1
		}
		if allocated+n > targetSize {
			n = targetSize - allocated
		}
		if n <= 0 {
			continue
		}
		tn := tagNodes{tag: tag}
		for range n {
			node := l.AddNode([]axiom.Constraint{constraintFactory(tag)})
			node.SetEnergy(true)
			tn.nodes = append(tn.nodes, node)
		}
		nodesByTag = append(nodesByTag, tn)
		allocated += n
	}

	// Connect within each type (ring topology).
	for _, tn := range nodesByTag {
		for i := range tn.nodes {
			l.Connect(tn.nodes[i], tn.nodes[(i+1)%len(tn.nodes)])
		}
	}

	// Cross-connect between types based on source connectivity.
	for i := 0; i < len(nodesByTag); i++ {
		for j := i + 1; j < len(nodesByTag); j++ {
			nodesA := nodesByTag[i].nodes
			nodesB := nodesByTag[j].nodes
			// Connect every Nth node between types.
			step := max(1, min(len(nodesA), len(nodesB))/3)
			for k := 0; k < min(len(nodesA), len(nodesB)); k += step {
				l.Connect(nodesA[k], nodesB[k])
			}
		}
	}

	return l
}

// NucleateGrammar creates a new lattice from the spore's TypeSignature,
// filtered by grammar adjacency. Unlike Nucleate, cross-connections are only
// made between tag pairs that canNeighbor permits, and bridge node selection
// is seeded by instanceSeed for per-instance topology variation.
//
// canNeighbor should return true if elements of type a and b may be lattice
// neighbors. Passing a closure avoids importing the grammar package (which
// would create an import cycle through memory).
func (s *Spore) NucleateGrammar(
	targetSize int,
	canNeighbor func(a, b string) bool,
	instanceSeed [32]byte,
	constraintFactory func(string) axiom.Constraint,
) *lattice.Lattice {
	l := lattice.New()

	if targetSize <= 0 || len(s.TypeSignature) == 0 {
		return l
	}

	// Calculate proportional allocation.
	totalConstraints := 0
	for _, tc := range s.TypeSignature {
		totalConstraints += tc.Count
	}

	// Sort tags for deterministic ordering.
	tags := sortedTagCountTags(s.TypeSignature)

	// Allocate nodes proportionally.
	type tagGroup struct {
		tag   string
		nodes []*lattice.Node
	}
	var groups []tagGroup

	allocated := 0
	for _, tag := range tags {
		count := tagCountValue(s.TypeSignature, tag)
		n := int(ratio.New(int64(count), int64(totalConstraints)).ScaleInt(int64(targetSize)))
		if n < 1 {
			n = 1
		}
		if allocated+n > targetSize {
			n = targetSize - allocated
		}
		if n <= 0 {
			continue
		}
		tg := tagGroup{tag: tag}
		for range n {
			node := l.AddNode([]axiom.Constraint{constraintFactory(tag)})
			node.SetEnergy(true)
			tg.nodes = append(tg.nodes, node)
		}
		groups = append(groups, tg)
		allocated += n
	}

	// Connect within each type (ring topology).
	for _, tg := range groups {
		if len(tg.nodes) < 2 {
			continue
		}
		for i := range tg.nodes {
			l.Connect(tg.nodes[i], tg.nodes[(i+1)%len(tg.nodes)])
		}
	}

	// Grammar-filtered cross-connections with seeded bridge selection.
	var seed [32]byte
	copy(seed[:], instanceSeed[:])
	rng := rand.New(rand.NewChaCha8(seed))

	for i, tgA := range groups {
		for j := i + 1; j < len(groups); j++ {
			tgB := groups[j]

			// Only bridge grammar-adjacent pairs.
			if !canNeighbor(tgA.tag, tgB.tag) && !canNeighbor(tgB.tag, tgA.tag) {
				continue
			}

			smaller := min(len(tgA.nodes), len(tgB.nodes))
			nBridges := max(1, smaller/3)

			for range nBridges {
				idxA := rng.IntN(len(tgA.nodes))
				idxB := rng.IntN(len(tgB.nodes))
				l.Connect(tgA.nodes[idxA], tgB.nodes[idxB])
			}
		}
	}

	return l
}

// WriteTo serializes the spore to JSON.
func (s *Spore) WriteTo(w io.Writer) (int64, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	return int64(n), err
}

// ReadSpore deserializes a spore from JSON.
func ReadSpore(r io.Reader) (*Spore, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var s Spore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// String returns a human-readable summary of the spore.
func (s *Spore) String() string {
	out := fmt.Sprintf("spore: %d nodes, %.0f%% occupied\n", s.TotalNodes, s.OccupancyRate().Float64()*100)
	out += "type signature:\n"

	tags := sortedTagCountTags(s.TypeSignature)
	for _, tag := range tags {
		count := tagCountValue(s.TypeSignature, tag)
		conn := tagRatioValue(s.Connectivity, tag)
		out += fmt.Sprintf("  %-20s count=%-4d connectivity=%.1f\n", tag, count, conn.Float64())
	}

	if len(s.ElementTypes) > 0 {
		out += "element types:\n"
		etags := sortedTagCountTags(s.ElementTypes)
		for _, tag := range etags {
			count := tagCountValue(s.ElementTypes, tag)
			out += fmt.Sprintf("  %-20s %d\n", tag, count)
		}
	}

	return out
}
