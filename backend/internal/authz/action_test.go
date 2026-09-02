package authz

import "testing"

func TestActionsHaveStableResourceType(t *testing.T) {
	t.Parallel()

	seen := make(map[Action]struct{}, len(AllActions()))
	for _, action := range AllActions() {
		if !action.Valid() {
			t.Fatalf("invalid declared action %q", action)
		}
		if _, exists := seen[action]; exists {
			t.Fatalf("duplicate declared action %q", action)
		}
		seen[action] = struct{}{}
		resourceType, ok := action.ResourceType()
		if !ok || !action.ValidFor(resourceType) {
			t.Fatalf("action %q has no valid resource type", action)
		}
	}
	if len(seen) != 13 {
		t.Fatalf("unexpected action count: got %d want 13", len(seen))
	}
}

func TestUnknownAndMismatchedActionsFailClosed(t *testing.T) {
	t.Parallel()

	if action, ok := ParseAction("group.operate"); ok || action.Valid() {
		t.Fatalf("unknown action accepted: %q", action)
	}
	if ActionAccountEdit.ValidFor(ResourceTypeGroup) {
		t.Fatal("account action accepted for group")
	}
	if ActionGroupEdit.ValidFor(ResourceType("workspace")) {
		t.Fatal("action accepted for unknown resource type")
	}
}

func TestResourceRefRejectsUntrustedValues(t *testing.T) {
	t.Parallel()

	if _, err := NewResourceRef(ResourceTypeAccount, 1); err != nil {
		t.Fatalf("valid resource ref rejected: %v", err)
	}
	for _, testCase := range []struct {
		resourceType ResourceType
		id           int64
	}{
		{resourceType: ResourceType("workspace"), id: 1},
		{resourceType: ResourceTypeGroup, id: 0},
		{resourceType: ResourceTypeAccount, id: -1},
	} {
		if _, err := NewResourceRef(testCase.resourceType, testCase.id); err == nil {
			t.Fatalf("invalid resource ref accepted: %+v", testCase)
		}
	}
}
