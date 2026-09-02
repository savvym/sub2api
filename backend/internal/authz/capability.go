package authz

// Capability is a platform-wide permission. It never implies access to every
// resource of the corresponding type unless its contract says so explicitly.
type Capability string

const (
	CapabilityAPIKeyCreate                        Capability = "api_key.create"
	CapabilityAccountCreate                       Capability = "account.create"
	CapabilityGroupCreate                         Capability = "group.create"
	CapabilityResourceShare                       Capability = "resource.share"
	CapabilityResourceTransfer                    Capability = "resource.transfer"
	CapabilityPlatformResourceViewAll             Capability = "platform.resource.view_all"
	CapabilityPlatformResourceOperateAll          Capability = "platform.resource.operate_all"
	CapabilityPlatformResourceManageAll           Capability = "platform.resource.manage_all"
	CapabilityPlatformRoleManage                  Capability = "platform.role.manage"
	CapabilityPlatformGrantManage                 Capability = "platform.grant.manage"
	CapabilityPlatformSecretExport                Capability = "platform.secret.export"
	CapabilityPlatformAccountOpenAIQuotaAutoReset Capability = "platform.account.openai_quota_auto_reset"
)

var allCapabilities = [...]Capability{
	CapabilityAPIKeyCreate,
	CapabilityAccountCreate,
	CapabilityGroupCreate,
	CapabilityResourceShare,
	CapabilityResourceTransfer,
	CapabilityPlatformResourceViewAll,
	CapabilityPlatformResourceOperateAll,
	CapabilityPlatformResourceManageAll,
	CapabilityPlatformRoleManage,
	CapabilityPlatformGrantManage,
	CapabilityPlatformSecretExport,
	CapabilityPlatformAccountOpenAIQuotaAutoReset,
}

func (c Capability) Valid() bool {
	switch c {
	case CapabilityAPIKeyCreate,
		CapabilityAccountCreate,
		CapabilityGroupCreate,
		CapabilityResourceShare,
		CapabilityResourceTransfer,
		CapabilityPlatformResourceViewAll,
		CapabilityPlatformResourceOperateAll,
		CapabilityPlatformResourceManageAll,
		CapabilityPlatformRoleManage,
		CapabilityPlatformGrantManage,
		CapabilityPlatformSecretExport,
		CapabilityPlatformAccountOpenAIQuotaAutoReset:
		return true
	default:
		return false
	}
}

func ParseCapability(value string) (Capability, bool) {
	capability := Capability(value)
	return capability, capability.Valid()
}

func AllCapabilities() []Capability {
	result := make([]Capability, len(allCapabilities))
	copy(result, allCapabilities[:])
	return result
}
