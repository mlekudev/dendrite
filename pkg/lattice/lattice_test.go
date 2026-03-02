package lattice

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/axiom"
	"github.com/mlekudev/dendrite/pkg/ratio"
	"github.com/mlekudev/dendrite/pkg/state"
)

// testElement is a minimal element for testing.
type testElement struct {
	tag string
	val string
}

func (e testElement) Type() string { return e.tag }
func (e testElement) Value() any   { return e.val }

// testConstraint admits elements with a matching type tag.
type testConstraint struct {
	tag string
}

func (c testConstraint) Tag() string              { return c.tag }
func (c testConstraint) Admits(e axiom.Element) bool { return e.Type() == c.tag }

func TestAddNodeAndSize(t *testing.T) {
	l := New()
	if l.Size() != 0 {
		t.Fatal("new lattice should be empty")
	}
	c := []axiom.Constraint{testConstraint{"word"}}
	n := l.AddNode(c)
	if l.Size() != 1 {
		t.Fatal("expected size 1")
	}
	if n.ID() != 0 {
		t.Fatal("first node ID should be 0")
	}
}

func TestBondAndDissolve(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	// Should admit matching element.
	e := testElement{"word", "hello"}
	if !n.Admits(e) {
		t.Fatal("node should admit matching element")
	}

	// Bond.
	if !n.Bond(e) {
		t.Fatal("bond should succeed")
	}
	if !n.Occupied() {
		t.Fatal("node should be occupied after bond")
	}

	// Should not admit when occupied.
	e2 := testElement{"word", "world"}
	if n.Admits(e2) {
		t.Fatal("occupied node should not admit")
	}

	// Second bond should fail.
	if n.Bond(e2) {
		t.Fatal("second bond should fail")
	}

	// Dissolve.
	dissolved := n.Dissolve()
	if dissolved == nil {
		t.Fatal("dissolve should return element")
	}
	if dissolved.(testElement).val != "hello" {
		t.Fatal("dissolved element should be the one that was bonded")
	}
	if n.Occupied() {
		t.Fatal("node should be vacant after dissolve")
	}
}

func TestConstraintRejection(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	// Wrong type should be rejected.
	e := testElement{"number", "42"}
	if n.Admits(e) {
		t.Fatal("node should reject mismatched type")
	}
	if n.Bond(e) {
		t.Fatal("bond should fail for mismatched type")
	}
}

func TestConnect(t *testing.T) {
	l := New()
	a := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	b := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	l.Connect(a, b)

	nb := RandomNeighbor(a)
	if nb == nil || nb.ID() != b.ID() {
		t.Fatal("a's neighbor should be b")
	}
	nb = RandomNeighbor(b)
	if nb == nil || nb.ID() != a.ID() {
		t.Fatal("b's neighbor should be a")
	}
}

func TestDisconnect(t *testing.T) {
	l := New()
	a := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	b := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	l.Connect(a, b)
	l.Disconnect(a, b)

	if RandomNeighbor(a) != nil {
		t.Fatal("a should have no neighbors after disconnect")
	}
}

func TestVacantSites(t *testing.T) {
	l := New()
	l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n3 := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	// Bond one node.
	n3.Bond(testElement{"word", "taken"})

	sites := l.VacantSites()
	if len(sites) != 2 {
		t.Fatalf("expected 2 vacant sites, got %d", len(sites))
	}
}

func TestHexagramUpdatesOnBond(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	// Before bond: vacant node. bonding=false, constraint=false, energy=false -> Earth (000)
	// The constraint bit reflects the occupant being bound, not the
	// existence of constraints on the site.
	h := n.Hexagram()
	if h.Inner() != state.Earth {
		t.Errorf("expected Earth (000) before bond, got %03b", h.Inner())
	}

	n.Bond(testElement{"word", "hello"})

	// After bond: bonding=true, constraint=true (bound by constraints), energy=false -> Lake (011)
	h = n.Hexagram()
	if h.Inner() != state.Lake {
		t.Errorf("expected Lake (011) after bond, got %03b", h.Inner())
	}
}

func TestNoConstraintNoAdmit(t *testing.T) {
	l := New()
	n := l.AddNode(nil) // no constraints

	e := testElement{"word", "hello"}
	if n.Admits(e) {
		t.Fatal("node with no constraints should not admit anything")
	}
}

func TestRandomNode(t *testing.T) {
	l := New()
	if l.RandomNode() != nil {
		t.Fatal("empty lattice should return nil")
	}
	l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	if l.RandomNode() == nil {
		t.Fatal("non-empty lattice should return a node")
	}
}

