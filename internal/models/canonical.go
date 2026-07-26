package models

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrEmptyCanonicalKeyField is returned when CanonicalKey (or a
// constructor deriving one) is given an empty provider_id or
// provider_model_id. A canonical key is never derived from a partially
// empty identity — fail closed rather than hash an empty field.
var ErrEmptyCanonicalKeyField = errors.New("models: canonical key requires non-empty provider_id and provider_model_id")

// ErrQualityRatingOutOfRange is returned when a quality rating outside the
// documented 0-100 scale (04 §3) is supplied to NewCanonicalModel.
var ErrQualityRatingOutOfRange = errors.New("models: quality_rating must be within 0-100")

// CanonicalKey derives the models.canonical_key_sha256 value for a
// provider-scoped canonical model identity: the lowercase hex SHA-256 of an
// injective, length-prefixed encoding of (providerID, providerModelID) —
// mirroring internal/secrets.RecordIdentity's AAD derivation. Each field is
// preceded by its own 4-byte big-endian length, so distinct pairs can never
// collide (e.g. ("a","bc") and ("ab","c") hash differently). The key is
// provider-scoped by construction: the same providerModelID under two
// providers always yields two different keys. No normalization (case
// folding, trimming, family/name matching) is applied — that would create
// cross-provider equivalence, which v1 does not support. Empty inputs are
// rejected with ErrEmptyCanonicalKeyField rather than hashed.
func CanonicalKey(providerID, providerModelID string) (string, error) {
	if providerID == "" || providerModelID == "" {
		return "", ErrEmptyCanonicalKeyField
	}

	fields := [...]string{providerID, providerModelID}

	size := 0
	for _, f := range fields {
		size += 4 + len(f)
	}

	buf := make([]byte, 0, size)
	var lenPrefix [4]byte
	for _, f := range fields {
		binary.BigEndian.PutUint32(lenPrefix[:], uint32(len(f)))
		buf = append(buf, lenPrefix[:]...)
		buf = append(buf, f...)
	}

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalModel is the provider-scoped canonical identity + native facts
// for a model version (04 §3, 02 §3 "Canonical model"). NativeContextTokens
// and QualityRating are nil when unknown — a pointer distinguishes unknown
// from a real zero value, which the models.native_context_tokens /
// quality_rating columns require (both nullable = unknown). NativeModalities
// is nil when unknown, a (possibly empty) non-nil slice when known.
type CanonicalModel struct {
	ID                  string
	CanonicalKey        string
	DisplayName         string
	NativeContextTokens *int
	NativeModalities    []string
	QualityRating       *float64
}

// NewCanonicalModel constructs a CanonicalModel, deriving CanonicalKey from
// providerID/providerModelID itself — it never trusts a caller-supplied
// canonical key. It fails closed on an empty id/providerID/providerModelID
// and on a qualityRating outside the documented 0-100 scale (04 §3).
func NewCanonicalModel(
	id, providerID, providerModelID, displayName string,
	nativeContextTokens *int,
	nativeModalities []string,
	qualityRating *float64,
) (CanonicalModel, error) {
	if id == "" {
		return CanonicalModel{}, fmt.Errorf("models: NewCanonicalModel: empty id")
	}

	key, err := CanonicalKey(providerID, providerModelID)
	if err != nil {
		return CanonicalModel{}, err
	}

	if qualityRating != nil && (*qualityRating < 0 || *qualityRating > 100) {
		return CanonicalModel{}, fmt.Errorf("%w: got %v", ErrQualityRatingOutOfRange, *qualityRating)
	}

	return CanonicalModel{
		ID:                  id,
		CanonicalKey:        key,
		DisplayName:         displayName,
		NativeContextTokens: nativeContextTokens,
		NativeModalities:    nativeModalities,
		QualityRating:       qualityRating,
	}, nil
}
