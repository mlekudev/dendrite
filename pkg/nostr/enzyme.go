package nostr

import (
	"fmt"
	"strings"

	"github.com/mlekudev/dendrite/pkg/axiom"
)

// element is the concrete axiom.Element type for Nostr events.
type element struct {
	tag string
	val any
}

func (e element) Type() string { return e.tag }
func (e element) Value() any   { return e.val }

// EventToElements decomposes a validated Nostr event into typed lattice
// elements. Each event produces elements for its structural parts:
// pubkey, kind, content words, tags, and references.
func EventToElements(ev *Event) []axiom.Element {
	var elems []axiom.Element

	// The event itself.
	elems = append(elems, element{"event", ev.ID[:16]})

	// Author identity.
	elems = append(elems, element{"pubkey", ev.Pubkey})

	// Kind as a typed element.
	elems = append(elems, element{"kind", fmt.Sprintf("%d", ev.Kind)})

	// Content — decompose into words for bonding.
	if ev.Content != "" {
		words := strings.Fields(ev.Content)
		for _, w := range words {
			// Skip very short words.
			if len(w) > 2 {
				elems = append(elems, element{"word", w})
			}
		}
		// Also emit the content as a literal for format-string absorption.
		if len(ev.Content) > 10 && len(ev.Content) < 200 {
			elems = append(elems, element{"literal", fmt.Sprintf("%q", ev.Content)})
		}
	}

	// Tags — each tag becomes a typed element.
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "e":
			// Event reference.
			elems = append(elems, element{"e-tag", tag[1]})
		case "p":
			// Pubkey mention.
			elems = append(elems, element{"p-tag", tag[1]})
		case "t":
			// Hashtag.
			elems = append(elems, element{"hashtag", tag[1]})
		default:
			// Generic tag.
			elems = append(elems, element{"tag", tag[0] + ":" + tag[1]})
		}
	}

	// Timestamp as a structural element.
	elems = append(elems, element{"timestamp", fmt.Sprintf("%d", ev.CreatedAt)})

	return elems
}
