package authz

func legacyAdminCapabilityAllowed(capability Capability) bool {
	// Keep this exhaustive and immutable. New capabilities must not become
	// available to legacy administrators without a migration review.
	switch capability {
	case CapabilityAPIKeyCreate,
		CapabilityAccountCreate,
		CapabilityGroupCreate,
		CapabilityResourceShare,
		CapabilityResourceTransfer,
		CapabilityPlatformResourceViewAll,
		CapabilityPlatformResourceOperateAll,
		CapabilityPlatformResourceManageAll,
		CapabilityPlatformRoleManage,
		CapabilityPlatformGrantManage:
		return true
	default:
		return false
	}
}

func createCapability(resourceType ResourceType) (Capability, bool) {
	switch resourceType {
	case ResourceTypeAccount:
		return CapabilityAccountCreate, true
	case ResourceTypeGroup:
		return CapabilityGroupCreate, true
	default:
		return "", false
	}
}

// platformCapabilitiesForAction returns the narrowest platform capability
// first so audit provenance remains deterministic when an actor has multiple
// capabilities that cover the same action.
func platformCapabilitiesForAction(action Action) []Capability {
	switch action {
	case ActionAccountView:
		return []Capability{
			CapabilityPlatformResourceViewAll,
			CapabilityPlatformResourceOperateAll,
			CapabilityPlatformResourceManageAll,
		}
	case ActionGroupView:
		return []Capability{
			CapabilityPlatformResourceViewAll,
			CapabilityPlatformResourceManageAll,
		}
	case ActionAccountOperate:
		return []Capability{
			CapabilityPlatformResourceOperateAll,
			CapabilityPlatformResourceManageAll,
		}
	case ActionGroupUse,
		ActionGroupEdit,
		ActionGroupManageAccess,
		ActionGroupDelete,
		ActionGroupTransfer,
		ActionAccountUse,
		ActionAccountEdit,
		ActionAccountManageAccess,
		ActionAccountDelete,
		ActionAccountTransfer:
		return []Capability{CapabilityPlatformResourceManageAll}
	default:
		return nil
	}
}
