package authz

import "errors"

var ErrInvalidResourceRef = errors.New("authz: invalid resource reference")

type ResourceType string

const (
	ResourceTypeAccount ResourceType = "account"
	ResourceTypeGroup   ResourceType = "group"
)

func (r ResourceType) Valid() bool {
	return r == ResourceTypeAccount || r == ResourceTypeGroup
}

func ParseResourceType(value string) (ResourceType, bool) {
	resourceType := ResourceType(value)
	return resourceType, resourceType.Valid()
}

type ResourceRef struct {
	resourceType ResourceType
	id           int64
}

func NewResourceRef(resourceType ResourceType, id int64) (ResourceRef, error) {
	if !resourceType.Valid() || id <= 0 {
		return ResourceRef{}, ErrInvalidResourceRef
	}
	return ResourceRef{resourceType: resourceType, id: id}, nil
}

func (r ResourceRef) Valid() bool {
	return r.resourceType.Valid() && r.id > 0
}

func (r ResourceRef) Type() ResourceType {
	return r.resourceType
}

func (r ResourceRef) ID() int64 {
	return r.id
}

type Action string

const (
	ActionGroupView         Action = "group.view"
	ActionGroupUse          Action = "group.use"
	ActionGroupEdit         Action = "group.edit"
	ActionGroupManageAccess Action = "group.manage_access"
	ActionGroupDelete       Action = "group.delete"
	ActionGroupTransfer     Action = "group.transfer"

	ActionAccountView         Action = "account.view"
	ActionAccountUse          Action = "account.use"
	ActionAccountOperate      Action = "account.operate"
	ActionAccountEdit         Action = "account.edit"
	ActionAccountManageAccess Action = "account.manage_access"
	ActionAccountDelete       Action = "account.delete"
	ActionAccountTransfer     Action = "account.transfer"
)

var allActions = [...]Action{
	ActionGroupView,
	ActionGroupUse,
	ActionGroupEdit,
	ActionGroupManageAccess,
	ActionGroupDelete,
	ActionGroupTransfer,
	ActionAccountView,
	ActionAccountUse,
	ActionAccountOperate,
	ActionAccountEdit,
	ActionAccountManageAccess,
	ActionAccountDelete,
	ActionAccountTransfer,
}

func (a Action) ResourceType() (ResourceType, bool) {
	switch a {
	case ActionGroupView,
		ActionGroupUse,
		ActionGroupEdit,
		ActionGroupManageAccess,
		ActionGroupDelete,
		ActionGroupTransfer:
		return ResourceTypeGroup, true
	case ActionAccountView,
		ActionAccountUse,
		ActionAccountOperate,
		ActionAccountEdit,
		ActionAccountManageAccess,
		ActionAccountDelete,
		ActionAccountTransfer:
		return ResourceTypeAccount, true
	default:
		return "", false
	}
}

func (a Action) Valid() bool {
	_, ok := a.ResourceType()
	return ok
}

func (a Action) ValidFor(resourceType ResourceType) bool {
	actionResourceType, ok := a.ResourceType()
	return ok && resourceType.Valid() && actionResourceType == resourceType
}

func ParseAction(value string) (Action, bool) {
	action := Action(value)
	return action, action.Valid()
}

func AllActions() []Action {
	result := make([]Action, len(allActions))
	copy(result, allActions[:])
	return result
}
