package authz

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestCapabilitiesMatchMigrationSeed(t *testing.T) {
	t.Parallel()

	migrationPaths := []string{
		filepath.Join("..", "..", "migrations", "229_resource_authorization_rbac.sql"),
		filepath.Join("..", "..", "migrations", "243_openai_quota_auto_reset_actor.sql"),
	}
	seededSet := make(map[string]struct{})
	for _, migrationPath := range migrationPaths {
		contents, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("read permission migration %s: %v", migrationPath, err)
		}

		seed := string(contents)
		block := regexp.MustCompile(`(?s)INSERT INTO permissions \(code, description\)\s*VALUES\s*(.*?)(?:ON CONFLICT|RETURNING|;)`).FindStringSubmatch(seed)
		if len(block) != 2 {
			t.Fatalf("permission seed start not found in %s", migrationPath)
		}

		matches := regexp.MustCompile(`\(\s*'([^']+)',\s*'[^']*'\s*\)`).FindAllStringSubmatch(block[1], -1)
		for _, match := range matches {
			seededSet[match[1]] = struct{}{}
		}
	}
	seeded := make([]string, 0, len(seededSet))
	for capability := range seededSet {
		seeded = append(seeded, capability)
	}

	defined := make([]string, 0, len(AllCapabilities()))
	for _, capability := range AllCapabilities() {
		defined = append(defined, string(capability))
	}
	sort.Strings(seeded)
	sort.Strings(defined)

	if strings.Join(seeded, "\n") != strings.Join(defined, "\n") {
		t.Fatalf("capability constants differ from migration seed\nseeded: %v\ndefined: %v", seeded, defined)
	}
}

func TestUnknownCapabilityFailsClosed(t *testing.T) {
	t.Parallel()

	if capability, ok := ParseCapability("account.fly"); ok || capability.Valid() {
		t.Fatalf("unknown capability accepted: %q", capability)
	}
}

func TestAllCapabilitiesReturnsCopy(t *testing.T) {
	t.Parallel()

	first := AllCapabilities()
	first[0] = Capability("modified")
	second := AllCapabilities()
	if second[0] == first[0] {
		t.Fatal("AllCapabilities returned mutable shared storage")
	}
}
