package nostr

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// Identity is a Nostr keypair — the organism's external face.
// Distinct from the birthmark (internal fingerprint), the identity
// is what other Nostr clients and relays see.
type Identity struct {
	PrivKey *btcec.PrivateKey
	PubKey  string // 32-byte hex x-only pubkey
}

// NewIdentity generates a fresh Nostr identity from crypto/rand entropy.
func NewIdentity() (*Identity, error) {
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("entropy: %w", err)
	}

	privKey, _ := btcec.PrivKeyFromBytes(seed[:])
	pubKey := schnorr.SerializePubKey(privKey.PubKey())

	return &Identity{
		PrivKey: privKey,
		PubKey:  hex.EncodeToString(pubKey),
	}, nil
}

// PrivKeyHex returns the private key as a hex string for signing.
func (id *Identity) PrivKeyHex() string {
	return hex.EncodeToString(id.PrivKey.Serialize())
}

// Metadata holds NIP-01 kind 0 metadata fields.
type Metadata struct {
	Name    string `json:"name"`
	About   string `json:"about"`
	Picture string `json:"picture,omitempty"`
}

// ComposeMetadata creates a signed kind 0 event describing this instance.
func (id *Identity) ComposeMetadata(instanceID uint32, generation int, sporeHash string) (*Event, error) {
	meta := Metadata{
		Name:  fmt.Sprintf("dendrite-inst%d", instanceID),
		About: fmt.Sprintf("dendrite lattice organism, generation %d, spore %s", generation, truncHash(sporeHash)),
	}

	content, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}

	ev := &Event{
		CreatedAt: time.Now().Unix(),
		Kind:      0,
		Tags:      [][]string{},
		Content:   string(content),
	}

	if err := ev.Sign(id.PrivKeyHex()); err != nil {
		return nil, fmt.Errorf("sign metadata: %w", err)
	}

	return ev, nil
}

// ComposeStatus creates a signed kind 1 event describing current lattice state.
func (id *Identity) ComposeStatus(instanceID uint32, generation int, totalNodes int, occupied int, occupancy float64, fitness float64, peerCount int) (*Event, error) {
	content := fmt.Sprintf(
		"dendrite inst%d gen%d: %d nodes, %d occupied (%.0f%%), fitness=%.3f, %d peers",
		instanceID, generation, totalNodes, occupied, occupancy*100, fitness, peerCount,
	)

	ev := &Event{
		CreatedAt: time.Now().Unix(),
		Kind:      1,
		Tags: [][]string{
			{"t", "dendrite"},
			{"t", "lattice"},
			{"t", fmt.Sprintf("gen%d", generation)},
		},
		Content: content,
	}

	if err := ev.Sign(id.PrivKeyHex()); err != nil {
		return nil, fmt.Errorf("sign status: %w", err)
	}

	return ev, nil
}

// truncHash returns the first 16 chars of a hash string.
func truncHash(h string) string {
	if len(h) > 16 {
		return h[:16]
	}
	return h
}
