// Block-decomposed lattice partitioning. Divides a lattice into
// contiguous blocks for cache-local growth. Each block is owned
// exclusively by one worker during a round — no per-node locks.
//
// The only shared structure between blocks is the spillover queue:
// an append-only list of elements that walked to a block boundary.
package grow

import (
	"math/rand/v2"
	"sync"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// DefaultBlockSize is the number of nodes per block. Tuned for L2 cache:
// 1024 nodes * ~200 bytes = ~200KB, fitting comfortably in per-core L2.
const DefaultBlockSize = 1024

// BlockState tracks whether a block's nodes carry full payload or have
// been stripped to skeleton (id + neighbors only) with payload on disk.
type BlockState uint8

const (
	// BlockResident means node payloads are in memory; the block
	// participates in rounds normally.
	BlockResident BlockState = iota

	// BlockStripped means node payloads have been serialized to disk
	// and the in-memory nodes carry only id + neighbor pointers.
	// The block holds deferred work queues until it is loaded.
	BlockStripped
)

// Block is a contiguous partition of the lattice. During a round,
// exactly one worker owns a block — no locks needed on nodes within it.
type Block struct {
	ID    int
	Start lattice.NodeID // inclusive
	End   lattice.NodeID // exclusive
	Nodes []*lattice.Node

	// Block-local vascular index: tag → local indices into Nodes slice.
	vacantIdx map[string][]int

	// Spillover: elements that walked to a boundary edge during this round.
	// Protected by spillMu — the only lock in the system during rounds.
	spillover []SpillItem
	spillMu   sync.Mutex

	// Inbound: spillover from neighbors, drained at round start.
	inbound []SpillItem

	// active tracks whether this block has work to do.
	active bool

	// demandMap: tag → []uint16 indexed by local node offset.
	// Value = BFS distance to nearest matching vacancy. demandUnreachable = no path.
	// Rebuilt each round before walks begin.
	demandMap map[string][]uint16

	// boundaryDemand: demand signals received from neighboring blocks.
	// Integrated into the demand map during buildDemandMap.
	boundaryDemand []BoundarySignal

	// --- Demand paging fields ---

	// state tracks whether the block's nodes carry full payload.
	state BlockState

	// hasDiskCopy is true after the block has been frozen at least once.
	// Avoids redundant freezes when evicting a block that hasn't changed.
	hasDiskCopy bool

	// deferredSpills collects spill items arriving while the block is
	// stripped. Transferred to spillover on load.
	deferredSpills []SpillItem

	// deferredDemand collects boundary demand signals arriving while the
	// block is stripped. Transferred to boundaryDemand on load.
	deferredDemand []BoundarySignal
}

// SpillItem is an element that crossed a block boundary.
type SpillItem struct {
	Element axiom.Element
}

// BoundarySignal carries demand from a neighboring block's vacancy.
// The wavefront propagates outward from vacant sites; when it hits a
// block boundary, the signal crosses to the adjacent block so the
// demand gradient extends across the full lattice.
type BoundarySignal struct {
	Tag      string
	Distance uint16
	EntryID  lattice.NodeID // node in THIS block where demand enters
}

const demandUnreachable = 0xFFFF

// BlockMap holds the full decomposition of a lattice into blocks.
type BlockMap struct {
	Blocks      []*Block
	NodeToBlock []int // NodeID → block index; flat array, L1-cacheable
	BlockSize   int

	// --- Demand paging fields (zero values = paging disabled) ---

	// residentCount is the number of blocks currently in BlockResident state.
	residentCount int

	// maxResident is the memory budget: maximum simultaneous resident blocks.
	// 0 means all blocks stay resident (no paging).
	maxResident int

	// blockDir is the directory for block snapshot files.
	blockDir string

	// constraintFactory reconstructs Constraint objects from tag strings
	// during block thaw.
	constraintFactory func(string) axiom.Constraint

	// lat is a back-reference to the lattice for node range access.
	lat *lattice.Lattice
}

// buildBlockMap partitions the lattice into blocks of the given size.
// O(N) in lattice size. Builds per-block vacant indices.
func buildBlockMap(l *lattice.Lattice, blockSize int) *BlockMap {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	nodes := l.Nodes()
	n := len(nodes)
	if n == 0 {
		return &BlockMap{BlockSize: blockSize}
	}

	numBlocks := (n + blockSize - 1) / blockSize
	bm := &BlockMap{
		Blocks:      make([]*Block, numBlocks),
		NodeToBlock: make([]int, n),
		BlockSize:   blockSize,
	}

	for i := range numBlocks {
		start := i * blockSize
		end := start + blockSize
		if end > n {
			end = n
		}
		b := &Block{
			ID:        i,
			Start:     lattice.NodeID(start),
			End:       lattice.NodeID(end),
			Nodes:     nodes[start:end],
			vacantIdx: make(map[string][]int),
			active:    true,
			state:     BlockResident,
		}
		bm.Blocks[i] = b

		// Build block-local vacant index.
		for localIdx, node := range b.Nodes {
			bm.NodeToBlock[node.ID()] = i
			if !node.OccupiedUnsafe() {
				for _, c := range node.Constraints() {
					tag := c.Tag()
					b.vacantIdx[tag] = append(b.vacantIdx[tag], localIdx)
				}
			}
		}
	}

	return bm
}

// vacantByTag returns a vacant node within this block matching the tag.
// Lazily compacts stale entries. No lock needed — caller owns the block.
func (b *Block) vacantByTag(tag string) *lattice.Node {
	ids := b.vacantIdx[tag]
	for attempts := len(ids); attempts > 0 && len(ids) > 0; attempts-- {
		idx := rand.IntN(len(ids))
		localIdx := ids[idx]
		n := b.Nodes[localIdx]
		if !n.OccupiedUnsafe() {
			return n
		}
		// Stale — swap-remove.
		ids[idx] = ids[len(ids)-1]
		ids = ids[:len(ids)-1]
		b.vacantIdx[tag] = ids
	}
	return nil
}

// randomNode returns a random node within this block.
func (b *Block) randomNode() *lattice.Node {
	if len(b.Nodes) == 0 {
		return nil
	}
	return b.Nodes[rand.IntN(len(b.Nodes))]
}

// removeFromVacant removes a node from the block's vacant index
// after it has been bonded. Called by the walker after a successful bond.
func (b *Block) removeFromVacant(n *lattice.Node) {
	localIdx := int(n.ID()) - int(b.Start)
	for tag, ids := range b.vacantIdx {
		for i, id := range ids {
			if id == localIdx {
				ids[i] = ids[len(ids)-1]
				ids = ids[:len(ids)-1]
				b.vacantIdx[tag] = ids
				break
			}
		}
	}
}

// pushSpill adds a spill item to this block's spillover queue.
// Thread-safe — this is the only lock taken during a round.
// If the block is stripped, the item goes to the deferred queue
// and will be transferred on the next load.
func (b *Block) pushSpill(item SpillItem) {
	b.spillMu.Lock()
	if b.state == BlockStripped {
		b.deferredSpills = append(b.deferredSpills, item)
	} else {
		b.spillover = append(b.spillover, item)
	}
	b.spillMu.Unlock()
}

// drainSpill moves spillover into inbound and clears spillover.
// Called between rounds, no concurrent access.
func (b *Block) drainSpill() {
	b.inbound = append(b.inbound[:0], b.spillover...)
	b.spillover = b.spillover[:0]
}

// isInBlock reports whether a node belongs to this block.
func (bm *BlockMap) isInBlock(nodeID lattice.NodeID, blockID int) bool {
	return bm.NodeToBlock[nodeID] == blockID
}

// buildDemandMap runs multi-source BFS from all vacant sites within this
// block for each constraint tag. The result is a distance map: for each
// node, how many hops to the nearest vacancy of that tag. O(nodes × tags).
//
// Also integrates boundary demand signals from neighboring blocks, seeding
// the BFS with external demand sources so the gradient extends across
// block boundaries.
func (b *Block) buildDemandMap(bm *BlockMap) {
	n := len(b.Nodes)
	if n == 0 {
		return
	}

	// Collect all tags that have vacancies in this block.
	tags := make(map[string]bool)
	for tag := range b.vacantIdx {
		tags[tag] = true
	}
	// Also include tags from boundary demand signals.
	for _, sig := range b.boundaryDemand {
		tags[sig.Tag] = true
	}

	if b.demandMap == nil {
		b.demandMap = make(map[string][]uint16, len(tags))
	}

	for tag := range tags {
		// Get or allocate the distance array for this tag.
		dist := b.demandMap[tag]
		if cap(dist) >= n {
			dist = dist[:n]
		} else {
			dist = make([]uint16, n)
		}
		for i := range dist {
			dist[i] = demandUnreachable
		}

		// BFS queue — local indices.
		queue := make([]int, 0, 64)

		// Seed: all vacant nodes matching this tag.
		for _, localIdx := range b.vacantIdx[tag] {
			if localIdx < n && !b.Nodes[localIdx].OccupiedUnsafe() {
				dist[localIdx] = 0
				queue = append(queue, localIdx)
			}
		}

		// Seed: boundary demand signals for this tag.
		for _, sig := range b.boundaryDemand {
			if sig.Tag != tag {
				continue
			}
			localIdx := int(sig.EntryID) - int(b.Start)
			if localIdx < 0 || localIdx >= n {
				continue
			}
			if sig.Distance < dist[localIdx] {
				dist[localIdx] = sig.Distance
				queue = append(queue, localIdx)
			}
		}

		// BFS expansion — only within this block.
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			curDist := dist[cur]
			next := curDist + 1

			for _, nb := range b.Nodes[cur].NeighborsUnsafe() {
				if !bm.isInBlock(nb.ID(), b.ID) {
					continue // cross-block neighbor, skip
				}
				localNb := int(nb.ID()) - int(b.Start)
				if localNb < 0 || localNb >= n {
					continue
				}
				if next < dist[localNb] {
					dist[localNb] = next
					queue = append(queue, localNb)
				}
			}
		}

		b.demandMap[tag] = dist
	}

	// Clear boundary signals after integration.
	b.boundaryDemand = b.boundaryDemand[:0]
}

