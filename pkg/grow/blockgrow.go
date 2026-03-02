// Block-decomposed growth loop. Partitions the lattice into cache-local
// blocks, assigns each block exclusively to one worker per round, and
// uses boundary spillover queues as the only shared structure.
//
// Convergence: each round either bonds elements (reducing work) or
// saturates blocks (removing them from the active set). After 3
// consecutive stall rounds (no bonds, no reduction in spills), all
// remaining elements are expired.
package grow

import (
	"context"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/lattice"
)

// RunBlocked is the block-decomposed growth loop. Same channel protocol
// and event semantics as Run. For small lattices (< 2*BlockSize nodes),
// falls back to Run.
func RunBlocked(ctx context.Context, l *lattice.Lattice, solution <-chan axiom.Element, cfg Config, events chan<- Event) {
	blockSize := cfg.BlockSize
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}

	// Small lattice bypass — decomposition overhead exceeds benefit.
	if l.Size() < 2*blockSize {
		Run(ctx, l, solution, cfg, events)
		return
	}

	bm := buildBlockMap(l, blockSize)

	// Configure demand paging if requested.
	if cfg.MaxResidentBlocks > 0 && cfg.MaxResidentBlocks < len(bm.Blocks) {
		bm.maxResident = cfg.MaxResidentBlocks
		bm.blockDir = cfg.BlockDir
		bm.constraintFactory = cfg.ConstraintFactory
		bm.lat = l
		bm.residentCount = len(bm.Blocks) // all start resident

		// Ensure block directory exists.
		os.MkdirAll(bm.blockDir, 0o755)

		// Evict excess blocks down to budget. Keep blocks with
		// the lowest IDs resident (arbitrary but deterministic).
		for i := len(bm.Blocks) - 1; i >= 0 && bm.residentCount > bm.maxResident; i-- {
			bm.stripBlock(bm.Blocks[i])
		}
	}

	// Drain solution channel into per-block element queues.
	// Use global VacantByTag to find the best starting block for each element.
	// The drain uses a short idle timeout: when no elements arrive within 50ms,
	// we assume the injection goroutine has finished and move to processing.
	// This avoids consuming the growth context budget on the drain phase.
	blockQueues := make([][]axiom.Element, len(bm.Blocks))
	robin := 0
	idleTimeout := 500 * time.Millisecond
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()
drainLoop:
	for {
		select {
		case <-ctx.Done():
			break drainLoop
		case elem, ok := <-solution:
			if !ok {
				break drainLoop
			}
			placed := false
			// Try directed placement via the lattice's vascular index.
			if n := l.VacantByTag(elem.Type()); n != nil {
				bid := bm.NodeToBlock[n.ID()]
				blockQueues[bid] = append(blockQueues[bid], elem)
				placed = true
			}
			if !placed {
				blockQueues[robin%len(bm.Blocks)] = append(blockQueues[robin%len(bm.Blocks)], elem)
				robin++
			}
			// Reset idle timer on each received element.
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleTimeout)
		case <-idleTimer.C:
			// No elements received within idle window — injection complete.
			break drainLoop
		}
	}

	// Round loop.
	workers := cfg.Workers
	if workers <= 0 {
		workers = WorkerCount()
	}
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 100 // safety cap
	}

	prevSpillCount := -1
	stallRounds := 0
	const maxStall = 3

	for round := range maxRounds {
		_ = round

		select {
		case <-ctx.Done():
			return
		default:
		}

		// Phase 0: LOAD — thaw stripped blocks that received deferred work
		// or have queued elements from the solution distribution.
		if bm.pagingEnabled() {
			// Check blockQueues for stripped blocks — treat as deferred work.
			for _, b := range bm.Blocks {
				if b.state == BlockStripped && len(blockQueues[b.ID]) > 0 {
					for _, elem := range blockQueues[b.ID] {
						b.deferredSpills = append(b.deferredSpills, SpillItem{Element: elem})
					}
					blockQueues[b.ID] = nil
				}
			}
			if err := bm.loadNeededBlocks(); err != nil {
				return // disk error — bail
			}
		}

		// Phase 1: Move spillover → inbound for resident blocks.
		for _, b := range bm.Blocks {
			if b.state == BlockResident {
				b.drainSpill()
			}
		}

		// Phase 2: Build demand maps (BFS from vacancies).
		for _, b := range bm.Blocks {
			if b.state == BlockResident {
				b.buildDemandMap(bm)
			}
		}

		// Phase 3: Propagate demand across block boundaries.
		for _, b := range bm.Blocks {
			if b.state == BlockResident {
				b.propagateBoundaryDemand(bm)
			}
		}

		// Phase 4: Merge block queues + inbound into work lists.
		type blockWork struct {
			block    *Block
			elements []axiom.Element
		}
		var active []blockWork
		activeSet := make(map[int]bool)
		for _, b := range bm.Blocks {
			if b.state != BlockResident {
				continue
			}
			var work []axiom.Element
			if len(blockQueues[b.ID]) > 0 {
				work = append(work, blockQueues[b.ID]...)
				blockQueues[b.ID] = nil
			}
			if len(b.inbound) > 0 {
				for _, item := range b.inbound {
					work = append(work, item.Element)
				}
				b.inbound = b.inbound[:0]
			}
			if len(work) > 0 {
				b.active = true
				active = append(active, blockWork{block: b, elements: work})
				activeSet[b.ID] = true
			} else {
				b.active = false
			}
		}

		if len(active) == 0 {
			break
		}

		// Phase 5: Process active blocks in parallel.
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)

		var evMu sync.Mutex
		var roundEvents []Event

		for _, bw := range active {
			wg.Add(1)
			sem <- struct{}{} // acquire worker slot
			go func(b *Block, elems []axiom.Element) {
				defer func() { <-sem; wg.Done() }()
				evs := processBlock(ctx, b, bm, elems, cfg.MaxSteps)
				evMu.Lock()
				roundEvents = append(roundEvents, evs...)
				evMu.Unlock()
			}(bw.block, bw.elements)
		}
		wg.Wait()

		// Phase 6: Emit events.
		for _, ev := range roundEvents {
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}

		// Phase 7: EVICT — strip exhausted blocks to make room.
		if bm.pagingEnabled() {
			if err := bm.evictExhaustedBlocks(activeSet); err != nil {
				return // disk error — bail
			}
		}

		// Phase 8: Convergence / stall detection.
		spillCount := 0
		for _, b := range bm.Blocks {
			spillCount += len(b.spillover)
			spillCount += len(b.deferredSpills)
		}

		if spillCount == 0 {
			break // converged
		}

		if spillCount >= prevSpillCount && prevSpillCount >= 0 {
			stallRounds++
		} else {
			stallRounds = 0
		}
		prevSpillCount = spillCount

		if stallRounds >= maxStall {
			// Expire all remaining spill items (resident + deferred).
			for _, b := range bm.Blocks {
				b.spillMu.Lock()
				for _, item := range b.spillover {
					select {
					case events <- Event{Type: EventExpired, Element: item.Element}:
					case <-ctx.Done():
						b.spillMu.Unlock()
						return
					}
				}
				b.spillover = b.spillover[:0]
				for _, item := range b.deferredSpills {
					select {
					case events <- Event{Type: EventExpired, Element: item.Element}:
					case <-ctx.Done():
						b.spillMu.Unlock()
						return
					}
				}
				b.deferredSpills = b.deferredSpills[:0]
				b.spillMu.Unlock()
			}
			break
		}
	}

	// Restore all stripped blocks so the lattice reflects the final state.
	if bm.pagingEnabled() {
		for _, b := range bm.Blocks {
			if b.state == BlockStripped && b.hasDiskCopy {
				thawBlock(b, bm.blockDir, bm.constraintFactory)
				b.state = BlockResident
				bm.residentCount++
			}
		}
	}
}

