package lattice

import "sync"

// neighborPool caches []*Node slices to reduce GC pressure.
// Slices are returned with length 0 and capacity 8.
var neighborPool = sync.Pool{
	New: func() any { return make([]*Node, 0, 8) },
}

// getNeighborSlice returns a pre-allocated neighbor slice from the pool.
func getNeighborSlice() []*Node {
	return neighborPool.Get().([]*Node)[:0]
}

// putNeighborSlice returns a neighbor slice to the pool.
// All pointers are cleared to avoid retaining GC roots.
func putNeighborSlice(s []*Node) {
	clear(s)
	neighborPool.Put(s[:0])
}
