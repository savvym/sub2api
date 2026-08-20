package authz

import "testing"

func TestAccessLevelActionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType ResourceType
		level        AccessLevel
		allowed      []Action
		denied       []Action
	}{
		{
			name:         "group viewer",
			resourceType: ResourceTypeGroup,
			level:        AccessLevelViewer,
			allowed:      []Action{ActionGroupView},
			denied:       []Action{ActionGroupUse, ActionGroupEdit, ActionGroupManageAccess, ActionGroupDelete, ActionGroupTransfer},
		},
		{
			name:         "group consumer",
			resourceType: ResourceTypeGroup,
			level:        AccessLevelConsumer,
			allowed:      []Action{ActionGroupView, ActionGroupUse},
			denied:       []Action{ActionGroupEdit, ActionGroupManageAccess, ActionGroupDelete, ActionGroupTransfer},
		},
		{
			name:         "group maintainer",
			resourceType: ResourceTypeGroup,
			level:        AccessLevelMaintainer,
			allowed:      []Action{ActionGroupView, ActionGroupUse, ActionGroupEdit},
			denied:       []Action{ActionGroupManageAccess, ActionGroupDelete, ActionGroupTransfer},
		},
		{
			name:         "group manager",
			resourceType: ResourceTypeGroup,
			level:        AccessLevelManager,
			allowed:      []Action{ActionGroupView, ActionGroupUse, ActionGroupEdit, ActionGroupManageAccess},
			denied:       []Action{ActionGroupDelete, ActionGroupTransfer},
		},
		{
			name:         "account viewer",
			resourceType: ResourceTypeAccount,
			level:        AccessLevelViewer,
			allowed:      []Action{ActionAccountView},
			denied:       []Action{ActionAccountUse, ActionAccountOperate, ActionAccountEdit, ActionAccountManageAccess, ActionAccountDelete, ActionAccountTransfer},
		},
		{
			name:         "account consumer",
			resourceType: ResourceTypeAccount,
			level:        AccessLevelConsumer,
			allowed:      []Action{ActionAccountView, ActionAccountUse},
			denied:       []Action{ActionAccountOperate, ActionAccountEdit, ActionAccountManageAccess, ActionAccountDelete, ActionAccountTransfer},
		},
		{
			name:         "account maintainer",
			resourceType: ResourceTypeAccount,
			level:        AccessLevelMaintainer,
			allowed:      []Action{ActionAccountView, ActionAccountUse, ActionAccountOperate, ActionAccountEdit},
			denied:       []Action{ActionAccountManageAccess, ActionAccountDelete, ActionAccountTransfer},
		},
		{
			name:         "account manager",
			resourceType: ResourceTypeAccount,
			level:        AccessLevelManager,
			allowed:      []Action{ActionAccountView, ActionAccountUse, ActionAccountOperate, ActionAccountEdit, ActionAccountManageAccess},
			denied:       []Action{ActionAccountDelete, ActionAccountTransfer},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			for _, action := range testCase.allowed {
				if !testCase.level.Covers(testCase.resourceType, action) {
					t.Errorf("%s should cover %s", testCase.level, action)
				}
			}
			for _, action := range testCase.denied {
				if testCase.level.Covers(testCase.resourceType, action) {
					t.Errorf("%s must not cover %s", testCase.level, action)
				}
			}
		})
	}
}

func TestAccessLevelBoundariesFailClosed(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "owner", "admin", "editor", "MANAGER"} {
		level, ok := ParseAccessLevel(value)
		if ok || level.Valid() || level.Covers(ResourceTypeGroup, ActionGroupView) {
			t.Fatalf("unknown access level accepted: %q", value)
		}
	}
	if AccessLevelMaintainer.AllowedAsPublic() || AccessLevelManager.AllowedAsPublic() {
		t.Fatal("maintainer or manager accepted as public access")
	}
	if !AccessLevelViewer.AllowedAsPublic() || !AccessLevelConsumer.AllowedAsPublic() {
		t.Fatal("viewer or consumer rejected as public access")
	}
}

func TestHighestAccessLevelRejectsUnknownInput(t *testing.T) {
	t.Parallel()

	level, ok := HighestAccessLevel(AccessLevelViewer, AccessLevelManager, AccessLevelConsumer)
	if !ok || level != AccessLevelManager {
		t.Fatalf("unexpected highest access level: %q, %v", level, ok)
	}
	if _, ok := HighestAccessLevel(AccessLevelViewer, AccessLevel("owner")); ok {
		t.Fatal("highest access level accepted unknown input")
	}
	if _, ok := HighestAccessLevel(); ok {
		t.Fatal("highest access level accepted empty input")
	}
}

func TestAccessLevelActionsReturnsCopy(t *testing.T) {
	t.Parallel()

	first, ok := AccessLevelManager.Actions(ResourceTypeAccount)
	if !ok || len(first) == 0 {
		t.Fatal("expected account manager actions")
	}
	first[0] = Action("modified")
	second, ok := AccessLevelManager.Actions(ResourceTypeAccount)
	if !ok || second[0] == first[0] {
		t.Fatal("Actions returned mutable shared storage")
	}
}
