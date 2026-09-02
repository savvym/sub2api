package authz

type AccessLevel string

const (
	AccessLevelViewer     AccessLevel = "viewer"
	AccessLevelConsumer   AccessLevel = "consumer"
	AccessLevelMaintainer AccessLevel = "maintainer"
	AccessLevelManager    AccessLevel = "manager"
)

var allAccessLevels = [...]AccessLevel{
	AccessLevelViewer,
	AccessLevelConsumer,
	AccessLevelMaintainer,
	AccessLevelManager,
}

var accessLevelActions = map[ResourceType]map[AccessLevel][]Action{
	ResourceTypeGroup: {
		AccessLevelViewer: {
			ActionGroupView,
		},
		AccessLevelConsumer: {
			ActionGroupView,
			ActionGroupUse,
		},
		AccessLevelMaintainer: {
			ActionGroupView,
			ActionGroupUse,
			ActionGroupEdit,
		},
		AccessLevelManager: {
			ActionGroupView,
			ActionGroupUse,
			ActionGroupEdit,
			ActionGroupManageAccess,
		},
	},
	ResourceTypeAccount: {
		AccessLevelViewer: {
			ActionAccountView,
		},
		AccessLevelConsumer: {
			ActionAccountView,
			ActionAccountUse,
		},
		AccessLevelMaintainer: {
			ActionAccountView,
			ActionAccountUse,
			ActionAccountOperate,
			ActionAccountEdit,
		},
		AccessLevelManager: {
			ActionAccountView,
			ActionAccountUse,
			ActionAccountOperate,
			ActionAccountEdit,
			ActionAccountManageAccess,
		},
	},
}

func (l AccessLevel) Valid() bool {
	switch l {
	case AccessLevelViewer, AccessLevelConsumer, AccessLevelMaintainer, AccessLevelManager:
		return true
	default:
		return false
	}
}

func (l AccessLevel) Rank() (int, bool) {
	switch l {
	case AccessLevelViewer:
		return 1, true
	case AccessLevelConsumer:
		return 2, true
	case AccessLevelMaintainer:
		return 3, true
	case AccessLevelManager:
		return 4, true
	default:
		return 0, false
	}
}

func (l AccessLevel) AllowedAsPublic() bool {
	return l == AccessLevelViewer || l == AccessLevelConsumer
}

func (l AccessLevel) Covers(resourceType ResourceType, action Action) bool {
	if !l.Valid() || !action.ValidFor(resourceType) {
		return false
	}
	for _, allowedAction := range accessLevelActions[resourceType][l] {
		if allowedAction == action {
			return true
		}
	}
	return false
}

func (l AccessLevel) Actions(resourceType ResourceType) ([]Action, bool) {
	if !l.Valid() || !resourceType.Valid() {
		return nil, false
	}
	actions := accessLevelActions[resourceType][l]
	result := make([]Action, len(actions))
	copy(result, actions)
	return result, true
}

func ParseAccessLevel(value string) (AccessLevel, bool) {
	level := AccessLevel(value)
	return level, level.Valid()
}

func AllAccessLevels() []AccessLevel {
	result := make([]AccessLevel, len(allAccessLevels))
	copy(result, allAccessLevels[:])
	return result
}

func HighestAccessLevel(levels ...AccessLevel) (AccessLevel, bool) {
	var highest AccessLevel
	highestRank := 0
	for _, level := range levels {
		rank, ok := level.Rank()
		if !ok {
			return "", false
		}
		if rank > highestRank {
			highest = level
			highestRank = rank
		}
	}
	return highest, highestRank > 0
}