// processBlock runs all walks for one block in one round.
// Returns events for bonded/rejected/expired elements. Spilled elements
// are deposited directly into neighbor blocks' spillover queues.
func processBlock(ctx context.Context, b *Block, bm *BlockMap, elements []axiom.Element, maxSteps int) []Event {
	events := make([]Event, 0, len(elements))
	for _, elem := range elements {
		select {
		case <-ctx.Done():
			return events
		default:
		}
		ev := blockWalk(ctx, b, bm, elem, maxSteps)
		if ev != nil {
			events = append(events, *ev)
		}
	}
	return events
}

// blockWalk performs a wavefront-guided walk within a single block.
// The demand map (BFS distance to nearest matching vacancy) provides
// the gradient. The element follows the gradient downhill, bonding
// when it reaches a vacancy. If no demand exists in this block for the
// element's type, it spills to a neighbor block with closer demand.
//
// Returns nil for spilled elements (no event — continues next round).
func blockWalk(ctx context.Context, b *Block, bm *BlockMap, elem axiom.Element, maxSteps int) *Event {
	tag := elem.Type()

	// Find the best starting node: lowest demand distance for this tag.
	var current *lattice.Node
	bestDist := uint16(demandUnreachable)

	dist := b.demandMap[tag]
	if len(dist) > 0 {
		for localIdx, d := range dist {
			if d < bestDist {
				bestDist = d
				current = b.Nodes[localIdx]
			}
		}
	}

	// No demand for this tag in this block — spill to the neighbor
	// block with the closest boundary demand.
	if bestDist == demandUnreachable {
		return spillToClosestDemand(b, bm, elem, tag)
	}

	// If the best starting point is the vacancy itself, bond directly.
	if bestDist == 0 && current != nil && current.AdmitsUnsafe(elem) {
		if current.BondUnsafe(elem) {
			b.removeFromVacant(current)
			ev := Event{Type: EventBonded, NodeID: current.ID(), Element: elem, Steps: 0}
			return &ev
		}
	}

	if current == nil {
		ev := Event{Type: EventRejected, Element: elem}
		return &ev
	}

	// Gradient descent: follow the demand map downhill.
	for step := range maxSteps {
		if step&0xF == 0 {
			select {
			case <-ctx.Done():
				ev := Event{Type: EventExpired, Element: elem, Steps: step}
				return &ev
			default:
			}
		}

		// Try bond at current node.
		if current.AdmitsUnsafe(elem) {
			if current.BondUnsafe(elem) {
				b.removeFromVacant(current)
				ev := Event{Type: EventBonded, NodeID: current.ID(), Element: elem, Steps: step}
				return &ev
			}
		}

		// Follow gradient: pick the neighbor with the lowest demand distance.
		neighbors := current.NeighborsUnsafe()
		curLocalIdx := int(current.ID()) - int(b.Start)
		curDist := uint16(demandUnreachable)
		if curLocalIdx >= 0 && curLocalIdx < len(dist) {
			curDist = dist[curLocalIdx]
		}

		var bestNb *lattice.Node
		bestNbDist := curDist // must improve on current
		var spillCandidate *lattice.Node

		for _, nb := range neighbors {
			if !bm.isInBlock(nb.ID(), b.ID) {
				spillCandidate = nb
				continue
			}
			// Try immediate bond on neighbor.
			if nb.AdmitsUnsafe(elem) {
				if nb.BondUnsafe(elem) {
					b.removeFromVacant(nb)
					ev := Event{Type: EventBonded, NodeID: nb.ID(), Element: elem, Steps: step}
					return &ev
				}
			}
			nbLocalIdx := int(nb.ID()) - int(b.Start)
			if nbLocalIdx >= 0 && nbLocalIdx < len(dist) {
				d := dist[nbLocalIdx]
				if d < bestNbDist {
					bestNbDist = d
					bestNb = nb
				}
			}
		}

		if bestNb != nil {
			current = bestNb
			continue
		}

		// No downhill neighbor — gradient bottomed out.
		// Spill to adjacent block if there's a cross-block neighbor.
		if spillCandidate != nil {
			targetBlockID := bm.NodeToBlock[spillCandidate.ID()]
			bm.Blocks[targetBlockID].pushSpill(SpillItem{Element: elem})
			return nil
		}

		// Dead end — no in-block improvement and no cross-block escape.
		// Try a random in-block neighbor to escape local minimum.
		var inBlock [8]*lattice.Node
		inBlockSlice := inBlock[:0]
		for _, nb := range neighbors {
			if bm.isInBlock(nb.ID(), b.ID) {
				inBlockSlice = append(inBlockSlice, nb)
			}
		}
		if len(inBlockSlice) > 0 {
			current = inBlockSlice[rand.IntN(len(inBlockSlice))]
			continue
		}

		ev := Event{Type: EventRejected, Element: elem, Steps: maxSteps}
		return &ev
	}

	ev := Event{Type: EventExpired, Element: elem, Steps: maxSteps}
	return &ev
}

