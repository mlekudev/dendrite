package enzyme

import (
	"strings"
	"testing"
)

func TestJSSourceDigestClass(t *testing.T) {
	src := `
import { Event, kinds } from 'nostr-tools'
import { Pubkey } from '../shared'

export type NoteType = 'root' | 'reply' | 'quote'

export class Note {
  private readonly _event: Event
  private readonly _mentions: NoteMention[]

  get id(): EventId {
    return EventId.fromHex(this._event.id)
  }

  get author(): Pubkey {
    return Pubkey.fromHex(this._event.pubkey)
  }

  mentionsUser(pubkey: Pubkey): boolean {
    return this._mentions.some((m) => m.pubkey.equals(pubkey))
  }

  static fromEvent(event: Event): Note {
    return new Note(event, [], [], [])
  }
}

export function createNote(event: Event): Note {
  return Note.fromEvent(event)
}
`
	js := JSSource{}
	if !js.CanDigest([]byte(src)) {
		t.Fatal("expected CanDigest to return true")
	}

	elements := js.Digest(strings.NewReader(src))

	counts := make(map[string]int)
	names := make(map[string][]string)
	for e := range elements {
		counts[e.Type()]++
		if v, ok := e.Value().(string); ok && v != "" {
			names[e.Type()] = append(names[e.Type()], v)
		}
	}

	// Should find imports.
	if counts["import"] < 2 {
		t.Errorf("expected >=2 imports, got %d", counts["import"])
	}

	// Should find type declarations (NoteType + Note class).
	if counts["type"] < 2 {
		t.Errorf("expected >=2 types, got %d: %v", counts["type"], names["type"])
	}

	// Should find struct (class).
	if counts["struct"] < 1 {
		t.Errorf("expected >=1 struct (class), got %d", counts["struct"])
	}

	// Should find methods (id, author, mentionsUser, fromEvent).
	if counts["method"] < 3 {
		t.Errorf("expected >=3 methods, got %d: %v", counts["method"], names["method"])
	}

	// Should find fields (_event, _mentions).
	if counts["field"] < 2 {
		t.Errorf("expected >=2 fields, got %d: %v", counts["field"], names["field"])
	}

	// Should find the createNote function.
	if counts["func"] < 1 {
		t.Errorf("expected >=1 func, got %d: %v", counts["func"], names["func"])
	}
}

func TestJSSourceDigestSvelte(t *testing.T) {
	src := `<script lang="ts">
import { onMount } from 'svelte'
import NoteCard from './NoteCard.svelte'

let notes = []

function handleClick() {
  console.log("clicked")
}
</script>

<div class="feed">
  {#each notes as note}
    <NoteCard {note} on:click={handleClick} />
  {/each}
</div>
`
	js := JSSource{}
	if !js.CanDigest([]byte(src)) {
		t.Fatal("expected CanDigest to return true for svelte")
	}

	elements := js.Digest(strings.NewReader(src))
	counts := make(map[string]int)
	for e := range elements {
		counts[e.Type()]++
	}

	if counts["import"] < 2 {
		t.Errorf("expected >=2 imports, got %d", counts["import"])
	}
	if counts["func"] < 1 {
		t.Errorf("expected >=1 func, got %d", counts["func"])
	}
}