// demandAt returns the BFS distance to the nearest vacancy matching tag
// at the given local index. Returns demandUnreachable if no path.
func (b *Block) demandAt(tag string, localIdx int) uint16 {
	dist := b.demandMap[tag]
	if localIdx < 0 || localIdx >= len(dist) {
		return demandUnreachable
	}
	return dist[localIdx]
}

// propagateBoundaryDemand pushes demand signals to adjacent blocks.
// For each boundary node in this block that has a finite demand distance,
// emit a signal to each cross-block neighbor so the demand wavefront
// extends across block boundaries.
func (b *Block) propagateBoundaryDemand(bm *BlockMap) {
	n := len(b.Nodes)
	for tag, dist := range b.demandMap {
		for localIdx := range n {
			d := dist[localIdx]
			if d == demandUnreachable {
				continue
			}
			// Check if this node has any cross-block neighbors.
			for _, nb := range b.Nodes[localIdx].NeighborsUnsafe() {
				nbBlock := bm.NodeToBlock[nb.ID()]
				if nbBlock == b.ID {
					continue // same block
				}
				// Push demand signal to the neighboring block.
				// The entry point is the neighbor node in that block.
				sig := BoundarySignal{
					Tag:      tag,
					Distance: d + 1,
					EntryID:  nb.ID(),
				}
				target := bm.Blocks[nbBlock]
				if target.state == BlockStripped {
					target.deferredDemand = append(target.deferredDemand, sig)
				} else {
					target.boundaryDemand = append(target.boundaryDemand, sig)
				}
			}
		}
	}
}

