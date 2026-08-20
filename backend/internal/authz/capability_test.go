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

	migrationPath := filepath.Join("..", "..", "migrations", "229_resource_authorization_rbac.sql")
	contents, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read permission migration: %v", err)
	}

	seed := string(contents)
	start := strings.Index(seed, "INSERT INTO permissions (code, description)")
	if start < 0 {
		t.Fatal("permission seed start not found")
	}
	seed = seed[start:]
	end := strings.Index(seed, "ON CONFLICT (code)")
	if end < 0 {
		t.Fatal("permission seed end not found")
	}
	seed = seed[:end]

	matches := regexp.MustCompile(`\('([^']+)',\s*'[^']*'\)`).FindAllStringSubmatch(seed, -1)
	seeded := make([]string, 0, len(matches))
	for _, match := range matches {
		seeded = append(seeded, match[1])
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
