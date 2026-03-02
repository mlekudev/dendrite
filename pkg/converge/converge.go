// Package converge tracks lattice convergence during training.
//
// The lattice is considered converged when new text stops requiring new
// vertices. This is measured as the vertex creation rate: new vertices
// per token ingested. The rate follows a power law decay — fast growth
// early as basic structures are captured, then a long tail as rare
// constructions trickle in. Convergence is declared when the rate drops
// below a configurable threshold.
package converge

import (
	"encoding/json"
	"sync"

	"github.com/mlekudev/dendrite/pkg/ratio"
)

// DefaultThreshold is the default convergence threshold: 1 new vertex
// per 1,000,000 tokens. Below this rate, the lattice has captured
// essentially all structure present in the input distribution.
var DefaultThreshold = ratio.New(1, 1_000_000)

// DefaultWindowSize is the number of tokens per measurement window.
const DefaultWindowSize int64 = 100_000

// WindowStats records counts for a single measurement window.
type WindowStats struct {
	TokensProcessed int64       `json:"tokens_processed"`
	NewVertices     int64       `json:"new_vertices"`
	VertexRate      ratio.Ratio `json:"vertex_rate"` // new_vertices / tokens_processed
}

// Report summarizes the convergence state.
type Report struct {
	TotalTokens      int64         `json:"total_tokens"`
	TotalNewVertices int64         `json:"total_new_vertices"`
	CurrentRate      ratio.Ratio   `json:"current_rate"`
	Converged        bool          `json:"converged"`
	WindowCount      int           `json:"window_count"`
	RecentWindows    []WindowStats `json:"recent_windows"`
}

// Tracker monitors vertex creation rate over sliding windows.
type Tracker struct {
	mu sync.Mutex

	windowSize int64
	threshold  ratio.Ratio

	// Current window accumulators.
	windowTokens   int64
	windowVertices int64

	// Completed windows.
	windows []WindowStats

	// Lifetime totals.
	totalTokens   int64
	totalVertices int64
}

// NewTracker creates a convergence tracker with the given window size
// and convergence threshold.
func NewTracker(windowSize int64, threshold ratio.Ratio) *Tracker {
	if windowSize <= 0 {
		windowSize = DefaultWindowSize
	}
	if threshold.IsZero() {
		threshold = DefaultThreshold
	}
	return &Tracker{
		windowSize: windowSize,
		threshold:  threshold,
	}
}

// RecordToken increments the token counter by one.
// If the current window is full, it is finalized and a new one starts.
func (t *Tracker) RecordToken() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalTokens++
	t.windowTokens++
	if t.windowTokens >= t.windowSize {
		t.finalizeWindow()
	}
}

// RecordTokens increments the token counter by n.
func (t *Tracker) RecordTokens(n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.totalTokens += n
	t.windowTokens += n
	for t.windowTokens >= t.windowSize {
		t.finalizeWindow()
	}
}

// RecordNewVertex increments the new vertex counter.
func (t *Tracker) RecordNewVertex() {
	t.mu.Lock()
	t.totalVertices++
	t.windowVertices++
	t.mu.Unlock()
}

// finalizeWindow closes the current measurement window and starts a new one.
// Must be called with lock held.
func (t *Tracker) finalizeWindow() {
	ws := WindowStats{
		TokensProcessed: t.windowTokens,
		NewVertices:     t.windowVertices,
	}
	if t.windowTokens > 0 {
		ws.VertexRate = ratio.New(t.windowVertices, t.windowTokens)
	}
	t.windows = append(t.windows, ws)
	t.windowTokens = 0
	t.windowVertices = 0
}

// IsConverged reports whether the vertex creation rate in recent windows
// is below the threshold. Requires at least 3 completed windows to avoid
// false convergence on small inputs.
func (t *Tracker) IsConverged() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.windows) < 3 {
		return false
	}

	// Check the last 3 windows. All must be below threshold.
	for i := len(t.windows) - 3; i < len(t.windows); i++ {
		if t.windows[i].VertexRate.Greater(t.threshold) {
			return false
		}
	}
	return true
}

// Report returns a summary of the current convergence state.
func (t *Tracker) Report() Report {
	t.mu.Lock()
	defer t.mu.Unlock()

	r := Report{
		TotalTokens:      t.totalTokens,
		TotalNewVertices: t.totalVertices,
		WindowCount:      len(t.windows),
	}

	if t.totalTokens > 0 {
		r.CurrentRate = ratio.New(t.totalVertices, t.totalTokens)
	}

	// Converged check (same logic as IsConverged but without re-locking).
	if len(t.windows) >= 3 {
		r.Converged = true
		for i := len(t.windows) - 3; i < len(t.windows); i++ {
			if t.windows[i].VertexRate.Greater(t.threshold) {
				r.Converged = false
				break
			}
		}
	}

	// Include the last 10 windows for display.
	start := 0
	if len(t.windows) > 10 {
		start = len(t.windows) - 10
	}
	r.RecentWindows = make([]WindowStats, len(t.windows)-start)
	copy(r.RecentWindows, t.windows[start:])

	return r
}

// Marshal serializes the tracker state to JSON for persistence.
func (t *Tracker) Marshal() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return json.Marshal(struct {
		WindowSize    int64         `json:"window_size"`
		Windows       []WindowStats `json:"windows"`
		TotalTokens   int64         `json:"total_tokens"`
		TotalVertices int64         `json:"total_vertices"`
	}{
		WindowSize:    t.windowSize,
		Windows:       t.windows,
		TotalTokens:   t.totalTokens,
		TotalVertices: t.totalVertices,
	})
}
