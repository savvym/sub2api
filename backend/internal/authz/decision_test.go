package authz

import "testing"

func TestAllowDecisionPreservesTypedProvenance(t *testing.T) {
	t.Parallel()

	owner := allow(ownerMatch())
	if !owner.Valid() || !owner.Allowed() || owner.MatchSource() != MatchSourceOwner {
		t.Fatal("valid owner decision rejected")
	}
	if _, ok := owner.AccessLevel(); ok {
		t.Fatal("owner bypass masqueraded as access level")
	}

	capabilityMatch, err := platformCapabilityMatch(CapabilityPlatformResourceManageAll)
	if err != nil {
		t.Fatalf("build capability provenance: %v", err)
	}
	capabilityDecision := allow(capabilityMatch)
	provenance, ok := capabilityDecision.Provenance()
	if !ok {
		t.Fatal("capability provenance missing")
	}
	if capability, ok := provenance.Capability(); !ok || capability != CapabilityPlatformResourceManageAll {
		t.Fatalf("unexpected matched capability: %q, %v", capability, ok)
	}

	roleMatch, err := roleGrantMatch(101, 7, AccessLevelMaintainer)
	if err != nil {
		t.Fatalf("build role grant provenance: %v", err)
	}
	grant := allow(roleMatch)
	if !grant.Valid() || !grant.Allowed() || grant.MatchSource() != MatchSourceRoleGrant {
		t.Fatal("valid role grant decision rejected")
	}
	if level, ok := grant.AccessLevel(); !ok || level != AccessLevelMaintainer {
		t.Fatalf("unexpected grant access level: %q, %v", level, ok)
	}
	provenance, _ = grant.Provenance()
	if grantID, ok := provenance.GrantID(); !ok || grantID != 101 {
		t.Fatalf("unexpected grant id: %d, %v", grantID, ok)
	}
	if roleID, ok := provenance.GranteeRoleID(); !ok || roleID != 7 {
		t.Fatalf("unexpected grantee role id: %d, %v", roleID, ok)
	}

	legacyAdmin := allow(legacyAdminMatch())
	if !legacyAdmin.Allowed() || legacyAdmin.MatchSource() != MatchSourceLegacyAdmin {
		t.Fatal("legacy admin source was not preserved")
	}
}

func TestInvalidMatchProvenanceFailsClosed(t *testing.T) {
	t.Parallel()

	invalidMatches := []MatchProvenance{
		{},
		{source: MatchSourceOwner, accessLevel: AccessLevelManager},
		{source: MatchSourcePlatformCapability, capability: Capability("unknown")},
		{source: MatchSourcePublicAccess, accessLevel: AccessLevelManager},
		{source: MatchSourceUserGrant, grantID: 0, accessLevel: AccessLevelViewer},
		{source: MatchSourceRoleGrant, grantID: 1, granteeRoleID: 0, accessLevel: AccessLevelViewer},
		{source: MatchSource("request_field")},
	}
	for _, match := range invalidMatches {
		decision := allow(match)
		if decision.Allowed() || decision.DenyReason() != DenyReasonInvalidDecision {
			t.Fatalf("invalid match metadata did not fail closed: %+v", match)
		}
	}

	if _, err := publicAccessMatch(AccessLevelMaintainer); err == nil {
		t.Fatal("maintainer accepted as public provenance")
	}
	if _, err := userGrantMatch(-1, AccessLevelViewer); err == nil {
		t.Fatal("invalid user grant id accepted")
	}
	if _, err := roleGrantMatch(1, 0, AccessLevelViewer); err == nil {
		t.Fatal("invalid role id accepted")
	}
	if _, err := platformCapabilityMatch(Capability("unknown")); err == nil {
		t.Fatal("unknown capability accepted as provenance")
	}
}

func TestDenyDecisionPreservesReasonAndTransportClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason   DenyReason
		class    DenialClass
		conceals bool
	}{
		{reason: DenyReasonNoMatchingAccess, class: DenialClassNotFound, conceals: true},
		{reason: DenyReasonInsufficientAccess, class: DenialClassForbidden},
		{reason: DenyReasonSessionInvalid, class: DenialClassUnauthenticated},
		{reason: DenyReasonAuthorizationDataUnavailable, class: DenialClassUnavailable},
		{reason: DenyReasonUnknownAction, class: DenialClassInvalid},
	}
	for _, testCase := range tests {
		decision := deny(testCase.reason)
		if !decision.Valid() || decision.Allowed() || decision.DenyReason() != testCase.reason {
			t.Fatalf("unexpected denial for %q: %+v", testCase.reason, decision)
		}
		class, ok := decision.DenialClass()
		if !ok || class != testCase.class {
			t.Fatalf("unexpected denial class for %q: %q, %v", testCase.reason, class, ok)
		}
		if decision.ConcealsResource() != testCase.conceals {
			t.Fatalf("unexpected concealment for %q", testCase.reason)
		}
	}

	unknown := deny(DenyReason("new_reason"))
	if unknown.Allowed() || unknown.DenyReason() != DenyReasonInvalidDecision {
		t.Fatal("unknown denial reason did not fail closed")
	}
	if class, ok := unknown.DenialClass(); !ok || class != DenialClassInvalid {
		t.Fatalf("unexpected unknown denial class: %q, %v", class, ok)
	}

	zero := Decision{}
	if zero.Valid() || zero.Allowed() || zero.DenyReason() != DenyReasonInvalidDecision {
		t.Fatal("zero decision did not fail closed")
	}

	garbage := Decision{
		provenance: MatchProvenance{source: MatchSourceOwner, accessLevel: AccessLevelManager},
		denyReason: DenyReasonNoMatchingAccess,
	}
	if garbage.Valid() || garbage.Allowed() || garbage.DenyReason() != DenyReasonInvalidDecision {
		t.Fatal("denial with non-zero provenance was accepted")
	}
}
