package enzyme

import (
	"bufio"
	"io"
	"regexp"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
)

// JSSource is an enzyme that decomposes JavaScript/TypeScript/Svelte source
// into typed AST-like elements using pattern recognition.
//
// It does not do full parsing — it recognizes structural markers: imports,
// exports, classes, types, interfaces, functions, methods, fields, and
// string literals. This gives the lattice the vocabulary of a JS/TS codebase
// without requiring a Node.js runtime.
type JSSource struct{}

// CanDigest returns true if the sample looks like JS/TS/Svelte source.
func (JSSource) CanDigest(sample []byte) bool {
	s := string(sample)
	return strings.Contains(s, "import ") ||
		strings.Contains(s, "export ") ||
		strings.Contains(s, "function ") ||
		strings.Contains(s, "<script")
}

var (
	// Import patterns.
	reImportFrom = regexp.MustCompile(`import\s+(?:\{[^}]*\}|[^{;]+)\s+from\s+['"]([^'"]+)['"]`)
	reImportBare = regexp.MustCompile(`import\s+['"]([^'"]+)['"]`)

	// Export/declaration patterns.
	reExportClass     = regexp.MustCompile(`(?:export\s+)?class\s+(\w+)`)
	reExportType      = regexp.MustCompile(`(?:export\s+)?type\s+(\w+)\s*[=<{]`)
	reExportInterface = regexp.MustCompile(`(?:export\s+)?interface\s+(\w+)`)
	reExportFunction  = regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+(\w+)`)
	reExportConst     = regexp.MustCompile(`(?:export\s+)?(?:const|let|var)\s+(\w+)`)
	reEnum            = regexp.MustCompile(`(?:export\s+)?enum\s+(\w+)`)

	// Class members.
	reMethod   = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static|async|readonly)\s+)*(\w+)\s*\(`)
	reGetter   = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static)\s+)*get\s+(\w+)\s*\(`)
	reSetter   = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static)\s+)*set\s+(\w+)\s*\(`)
	reField    = regexp.MustCompile(`^\s+(?:(?:public|private|protected|static|readonly)\s+)*(\w+)\s*[?!]?\s*:\s*`)
	reArrowFn  = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s+)?\(`)
	reArrowFn2 = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s+)?\w+\s*=>`)

	// String literals — single and double quoted.
	reSingleString = regexp.MustCompile(`'((?:[^'\\]|\\.){3,})'`)
	reDoubleString = regexp.MustCompile(`"((?:[^"\\]|\\.){3,})"`)
	reTemplateStr  = regexp.MustCompile("(`[^`]{3,}`)")

	// Svelte component tags.
	reSvelteComponent = regexp.MustCompile(`<([A-Z]\w+)`)

	// JSDoc/comment tags.
	reComment = regexp.MustCompile(`^\s*(?://|/\*|\*)`)
)

// Digest scans JS/TS/Svelte source and emits typed elements.
func (JSSource) Digest(r io.Reader) <-chan axiom.Element {
	ch := make(chan axiom.Element, 128)

	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		inClass := false
		braceDepth := 0
		classDepth := 0

		for scanner.Scan() {
			line := scanner.Text()

			// Track brace depth for class scope detection.
			for _, c := range line {
				if c == '{' {
					braceDepth++
				} else if c == '}' {
					braceDepth--
					if inClass && braceDepth < classDepth {
						inClass = false
					}
				}
			}

			// Skip pure comments — emit as comment elements.
			if reComment.MatchString(line) {
				trimmed := strings.TrimSpace(line)
				trimmed = strings.TrimLeft(trimmed, "/* ")
				if len(trimmed) > 3 {
					ch <- newHexElement("comment", trimmed)
				}
				continue
			}

			// Imports.
			if m := reImportFrom.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("import", m[1])
				// Also extract imported identifiers.
				if idx := strings.Index(line, "{"); idx >= 0 {
					if end := strings.Index(line[idx:], "}"); end >= 0 {
						names := line[idx+1 : idx+end]
						for _, name := range strings.Split(names, ",") {
							name = strings.TrimSpace(name)
							if as := strings.Index(name, " as "); as >= 0 {
								name = strings.TrimSpace(name[as+4:])
							}
							if name != "" {
								ch <- newHexElement("ident", name)
							}
						}
					}
				}
				continue
			}
			if m := reImportBare.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("import", m[1])
				continue
			}

			// Class declarations.
			if m := reExportClass.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("type", m[1])
				ch <- newHexElement("struct", "")
				inClass = true
				classDepth = braceDepth
				continue
			}

			// Type aliases.
			if m := reExportType.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("type", m[1])
				continue
			}

			// Interfaces.
			if m := reExportInterface.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("type", m[1])
				ch <- newHexElement("interface", "")
				continue
			}

			// Enums.
			if m := reEnum.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("type", m[1])
				continue
			}

			// Functions (top-level or exported).
			if m := reExportFunction.FindStringSubmatch(line); m != nil {
				ch <- newHexElement("func", m[1])
				continue
			}

			// Arrow functions assigned to const/let/var.
			if m := reArrowFn.FindStringSubmatch(line); m != nil {
				if !inClass {
					ch <- newHexElement("func", m[1])
				}
				continue
			}
			if m := reArrowFn2.FindStringSubmatch(line); m != nil {
				if !inClass {
					ch <- newHexElement("func", m[1])
				}
				continue
			}

			// Exported constants (that aren't arrow functions).
			if m := reExportConst.FindStringSubmatch(line); m != nil {
				if !inClass {
					ch <- newHexElement("ident", m[1])
				}
				continue
			}

			// Class members.
			if inClass {
				// Getters.
				if m := reGetter.FindStringSubmatch(line); m != nil {
					ch <- newHexElement("method", m[1])
					continue
				}
				// Setters.
				if m := reSetter.FindStringSubmatch(line); m != nil {
					ch <- newHexElement("method", m[1])
					continue
				}
				// Methods.
				if m := reMethod.FindStringSubmatch(line); m != nil {
					name := m[1]
					// Skip keywords that look like methods.
					if name != "if" && name != "for" && name != "while" &&
						name != "switch" && name != "return" && name != "constructor" &&
						name != "new" && name != "throw" && name != "catch" {
						ch <- newHexElement("method", name)
					}
					continue
				}
				// Fields.
				if m := reField.FindStringSubmatch(line); m != nil {
					name := m[1]
					if name != "return" && name != "const" && name != "let" {
						ch <- newHexElement("field", name)
					}
					continue
				}
			}

			// Svelte component references.
			if matches := reSvelteComponent.FindAllStringSubmatch(line, -1); matches != nil {
				for _, m := range matches {
					ch <- newHexElement("ident", m[1])
				}
			}

			// String literals.
			for _, re := range []*regexp.Regexp{reSingleString, reDoubleString, reTemplateStr} {
				if matches := re.FindAllStringSubmatch(line, -1); matches != nil {
					for _, m := range matches {
						ch <- newHexElement("literal", m[0])
					}
				}
			}
		}
	}()

	return ch
}