// spillToClosestDemand spills an element to the neighbor block most
// likely to have demand for the element's type. Checks boundary nodes
// for cross-block neighbors and picks the block with any demand signal
// for the tag. Falls back to any neighbor block. Prefers resident blocks
// over stripped blocks to avoid deferred-queue ping-pong.
func spillToClosestDemand(b *Block, bm *BlockMap, elem axiom.Element, tag string) *Event {
	// Pass 1: find a resident neighbor block with demand for this tag.
	for _, node := range b.Nodes {
		for _, nb := range node.NeighborsUnsafe() {
			nbBlockID := bm.NodeToBlock[nb.ID()]
			if nbBlockID == b.ID {
				continue
			}
			target := bm.Blocks[nbBlockID]
			if target.state != BlockResident {
				continue
			}
			if targetDist := target.demandMap[tag]; len(targetDist) > 0 {
				for _, d := range targetDist {
					if d < demandUnreachable {
						target.pushSpill(SpillItem{Element: elem})
						return nil
					}
				}
			}
		}
	}

	// Pass 2: any resident neighbor block (even without demand for this tag).
	for _, node := range b.Nodes {
		for _, nb := range node.NeighborsUnsafe() {
			nbBlockID := bm.NodeToBlock[nb.ID()]
			if nbBlockID == b.ID {
				continue
			}
			target := bm.Blocks[nbBlockID]
			if target.state == BlockResident {
				target.pushSpill(SpillItem{Element: elem})
				return nil
			}
		}
	}

	// Pass 3: any neighbor block (may be stripped — triggers load next round).
	for _, node := range b.Nodes {
		for _, nb := range node.NeighborsUnsafe() {
			nbBlockID := bm.NodeToBlock[nb.ID()]
			if nbBlockID != b.ID {
				bm.Blocks[nbBlockID].pushSpill(SpillItem{Element: elem})
				return nil
			}
		}
	}

	// Completely isolated block — reject.
	ev := Event{Type: EventRejected, Element: elem}
	return &ev
}
