package authz

import (
	"reflect"
	"testing"
)

func TestCreateCapabilityMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		resourceType ResourceType
		capability   Capability
		ok           bool
	}{
		{resourceType: ResourceTypeAccount, capability: CapabilityAccountCreate, ok: true},
		{resourceType: ResourceTypeGroup, capability: CapabilityGroupCreate, ok: true},
		{resourceType: ResourceType("workspace")},
	}
	for _, testCase := range tests {
		capability, ok := createCapability(testCase.resourceType)
		if capability != testCase.capability || ok != testCase.ok {
			t.Fatalf("unexpected create mapping for %q: %q, %v", testCase.resourceType, capability, ok)
		}
	}
}

func TestPlatformCapabilitiesCoverOnlyDeclaredResourceActions(t *testing.T) {
	t.Parallel()

	expected := map[Action][]Capability{
		ActionAccountView: {
			CapabilityPlatformResourceViewAll,
			CapabilityPlatformResourceOperateAll,
			CapabilityPlatformResourceManageAll,
		},
		ActionGroupView: {
			CapabilityPlatformResourceViewAll,
			CapabilityPlatformResourceManageAll,
		},
		ActionAccountOperate: {
			CapabilityPlatformResourceOperateAll,
			CapabilityPlatformResourceManageAll,
		},
		ActionGroupUse:            {CapabilityPlatformResourceManageAll},
		ActionGroupEdit:           {CapabilityPlatformResourceManageAll},
		ActionGroupManageAccess:   {CapabilityPlatformResourceManageAll},
		ActionGroupDelete:         {CapabilityPlatformResourceManageAll},
		ActionGroupTransfer:       {CapabilityPlatformResourceManageAll},
		ActionAccountUse:          {CapabilityPlatformResourceManageAll},
		ActionAccountEdit:         {CapabilityPlatformResourceManageAll},
		ActionAccountManageAccess: {CapabilityPlatformResourceManageAll},
		ActionAccountDelete:       {CapabilityPlatformResourceManageAll},
		ActionAccountTransfer:     {CapabilityPlatformResourceManageAll},
	}
	for _, action := range AllActions() {
		if got := platformCapabilitiesForAction(action); !reflect.DeepEqual(got, expected[action]) {
			t.Fatalf("unexpected platform capability mapping for %q: %v", action, got)
		}
	}
	if got := platformCapabilitiesForAction(Action("account.export_secret")); got != nil {
		t.Fatalf("unknown action received platform capabilities: %v", got)
	}
}

func TestLegacyAdminCapabilityAllowlistMatchesCompatibilitySeed(t *testing.T) {
	t.Parallel()

	// capability_test.go independently verifies AllCapabilities against the
	// migration seed. This table makes the legacy subset an explicit review
	// boundary so future seeded capabilities are denied by default.
	expected := map[Capability]bool{
		CapabilityAPIKeyCreate:                        true,
		CapabilityAccountCreate:                       true,
		CapabilityGroupCreate:                         true,
		CapabilityResourceShare:                       true,
		CapabilityResourceTransfer:                    true,
		CapabilityPlatformResourceViewAll:             true,
		CapabilityPlatformResourceOperateAll:          true,
		CapabilityPlatformResourceManageAll:           true,
		CapabilityPlatformRoleManage:                  true,
		CapabilityPlatformGrantManage:                 true,
		CapabilityPlatformSecretExport:                false,
		CapabilityPlatformAccountOpenAIQuotaAutoReset: false,
	}
	if len(expected) != len(AllCapabilities()) {
		t.Fatalf("legacy compatibility table does not cover every seeded capability: %d of %d", len(expected), len(AllCapabilities()))
	}
	for _, capability := range AllCapabilities() {
		if got := legacyAdminCapabilityAllowed(capability); got != expected[capability] {
			t.Fatalf("unexpected legacy compatibility for %q: %v", capability, got)
		}
	}
	if legacyAdminCapabilityAllowed(Capability("future.capability")) {
		t.Fatal("unknown capability entered legacy admin allowlist")
	}
}
