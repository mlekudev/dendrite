package grammar

import (
	"testing"

	"github.com/mlekudev/dendrite/pkg/ratio"
)

func TestGoASTCanNeighbor(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Functions contain body statements.
		{"func", "assign", true},
		{"func", "return", true},
		{"func", "if", true},
		{"func", "for", true},
		{"func", "ident:func-name", true},

		// Functions don't directly neighbor package/import.
		{"func", "package", false},
		{"func", "import", false},

		// Package neighbors import and declarations.
		{"package", "import", true},
		{"package", "func", true},
		{"package", "type", true},

		// Type contains struct/interface/field.
		{"type", "struct", true},
		{"type", "interface", true},
		{"type", "field", true},

		// Body statements neighbor each other and declaration-level idents.
		{"assign", "return", true},
		{"if", "for", true},
		{"assign", "ident:var-name", true},

		// Text fallback.
		{"word", "punct", true},
		{"word", "word", true},
		{"punct", "punct", true},

		// Cross-domain: import doesn't neighbor body statements.
		{"import", "assign", false},
		{"import", "return", false},

		// Struct doesn't directly neighbor import.
		{"struct", "import", false},

		// Ident subtypes have role-specific adjacency.
		{"ident:func-name", "func", true},
		{"ident:type-name", "type", true},
		{"ident:field-name", "struct", true},
		{"ident:receiver", "method", true},
		{"ident:param", "func", true},
	}

	for _, tt := range tests {
		got := GoAST.CanNeighbor(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("GoAST.CanNeighbor(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestGoEmitTighter(t *testing.T) {
	// GoEmit should be tighter than GoAST in some cases.
	// func should only neighbor body statements in GoEmit (not ident subtypes).
	if GoEmit.CanNeighbor("func", "ident:func-name") {
		t.Error("GoEmit: func should not neighbor ident:func-name (tighter scoping)")
	}
	// But func should still neighbor assign.
	if !GoEmit.CanNeighbor("func", "assign") {
		t.Error("GoEmit: func should neighbor assign")
	}
	// import should only neighbor package.
	if !GoEmit.CanNeighbor("import", "package") {
		t.Error("GoEmit: import should neighbor package")
	}
	if GoEmit.CanNeighbor("import", "func") {
		t.Error("GoEmit: import should not neighbor func (tighter)")
	}
}

func TestGrammarTags(t *testing.T) {
	tags := GoAST.Tags()
	if len(tags) == 0 {
		t.Fatal("GoAST.Tags() returned empty")
	}
	// Should include core Go types.
	want := map[string]bool{
		"func": true, "assign": true, "return": true,
		"type": true, "struct": true, "ident:var-name": true,
		"word": true, "punct": true, "package": true,
	}
	tagSet := make(map[string]bool)
	for _, tag := range tags {
		tagSet[tag] = true
	}
	for tag := range want {
		if !tagSet[tag] {
			t.Errorf("GoAST.Tags() missing %q", tag)
		}
	}
}

func TestGrammarNeighborsOf(t *testing.T) {
	nbs := GoAST.NeighborsOf("func")
	if len(nbs) == 0 {
		t.Fatal("GoAST.NeighborsOf(func) returned empty")
	}
	// func should have assign, return, if, for among neighbors.
	nbSet := make(map[string]bool)
	for _, nb := range nbs {
		nbSet[nb] = true
	}
	for _, want := range []string{"assign", "return", "if", "for"} {
		if !nbSet[want] {
			t.Errorf("GoAST.NeighborsOf(func) missing %q", want)
		}
	}
}

func TestDefaultCounts(t *testing.T) {
	counts := GoAST.DefaultCounts(100, ratio.New(3, 5))

	// Should have entries for all grammar tags that have weight > 0.
	if counts["func"] == 0 {
		t.Error("func should have > 0 nodes")
	}
	if counts["assign"] == 0 {
		t.Error("assign should have > 0 nodes")
	}
	if counts["word"] == 0 {
		t.Error("word should have > 0 nodes")
	}
	if counts["punct"] == 0 {
		t.Error("punct should have > 0 nodes")
	}

	// Total should be close to targetSize.
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 100 {
		t.Errorf("total nodes = %d, want 100", total)
	}
}
