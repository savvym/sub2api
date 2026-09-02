package oauthflow

import (
	"errors"
	"testing"
)

func TestBindingValidationAndEquality(t *testing.T) {
	platform, err := NewPlatformBinding("user:41")
	if err != nil || !platform.Valid() {
		t.Fatalf("platform binding invalid: %v", err)
	}
	otherActor, err := NewPlatformBinding("service_principal:41")
	if err != nil {
		t.Fatalf("service principal binding invalid: %v", err)
	}
	if platform.Equal(otherActor) {
		t.Fatal("different durable actors must not match")
	}

	owner, err := NewUserBinding("user:41", 41)
	if err != nil || !owner.Valid() || owner.OwnerID() == nil || *owner.OwnerID() != 41 {
		t.Fatalf("user binding invalid: %#v, %v", owner, err)
	}
	if _, err := NewUserBinding("user:41", 42); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("mismatched owner should fail: %v", err)
	}
}

func TestBindingFailsClosedForMissingOrTamperedState(t *testing.T) {
	ownerID := int64(41)
	tests := []Binding{
		{},
		{ActorSubjectKey: "user:41", OwnerKind: OwnerKindPlatform, OwnerUserID: &ownerID},
		{ActorSubjectKey: "service_principal:41", OwnerKind: OwnerKindUser, OwnerUserID: &ownerID},
		{ActorSubjectKey: "user:041", OwnerKind: OwnerKindPlatform},
		{ActorSubjectKey: "system:41", OwnerKind: OwnerKindPlatform},
	}
	for _, binding := range tests {
		if binding.Valid() {
			t.Fatalf("tampered binding accepted: %#v", binding)
		}
	}
}