// rebuildVacantIdx reconstructs the block-local vacant index from the
// current node state. Called after thawing a block from disk.
func (b *Block) rebuildVacantIdx() {
	b.vacantIdx = make(map[string][]int)
	for localIdx, node := range b.Nodes {
		if !node.OccupiedUnsafe() {
			for _, c := range node.Constraints() {
				tag := c.Tag()
				b.vacantIdx[tag] = append(b.vacantIdx[tag], localIdx)
			}
		}
	}
}

// --- BlockMap paging operations ---

// pagingEnabled reports whether demand paging is active.
func (bm *BlockMap) pagingEnabled() bool {
	return bm.maxResident > 0 && bm.maxResident < len(bm.Blocks)
}

// loadNeededBlocks scans for stripped blocks that have deferred work
// and loads them. Called between rounds (Phase 0).
func (bm *BlockMap) loadNeededBlocks() error {
	for _, b := range bm.Blocks {
		if b.state != BlockStripped {
			continue
		}
		if len(b.deferredSpills) == 0 && len(b.deferredDemand) == 0 {
			continue
		}
		if err := bm.loadBlock(b); err != nil {
			return err
		}
	}
	return nil
}

// loadBlock thaws a stripped block from disk. If at capacity, evicts
// a victim block first.
func (bm *BlockMap) loadBlock(b *Block) error {
	// Make room if at capacity.
	if bm.residentCount >= bm.maxResident {
		victim := bm.pickEvictionVictim(b.ID)
		if victim != nil {
			if err := bm.stripBlock(victim); err != nil {
				return err
			}
		}
	}

	// Thaw from disk.
	if err := thawBlock(b, bm.blockDir, bm.constraintFactory); err != nil {
		return err
	}

	// Transfer deferred work to normal queues.
	b.spillMu.Lock()
	b.spillover = append(b.spillover, b.deferredSpills...)
	b.deferredSpills = b.deferredSpills[:0]
	b.spillMu.Unlock()

	b.boundaryDemand = append(b.boundaryDemand, b.deferredDemand...)
	b.deferredDemand = b.deferredDemand[:0]

	// Rebuild block-local state.
	b.rebuildVacantIdx()
	b.state = BlockResident
	b.active = true
	bm.residentCount++
	return nil
}

