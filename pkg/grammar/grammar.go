// Package grammar defines adjacency rules for lattice element types.
//
// A grammar is a directed adjacency set: it specifies which element types
// can be neighbors in a lattice. This shapes the lattice topology to
// reflect the structure of the domain (e.g., Go AST hierarchy) rather
// than using flat random-node connectivity.
//
// The grammar is the rigid backbone — like the spinal cord in the nervous
// system, its shape is determined by what it connects to. Different
// grammars produce different topologies, and different seeds within the
// same grammar produce different wiring realizations.
package grammar

import (
	"sort"
	"sync"

	"github.com/mlekudev/dendrite/pkg/ratio"
)

// Rule defines a single adjacency: elements with Tag can neighbor elements
// with any of the Neighbors tags.
type Rule struct {
	Tag       string
	Neighbors []string
}

// Grammar is an ordered set of adjacency rules.
type Grammar struct {
	Rules    []Rule
	index    map[string]map[string]bool // lazily built: tag -> set of valid neighbors
	buildOne sync.Once
}

// build populates the index from Rules if not already built.
// Safe for concurrent use — sync.Once ensures single initialization.
func (g *Grammar) build() {
	g.buildOne.Do(func() {
		g.index = make(map[string]map[string]bool)
		for _, r := range g.Rules {
			if g.index[r.Tag] == nil {
				g.index[r.Tag] = make(map[string]bool)
			}
			for _, nb := range r.Neighbors {
				g.index[r.Tag][nb] = true
			}
		}
	})
}

// CanNeighbor reports whether an element of type a can be adjacent to
// an element of type b. The check is directional: a→b may be valid
// while b→a is not. Callers should check both directions for symmetric
// adjacency.
func (g *Grammar) CanNeighbor(a, b string) bool {
	g.build()
	return g.index[a][b]
}

// NeighborsOf returns the set of tags that can neighbor the given tag.
func (g *Grammar) NeighborsOf(tag string) []string {
	g.build()
	nbs := g.index[tag]
	out := make([]string, 0, len(nbs))
	for nb := range nbs {
		out = append(out, nb)
	}
	sort.Strings(out)
	return out
}

