// Package oauthflow defines server-owned identity and ownership state for OAuth flows.
package oauthflow

import (
	"errors"
	"strconv"
	"strings"
)

var ErrInvalidBinding = errors.New("oauth flow binding is invalid")

type OwnerKind string

const (
	OwnerKindPlatform OwnerKind = "platform"
	OwnerKindUser     OwnerKind = "user"
)

// Binding is persisted with an OAuth session. It is never populated from an
// HTTP callback and therefore prevents a callback from changing the initiating
// actor or the eventual account owner.
type Binding struct {
	ActorSubjectKey string    `json:"actor_subject_key"`
	OwnerKind       OwnerKind `json:"owner_kind"`
	OwnerUserID     *int64    `json:"owner_user_id,omitempty"`
}

func NewPlatformBinding(actorSubjectKey string) (Binding, error) {
	binding := Binding{
		ActorSubjectKey: strings.TrimSpace(actorSubjectKey),
		OwnerKind:       OwnerKindPlatform,
	}
	if !binding.Valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

func NewUserBinding(actorSubjectKey string, ownerUserID int64) (Binding, error) {
	ownerID := ownerUserID
	binding := Binding{
		ActorSubjectKey: strings.TrimSpace(actorSubjectKey),
		OwnerKind:       OwnerKindUser,
		OwnerUserID:     &ownerID,
	}
	if !binding.Valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

func (b Binding) Valid() bool {
	kind, subjectID, ok := parseSubjectKey(b.ActorSubjectKey)
	if !ok {
		return false
	}
	switch b.OwnerKind {
	case OwnerKindPlatform:
		return b.OwnerUserID == nil
	case OwnerKindUser:
		return kind == "user" && b.OwnerUserID != nil && *b.OwnerUserID > 0 && subjectID == *b.OwnerUserID
	default:
		return false
	}
}

func (b Binding) Equal(other Binding) bool {
	if !b.Valid() || !other.Valid() || b.ActorSubjectKey != other.ActorSubjectKey || b.OwnerKind != other.OwnerKind {
		return false
	}
	if b.OwnerUserID == nil || other.OwnerUserID == nil {
		return b.OwnerUserID == nil && other.OwnerUserID == nil
	}
	return *b.OwnerUserID == *other.OwnerUserID
}

func (b Binding) OwnerID() *int64 {
	if !b.Valid() || b.OwnerUserID == nil {
		return nil
	}
	ownerID := *b.OwnerUserID
	return &ownerID
}

func parseSubjectKey(raw string) (string, int64, bool) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 2 || (parts[0] != "user" && parts[0] != "service_principal") {
		return "", 0, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[1] {
		return "", 0, false
	}
	return parts[0], id, true
}
