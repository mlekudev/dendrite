// Package nostr implements the Nostr protocol — event structure,
// validation, and relay communication.
//
// This is the organism's own Nostr implementation, synthesized from
// ingesting both ORLY (relay) and smesh (client). It speaks the
// protocol from structural understanding, not from copying a library.
package nostr

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Event is a Nostr event as defined by NIP-01.
type Event struct {
	ID        string     `json:"id"`
	Pubkey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
}

// Canonical returns the NIP-01 canonical serialization for ID computation:
// [0,"<pubkey>",<created_at>,<kind>,<tags>,"<content>"]
func (e *Event) Canonical() []byte {
	tags, _ := json.Marshal(e.Tags)
	if e.Tags == nil {
		tags = []byte("[]")
	}
	return []byte(fmt.Sprintf(`[0,"%s",%d,%d,%s,%s]`,
		e.Pubkey, e.CreatedAt, e.Kind, tags, quoteContent(e.Content)))
}

// ComputeID returns the SHA256 hex digest of the canonical serialization.
func (e *Event) ComputeID() string {
	h := sha256.Sum256(e.Canonical())
	return hex.EncodeToString(h[:])
}

// ValidID returns true if the event's ID matches the computed ID.
func (e *Event) ValidID() bool {
	return e.ID == e.ComputeID()
}

// ValidSig returns true if the event's schnorr signature is valid.
func (e *Event) ValidSig() bool {
	// Decode the public key (32-byte x-only).
	pubBytes, err := hex.DecodeString(e.Pubkey)
	if err != nil || len(pubBytes) != 32 {
		return false
	}
	pub, err := schnorr.ParsePubKey(pubBytes)
	if err != nil {
		return false
	}

	// Decode the signature (64 bytes).
	sigBytes, err := hex.DecodeString(e.Sig)
	if err != nil || len(sigBytes) != 64 {
		return false
	}
	sig, err := schnorr.ParseSignature(sigBytes)
	if err != nil {
		return false
	}

	// The message is the event ID bytes (SHA256 hash).
	idBytes, err := hex.DecodeString(e.ID)
	if err != nil || len(idBytes) != 32 {
		return false
	}

	return sig.Verify(idBytes, pub)
}

// Valid returns true if both ID and signature are correct.
func (e *Event) Valid() bool {
	return e.ValidID() && e.ValidSig()
}

// quoteContent produces a JSON-encoded string literal for the content field.
func quoteContent(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Filter is a NIP-01 subscription filter.
type Filter struct {
	IDs     []string `json:"ids,omitempty"`
	Authors []string `json:"authors,omitempty"`
	Kinds   []int    `json:"kinds,omitempty"`
	Since   *int64   `json:"since,omitempty"`
	Until   *int64   `json:"until,omitempty"`
	Limit   *int     `json:"limit,omitempty"`
	// Tag filters: #e, #p, etc.
	Tags map[string][]string `json:"-"`
}

// MarshalJSON handles the tag filter serialization.
func (f Filter) MarshalJSON() ([]byte, error) {
	type plain Filter
	m := make(map[string]any)

	// Marshal the plain fields.
	data, _ := json.Marshal(plain(f))
	json.Unmarshal(data, &m)

	// Add tag filters as #<letter>.
	for k, v := range f.Tags {
		m["#"+k] = v
	}

	return json.Marshal(m)
}

// Sign creates a schnorr signature for an event using the given private key.
// The private key is a 32-byte hex-encoded scalar.
func (e *Event) Sign(privKeyHex string) error {
	privBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return fmt.Errorf("decode private key: %w", err)
	}

	privKey, _ := btcec.PrivKeyFromBytes(privBytes)

	// Set pubkey from private key.
	pub := privKey.PubKey()
	e.Pubkey = hex.EncodeToString(schnorr.SerializePubKey(pub))

	// Compute ID.
	e.ID = e.ComputeID()

	// Sign the ID.
	idBytes, _ := hex.DecodeString(e.ID)
	sig, err := schnorr.Sign(privKey, idBytes)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	e.Sig = hex.EncodeToString(sig.Serialize())

	return nil
}