func TestLockInDepth(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{
		testConstraint{"word"},
	})
	n.Bond(testElement{"word", "hello"})
	if !n.LockIn().Equal(ratio.One) {
		t.Errorf("expected lock-in 1/1, got %s", n.LockIn())
	}

	// More constraints = deeper lock-in.
	n2 := l.AddNode([]axiom.Constraint{
		testConstraint{"word"},
		multiConstraint{"word", 3}, // requires length >= 3
	})
	n2.Bond(testElement{"word", "hello"})
	if !n2.LockIn().Equal(ratio.FromInt(2)) {
		t.Errorf("expected lock-in 2/1, got %s", n2.LockIn())
	}
}

func TestHealth(t *testing.T) {
	l := New()
	n1 := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n2 := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n3 := l.AddNode([]axiom.Constraint{testConstraint{"punct"}})
	n1.SetEnergy(true)
	n2.SetEnergy(true)

	// Before bonding.
	h := l.Health()
	if h.NodeCount != 3 {
		t.Errorf("NodeCount = %d, want 3", h.NodeCount)
	}
	if h.Occupied != 0 {
		t.Errorf("Occupied = %d, want 0", h.Occupied)
	}
	if h.AccretionReady != 2 {
		t.Errorf("AccretionReady = %d, want 2 (n1 and n2 have energy)", h.AccretionReady)
	}

	// Bond one node.
	n1.Bond(testElement{"word", "hello"})
	h = l.Health()
	if h.Occupied != 1 {
		t.Errorf("after bond: Occupied = %d, want 1", h.Occupied)
	}
	if !h.AvgLockIn.Equal(ratio.One) {
		t.Errorf("AvgLockIn = %s, want 1/1", h.AvgLockIn)
	}
	if !h.OccupancyRate.Equal(ratio.New(1, 3)) {
		t.Errorf("OccupancyRate = %s, want 1/3", h.OccupancyRate)
	}

	_ = n2
	_ = n3
}

func TestAgeOnBond(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n.Bond(testElement{"word", "hello"})

	if n.Age() != 0 {
		t.Errorf("expected age 0 after bond, got %d", n.Age())
	}
}

func TestIncrementAge(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n.Bond(testElement{"word", "hello"})

	// Attack (0) → Decay (1) → Sustain (2): automatic.
	n.IncrementAge()
	if n.Age() != 1 {
		t.Errorf("expected age 1 (Decay), got %d", n.Age())
	}
	n.IncrementAge()
	if n.Age() != 2 {
		t.Errorf("expected age 2 (Sustain), got %d", n.Age())
	}

	// Sustain saturates — IncrementAge does not advance past 2.
	n.IncrementAge()
	if n.Age() != 2 {
		t.Errorf("age should saturate at 2 (Sustain), got %d", n.Age())
	}
	n.IncrementAge()
	if n.Age() != 2 {
		t.Errorf("age should still be 2, got %d", n.Age())
	}

	// Destabilize moves Sustain → Release.
	n.Destabilize()
	if n.Age() != 3 {
		t.Errorf("expected age 3 (Release) after Destabilize, got %d", n.Age())
	}

	// Destabilize is a no-op when not in Sustain.
	n.Destabilize()
	if n.Age() != 3 {
		t.Errorf("Destabilize should be no-op in Release, got %d", n.Age())
	}
}

func TestDissolveResetsAge(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})
	n.Bond(testElement{"word", "hello"})
	n.IncrementAge()
	n.IncrementAge()

	if n.Age() != 2 {
		t.Fatalf("expected age 2 before dissolve, got %d", n.Age())
	}

	n.Dissolve()

	if n.Age() != 0 {
		t.Errorf("expected age 0 after dissolve, got %d", n.Age())
	}
}

func TestProjectionByte(t *testing.T) {
	l := New()
	n := l.AddNode([]axiom.Constraint{testConstraint{"word"}})

	n.SetProjection(0b101, 0b011, 0) // vertex=5, key=3
	n.mu.Lock()
	n.age = 2
	n.mu.Unlock()

	got := n.ProjectionByte()
	// age=2 (0b10) << 6 | key=3 (0b011) << 3 | vertex=5 (0b101)
	// = 0b10_011_101 = 0x9D = 157
	want := uint8(0b10_011_101)
	if got != want {
		t.Errorf("ProjectionByte: got 0b%08b, want 0b%08b", got, want)
	}
}

// multiConstraint admits elements with matching tag and value length >= min.
type multiConstraint struct {
	tag    string
	minLen int
}

func (c multiConstraint) Tag() string { return c.tag }
func (c multiConstraint) Admits(e axiom.Element) bool {
	if e.Type() != c.tag {
		return false
	}
	s, ok := e.Value().(string)
	if !ok {
		return false
	}
	return len(s) >= c.minLen
}
