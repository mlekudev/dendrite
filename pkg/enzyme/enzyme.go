// Package enzyme implements extracellular digestion — decomposing raw input
// into typed elements that can bond into the lattice.
//
// Each enzyme is substrate-specific. Cellulase for cellulose, protease for
// protein. The type of each emitted element is determined by the enzyme's
// own type system.
package enzyme

import (
	"bufio"
	"io"
	"strings"
	"unicode"

	"github.com/mlekudev/dendrite/pkg/axiom"
)

// Enzyme decomposes raw substrate into typed elements.
type Enzyme interface {
	// CanDigest reports whether this enzyme can process the given sample.
	CanDigest(sample []byte) bool

	// Digest reads from the substrate and emits typed elements.
	// The channel is closed when the substrate is exhausted.
	Digest(r io.Reader) <-chan axiom.Element
}

// element is the concrete Element type emitted by enzymes.
type element struct {
	tag string
	val any
}

func (e element) Type() string { return e.tag }
func (e element) Value() any   { return e.val }

// Elem creates an element with the given type tag and value.
// If the value is a string, the element carries hexagram-encoded tokens.
func Elem(tag string, val any) axiom.Element {
	if s, ok := val.(string); ok {
		return newHexElement(tag, s)
	}
	return element{tag, val}
}

// hexElement carries hexagram-encoded value alongside the raw value.
// It satisfies both axiom.Element and axiom.HexagramElement.
type hexElement struct {
	tag     string
	val     any
	tokens  []uint8
	origLen int
}

func (e hexElement) Type() string       { return e.tag }
func (e hexElement) Value() any         { return e.val }
func (e hexElement) HexTokens() []uint8 { return e.tokens }
func (e hexElement) OrigLen() int       { return e.origLen }

// HexElem creates an element with hexagram-encoded value.
// The raw string is preserved as Value() for backward compatibility;
// the hexagram tokens are available via the HexagramElement interface.
func HexElem(tag string, val string) axiom.Element {
	return newHexElement(tag, val)
}

// newHexElement is the package-internal constructor for hexagram-encoded elements.
func newHexElement(tag string, val string) hexElement {
	data := []byte(val)
	tokens := encodeStringToHex(data)
	return hexElement{tag, val, tokens, len(data)}
}

// encodeStringToHex converts raw bytes to 6-bit hexagram tokens.
// Every 3 bytes produce 4 tokens (24 bits = 4 × 6 bits).
func encodeStringToHex(data []byte) []uint8 {
	if len(data) == 0 {
		return nil
	}
	groups := (len(data) + 2) / 3
	out := make([]uint8, groups*4)
	for i := 0; i < len(data); i += 3 {
		var block [3]byte
		copy(block[:], data[i:min(i+3, len(data))])
		bits := uint32(block[0])<<16 | uint32(block[1])<<8 | uint32(block[2])
		j := (i / 3) * 4
		out[j+0] = uint8((bits >> 18) & 0x3F)
		out[j+1] = uint8((bits >> 12) & 0x3F)
		out[j+2] = uint8((bits >> 6) & 0x3F)
		out[j+3] = uint8(bits & 0x3F)
	}
	return out
}

// Text is an enzyme that decomposes UTF-8 text into typed tokens.
// Words, punctuation, and whitespace are emitted as separate elements.
type Text struct{}

// CanDigest returns true for any input — text is the universal substrate.
func (Text) CanDigest([]byte) bool { return true }

// Digest breaks the input into word, punct, and space elements.
func (Text) Digest(r io.Reader) <-chan axiom.Element {
	ch := make(chan axiom.Element, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Split(scanTokens)
		for scanner.Scan() {
			tok := scanner.Text()
			tag := classifyToken(tok)
			ch <- hexElement{tag, tok, encodeStringToHex([]byte(tok)), len(tok)}
		}
	}()
	return ch
}

// classifyToken determines the type tag for a text token.
// Words are sub-classified by length bucket to create richer constraint
// envelopes. AI text tends toward uniform medium-length words; human text
// has more variation across length classes.
func classifyToken(tok string) string {
	if len(tok) == 0 {
		return "empty"
	}
	// Check first rune.
	r := []rune(tok)[0]
	switch {
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return wordLengthTag(tok)
	case unicode.IsSpace(r):
		return "space"
	default:
		return "punct"
	}
}

// wordLengthTag classifies a word token by its length bucket.
// The buckets are chosen to capture stylometric variation:
//
//	w1: 1 char      (articles, pronouns: a, I)
//	w2: 2-3 chars   (common words: the, is, an, to, of)
//	w3: 4-5 chars   (core vocab: from, with, that, about)
//	w4: 6-8 chars   (content words: between, another, writing)
//	w5: 9+ chars    (formal/technical: restructured, acknowledging)
func wordLengthTag(tok string) string {
	n := len([]rune(tok))
	switch {
	case n <= 1:
		return "w1"
	case n <= 3:
		return "w2"
	case n <= 5:
		return "w3"
	case n <= 8:
		return "w4"
	default:
		return "w5"
	}
}

// scanTokens is a bufio.SplitFunc that splits into words, whitespace runs,
// and individual punctuation characters.
func scanTokens(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if len(data) == 0 {
		return 0, nil, nil
	}

	r := rune(data[0])

	// Whitespace run.
	if unicode.IsSpace(r) {
		i := 0
		for i < len(data) && unicode.IsSpace(rune(data[i])) {
			i++
		}
		return i, data[:i], nil
	}

	// Word (letters and digits).
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		i := 0
		for i < len(data) {
			r := rune(data[i])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				break
			}
			i++
		}
		if i == 0 && !atEOF {
			return 0, nil, nil // need more data
		}
		return i, data[:i], nil
	}

	// Single punctuation character.
	return 1, data[:1], nil
}

// RefElement wraps an element to mark it as reference material.
// Reference elements influence lattice topology during growth but
// are excluded from emitted output. Use IsRef to check.
type RefElement struct {
	Inner axiom.Element
}

func (r RefElement) Type() string { return r.Inner.Type() }
func (r RefElement) Value() any   { return r.Inner.Value() }

// HexTokens forwards to the inner element if it supports hexagram encoding.
func (r RefElement) HexTokens() []uint8 {
	if h, ok := r.Inner.(interface{ HexTokens() []uint8 }); ok {
		return h.HexTokens()
	}
	return nil
}

// IsRef reports whether an element is reference material.
func IsRef(e axiom.Element) bool {
	_, ok := e.(RefElement)
	return ok
}

// Lines is an enzyme that emits each line as a single element.
type Lines struct {
	Tag string // type tag for emitted elements; defaults to "line"
}

func (e Lines) CanDigest([]byte) bool { return true }

func (e Lines) Digest(r io.Reader) <-chan axiom.Element {
	tag := e.Tag
	if tag == "" {
		tag = "line"
	}
	ch := make(chan axiom.Element, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r\n")
			ch <- hexElement{tag, line, encodeStringToHex([]byte(line)), len(line)}
		}
	}()
	return ch
}