// stripBlock freezes a block to disk and strips its nodes to skeleton.
// The block transitions to BlockStripped state.
func (bm *BlockMap) stripBlock(b *Block) error {
	if b.state != BlockResident {
		return nil // already stripped
	}

	// Freeze to disk (always freeze — correctness over avoiding writes).
	if err := freezeBlock(b, bm.blockDir); err != nil {
		return err
	}
	b.hasDiskCopy = true

	// Strip node payloads in place — preserves id + neighbor pointers.
	for _, n := range b.Nodes {
		n.StripForEvictionUnsafe()
	}

	// Move any in-flight spillover to deferred so it survives stripping.
	b.spillMu.Lock()
	if len(b.spillover) > 0 {
		b.deferredSpills = append(b.deferredSpills, b.spillover...)
		b.spillover = b.spillover[:0]
	}
	if len(b.inbound) > 0 {
		for _, item := range b.inbound {
			b.deferredSpills = append(b.deferredSpills, item)
		}
		b.inbound = b.inbound[:0]
	}
	b.spillMu.Unlock()

	// Clear block-level caches.
	b.vacantIdx = nil
	b.demandMap = nil
	b.state = BlockStripped
	b.active = false
	bm.residentCount--
	return nil
}

// pickEvictionVictim selects a resident block to evict. Prefers blocks
// that are not active and have no pending spills. Avoids the block
// being loaded (excludeID).
func (bm *BlockMap) pickEvictionVictim(excludeID int) *Block {
	for _, b := range bm.Blocks {
		if b.ID == excludeID {
			continue
		}
		if b.state != BlockResident {
			continue
		}
		if !b.active && len(b.spillover) == 0 && len(b.inbound) == 0 {
			return b
		}
	}
	// All resident blocks are active — evict the first non-excluded resident.
	for _, b := range bm.Blocks {
		if b.ID == excludeID {
			continue
		}
		if b.state == BlockResident {
			return b
		}
	}
	return nil
}

// evictExhaustedBlocks strips blocks that were not active this round
// and have no pending work. Called after processing (Phase 7).
func (bm *BlockMap) evictExhaustedBlocks(activeSet map[int]bool) error {
	if bm.residentCount <= bm.maxResident {
		return nil
	}
	for _, b := range bm.Blocks {
		if bm.residentCount <= bm.maxResident {
			break
		}
		if b.state != BlockResident {
			continue
		}
		if activeSet[b.ID] {
			continue
		}
		if len(b.spillover) > 0 || len(b.inbound) > 0 {
			continue
		}
		if err := bm.stripBlock(b); err != nil {
			return err
		}
	}
	return nil
}