// Tags returns all tags that appear in the grammar (as sources of rules),
// sorted for deterministic ordering.
func (g *Grammar) Tags() []string {
	seen := make(map[string]bool)
	for _, r := range g.Rules {
		seen[r.Tag] = true
		for _, nb := range r.Neighbors {
			seen[nb] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// DefaultCounts returns a tag count map suitable for BuildGrammarLattice.
// It distributes targetSize nodes across all grammar tags using fixed
// proportions derived from typical Go source statistics. The wordFrac
// parameter controls the word/punct ratio for text enzyme fallback tags.
func (g *Grammar) DefaultCounts(targetSize int, wordFrac ratio.Ratio) map[string]int {
	// Proportional weights for Go AST element types.
	// These reflect typical Go source: more identifiers and literals
	// than switch/select statements. Body statements are weighted by
	// frequency in real codebases.
	weights := map[string]int{
		// Declarations — the skeleton.
		"package": 1, "import": 2, "func": 4, "method": 4, "type": 3,
		"struct": 2, "interface": 2, "field": 3, "var": 2, "directive": 1,
		// Body statements — the meat.
		"assign": 4, "return": 3, "if": 3, "for": 2, "switch": 1,
		"select": 1, "go": 1, "send": 1, "expr": 3, "defer": 1,
		"decl": 1, "branch": 1, "case": 1, "comm": 1,
		// Declaration-level ident subtypes.
		"ident:func-name": 1, "ident:method-name": 1, "ident:type-name": 1,
		"ident:field-name": 1, "ident:param": 1, "ident:result": 1,
		"ident:receiver": 1, "ident:var-name": 1,
		// Reference material.
		"grammar-rule": 2,
		// Other atoms.
		"comment": 1, "file": 1,
		// Text enzyme fallback — scaled by wordFrac.
		"word": 0, "punct": 0,
	}

	// Text fallback: allocate ~15% of nodes to word/punct.
	textNodes := max(2, targetSize*15/100)
	wordNodes := int(wordFrac.ScaleInt(int64(textNodes)))
	punctNodes := textNodes - wordNodes
	if punctNodes < 1 {
		punctNodes = 1
		wordNodes = textNodes - 1
	}
	weights["word"] = wordNodes
	weights["punct"] = punctNodes

	// Sum weights.
	totalWeight := 0
	for _, w := range weights {
		totalWeight += w
	}

	// Distribute remaining nodes proportionally.
	remaining := targetSize - textNodes
	if remaining < 1 {
		remaining = 1
	}

	counts := make(map[string]int)
	allocated := 0
	// Sort keys for deterministic allocation.
	keys := make([]string, 0, len(weights))
	for k := range weights {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	astWeight := totalWeight - weights["word"] - weights["punct"]
	if astWeight < 1 {
		astWeight = 1
	}

	for _, tag := range keys {
		w := weights[tag]
		if tag == "word" || tag == "punct" {
			counts[tag] = w
			allocated += w
			continue
		}
		n := remaining * w / astWeight
		if n < 1 && w > 0 {
			n = 1
		}
		counts[tag] = n
		allocated += n
	}

	// Distribute any rounding remainder to "ident:var-name" (most flexible).
	if allocated < targetSize {
		counts["ident:var-name"] += targetSize - allocated
	}

	return counts
}

// bodyStmtTags are the Go body statement element types.
var bodyStmtTags = []string{
	"assign", "return", "if", "for", "switch", "select",
	"go", "send", "expr", "defer", "decl", "branch", "case", "comm",
}

// IdentSubtypes lists the declaration-level ident subtypes emitted by the
// Go enzyme. Expression-level idents (ref, selector, call-target) and
// literals are carried inside rendered statement elements and not emitted
// as standalone elements.
var IdentSubtypes = []string{
	"ident:field-name",
	"ident:func-name",
	"ident:method-name",
	"ident:param",
	"ident:receiver",
	"ident:result",
	"ident:type-name",
	"ident:var-name",
}

// GoAST is the input grammar derived from Go AST structure.
// Functions contain body statements. Body statements reference identifiers.
// Types contain structs/interfaces/fields. Imports are adjacent to package.
var GoAST = &Grammar{Rules: func() []Rule {
	// Body statements can neighbor each other and declaration-level idents.
	bodyNeighbors := append(append([]string{}, IdentSubtypes...), bodyStmtTags...)

	rules := []Rule{
		// Package scope.
		{Tag: "package", Neighbors: []string{"import", "func", "method", "type", "var", "directive", "comment", "file"}},
		{Tag: "import", Neighbors: []string{"package", "func", "method", "type", "var"}},
		{Tag: "file", Neighbors: []string{"package", "import", "func", "method", "type", "comment"}},
		{Tag: "directive", Neighbors: []string{"func", "method", "type", "var", "package"}},
		{Tag: "comment", Neighbors: []string{"package", "func", "method", "type", "struct", "interface", "field", "var", "file"}},

		// Function declarations neighbor their body types and naming subtypes.
		{Tag: "func", Neighbors: append([]string{
			"type", "var", "comment",
			"ident:func-name", "ident:param", "ident:result", "ident:var-name",
		}, bodyStmtTags...)},
		{Tag: "method", Neighbors: append([]string{
			"type", "struct", "var", "comment",
			"ident:method-name", "ident:receiver", "ident:param", "ident:result", "ident:var-name",
		}, bodyStmtTags...)},

		// Type declarations.
		{Tag: "type", Neighbors: []string{
			"struct", "interface", "field", "func", "method", "comment",
			"ident:type-name",
		}},
		{Tag: "struct", Neighbors: []string{
			"field", "type", "method", "comment",
			"ident:field-name", "ident:type-name",
		}},
		{Tag: "interface", Neighbors: []string{
			"method", "type", "comment",
			"ident:method-name",
		}},
		{Tag: "field", Neighbors: []string{
			"struct", "type", "comment",
			"ident:field-name",
		}},
		{Tag: "var", Neighbors: []string{
			"type", "assign", "expr", "func",
			"ident:var-name",
		}},

		// Declaration-level ident subtypes.
		{Tag: "ident:func-name", Neighbors: []string{"func", "expr"}},
		{Tag: "ident:method-name", Neighbors: []string{"method", "interface", "expr"}},
		{Tag: "ident:type-name", Neighbors: []string{"type", "struct", "interface", "field"}},
		{Tag: "ident:field-name", Neighbors: []string{"struct", "field", "type"}},
		{Tag: "ident:param", Neighbors: []string{"func", "method"}},
		{Tag: "ident:result", Neighbors: []string{"func", "method"}},
		{Tag: "ident:receiver", Neighbors: []string{"method", "struct"}},
		{Tag: "ident:var-name", Neighbors: []string{"var", "assign", "func", "method"}},

		// Grammar rules from the Go spec — EBNF productions that describe
		// valid Go syntax. These neighbor all declaration and statement types
		// so the lattice can structurally associate grammar rules with the
		// code elements they describe.
		{Tag: "grammar-rule", Neighbors: []string{
			"func", "method", "type", "struct", "interface", "field",
			"import", "var", "assign", "return", "if", "for",
			"package", "expr", "switch", "select",
		}},

		// Text enzyme fallback.
		{Tag: "word", Neighbors: []string{"punct", "word", "comment"}},
		{Tag: "punct", Neighbors: []string{"word", "punct"}},
	}

	// Body statement types.
	for _, tag := range bodyStmtTags {
		rules = append(rules, Rule{Tag: tag, Neighbors: bodyNeighbors})
	}

	return rules
}()}

// GoEmit is the output grammar — tighter grouping for emission.
// Functions neighbor only their body types (no cross-function leakage).
// Types neighbor only their structural members.
var GoEmit = &Grammar{Rules: func() []Rule {
	rules := []Rule{
		{Tag: "package", Neighbors: []string{"import"}},
		{Tag: "import", Neighbors: []string{"package"}},
		{Tag: "file", Neighbors: []string{"package"}},
		{Tag: "directive", Neighbors: []string{"func", "method"}},

		// Functions are scoped to their bodies.
		{Tag: "func", Neighbors: bodyStmtTags},
		{Tag: "method", Neighbors: bodyStmtTags},

		// Types are scoped to their members.
		{Tag: "type", Neighbors: []string{"struct", "interface"}},
		{Tag: "struct", Neighbors: []string{"field"}},
		{Tag: "interface", Neighbors: []string{"method"}},
		{Tag: "field", Neighbors: []string{"struct", "ident:field-name"}},
		{Tag: "var", Neighbors: []string{"ident:var-name"}},

		// Declaration-level ident subtypes — tighter emission grouping.
		{Tag: "ident:func-name", Neighbors: []string{"func"}},
		{Tag: "ident:method-name", Neighbors: []string{"method", "interface"}},
		{Tag: "ident:type-name", Neighbors: []string{"type"}},
		{Tag: "ident:field-name", Neighbors: []string{"field", "struct"}},
		{Tag: "ident:param", Neighbors: []string{"func", "method"}},
		{Tag: "ident:result", Neighbors: []string{"func", "method"}},
		{Tag: "ident:receiver", Neighbors: []string{"method"}},
		{Tag: "ident:var-name", Neighbors: []string{"var"}},

		{Tag: "comment", Neighbors: []string{"func", "method", "type", "struct"}},

		// Grammar rules — not emitted, but can neighbor declarations.
		{Tag: "grammar-rule", Neighbors: []string{"func", "method", "type", "struct", "interface"}},

		// Text.
		{Tag: "word", Neighbors: []string{"punct", "word"}},
		{Tag: "punct", Neighbors: []string{"word", "punct"}},
	}

	// Body statements neighbor only each other (same scope).
	for _, tag := range bodyStmtTags {
		rules = append(rules, Rule{Tag: tag, Neighbors: bodyStmtTags})
	}

	return rules
}()}
