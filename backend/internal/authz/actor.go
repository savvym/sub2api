package authz

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidActor = errors.New("authz: invalid actor")

type SubjectKind string

const (
	SubjectKindUser             SubjectKind = "user"
	SubjectKindServicePrincipal SubjectKind = "service_principal"
	SubjectKindSystem           SubjectKind = "system"
)

func (k SubjectKind) Valid() bool {
	return k == SubjectKindUser || k == SubjectKindServicePrincipal || k == SubjectKindSystem
}

type AuthMethod string

const (
	AuthMethodJWT              AuthMethod = "jwt"
	AuthMethodAdminAPIKey      AuthMethod = "admin_api_key"
	AuthMethodServicePrincipal AuthMethod = "service_principal"
	AuthMethodSystem           AuthMethod = "system"
)

func (m AuthMethod) validFor(kind SubjectKind) bool {
	switch kind {
	case SubjectKindUser:
		return m == AuthMethodJWT
	case SubjectKindServicePrincipal:
		return m == AuthMethodAdminAPIKey || m == AuthMethodServicePrincipal
	case SubjectKindSystem:
		return m == AuthMethodSystem
	default:
		return false
	}
}

type userActorOptions struct {
	subjectAuthzVersion int64
	roleVersions        map[int64]int64
	capabilities        []Capability
	legacyAdmin         bool
	authMethod          AuthMethod
}

type servicePrincipalActorOptions struct {
	subjectAuthzVersion int64
	roleVersions        map[int64]int64
	capabilities        []Capability
	authMethod          AuthMethod
}

type systemActorMarker struct{}

var trustedSystemActorMarker = &systemActorMarker{}

// Actor has no exported state so HTTP JSON binding cannot construct or
// upgrade a trusted subject. The package-local ActorResolver will be the only
// production constructor after runtime authorization is wired.
type Actor struct {
	kind                   SubjectKind
	userID                 int64
	servicePrincipalID     int64
	systemCode             string
	roleVersions           map[int64]int64
	capabilities           map[Capability]struct{}
	subjectAuthzVersion    int64
	legacyAdmin            bool
	authMethod             AuthMethod
	trustedSystemActorMark *systemActorMarker
}

// newUserActor is intentionally package-private. The ActorResolver introduced
// with runtime authorization is the only production path that may call it.
func newUserActor(userID int64, options userActorOptions) (Actor, error) {
	if userID <= 0 || options.subjectAuthzVersion <= 0 || !options.authMethod.validFor(SubjectKindUser) {
		return Actor{}, ErrInvalidActor
	}
	roles, capabilities, err := validateAndCopyAuthorizationState(options.roleVersions, options.capabilities)
	if err != nil {
		return Actor{}, err
	}
	return Actor{
		kind:                SubjectKindUser,
		userID:              userID,
		roleVersions:        roles,
		capabilities:        capabilities,
		subjectAuthzVersion: options.subjectAuthzVersion,
		legacyAdmin:         options.legacyAdmin,
		authMethod:          options.authMethod,
	}, nil
}

// newServicePrincipalActor has the same trust boundary as newUserActor.
func newServicePrincipalActor(servicePrincipalID int64, options servicePrincipalActorOptions) (Actor, error) {
	if servicePrincipalID <= 0 || options.subjectAuthzVersion <= 0 || !options.authMethod.validFor(SubjectKindServicePrincipal) {
		return Actor{}, ErrInvalidActor
	}
	roles, capabilities, err := validateAndCopyAuthorizationState(options.roleVersions, options.capabilities)
	if err != nil {
		return Actor{}, err
	}
	return Actor{
		kind:                SubjectKindServicePrincipal,
		servicePrincipalID:  servicePrincipalID,
		roleVersions:        roles,
		capabilities:        capabilities,
		subjectAuthzVersion: options.subjectAuthzVersion,
		authMethod:          options.authMethod,
	}, nil
}

// newSystemActor intentionally remains package-private until a trusted worker
// adapter is wired. This prevents HTTP-facing packages from minting system
// actors during the dark-launch phase.
func newSystemActor(code string) (Actor, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return Actor{}, ErrInvalidActor
	}
	return Actor{
		kind:                   SubjectKindSystem,
		systemCode:             code,
		roleVersions:           map[int64]int64{},
		capabilities:           map[Capability]struct{}{},
		authMethod:             AuthMethodSystem,
		trustedSystemActorMark: trustedSystemActorMarker,
	}, nil
}

func validateAndCopyAuthorizationState(roleVersions map[int64]int64, capabilities []Capability) (map[int64]int64, map[Capability]struct{}, error) {
	roles := make(map[int64]int64, len(roleVersions))
	for roleID, version := range roleVersions {
		if roleID <= 0 || version <= 0 {
			return nil, nil, ErrInvalidActor
		}
		roles[roleID] = version
	}

	capabilitySet := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !capability.Valid() {
			return nil, nil, ErrInvalidActor
		}
		capabilitySet[capability] = struct{}{}
	}
	return roles, capabilitySet, nil
}

func (a Actor) Valid() bool {
	if !a.kind.Valid() || !a.authMethod.validFor(a.kind) {
		return false
	}
	for roleID, version := range a.roleVersions {
		if roleID <= 0 || version <= 0 {
			return false
		}
	}
	for capability := range a.capabilities {
		if !capability.Valid() {
			return false
		}
	}

	switch a.kind {
	case SubjectKindUser:
		return a.userID > 0 && a.servicePrincipalID == 0 && a.systemCode == "" &&
			a.subjectAuthzVersion > 0 && a.trustedSystemActorMark == nil
	case SubjectKindServicePrincipal:
		return a.userID == 0 && a.servicePrincipalID > 0 && a.systemCode == "" &&
			a.subjectAuthzVersion > 0 && !a.legacyAdmin && a.trustedSystemActorMark == nil
	case SubjectKindSystem:
		return a.userID == 0 && a.servicePrincipalID == 0 && a.systemCode != "" &&
			a.subjectAuthzVersion == 0 && !a.legacyAdmin && a.trustedSystemActorMark == trustedSystemActorMarker
	default:
		return false
	}
}

func (a Actor) Kind() SubjectKind {
	return a.kind
}

func (a Actor) UserID() (int64, bool) {
	return a.userID, a.Valid() && a.kind == SubjectKindUser
}

func (a Actor) ServicePrincipalID() (int64, bool) {
	return a.servicePrincipalID, a.Valid() && a.kind == SubjectKindServicePrincipal
}

func (a Actor) SystemCode() (string, bool) {
	return a.systemCode, a.Valid() && a.kind == SubjectKindSystem
}

func (a Actor) subjectVersion() int64 {
	if !a.Valid() {
		return 0
	}
	return a.subjectAuthzVersion
}

func (a Actor) hasLegacyAdminBypass() bool {
	return a.Valid() && a.kind == SubjectKindUser && a.legacyAdmin
}

func (a Actor) AuthMethod() AuthMethod {
	if !a.Valid() {
		return ""
	}
	return a.authMethod
}

func (a Actor) roleIDsSnapshot() []int64 {
	if !a.Valid() {
		return nil
	}
	roleIDs := make([]int64, 0, len(a.roleVersions))
	for roleID := range a.roleVersions {
		roleIDs = append(roleIDs, roleID)
	}
	sort.Slice(roleIDs, func(i, j int) bool { return roleIDs[i] < roleIDs[j] })
	return roleIDs
}

func (a Actor) roleVersionsSnapshot() map[int64]int64 {
	if !a.Valid() {
		return nil
	}
	result := make(map[int64]int64, len(a.roleVersions))
	for roleID, version := range a.roleVersions {
		result[roleID] = version
	}
	return result
}

func (a Actor) capabilitiesSnapshot() []Capability {
	if !a.Valid() {
		return nil
	}
	result := make([]Capability, 0, len(a.capabilities))
	for capability := range a.capabilities {
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// hasCapabilitySnapshot is deliberately package-private. Policy code must
// revalidate versions for sensitive writes instead of authorizing directly
// from a potentially stale request snapshot.
func (a Actor) hasCapabilitySnapshot(capability Capability) bool {
	if !a.Valid() || !capability.Valid() {
		return false
	}
	_, ok := a.capabilities[capability]
	return ok
}

// DurableSubject returns the persisted subject required by append-only
// authorization events. A process-local system actor is intentionally not a
// durable writer; workers that mutate authorization state must use a Service
// Principal actor.
func (a Actor) DurableSubject() (SubjectKind, int64, bool) {
	if !a.Valid() {
		return "", 0, false
	}
	switch a.kind {
	case SubjectKindUser:
		return a.kind, a.userID, true
	case SubjectKindServicePrincipal:
		return a.kind, a.servicePrincipalID, true
	default:
		return "", 0, false
	}
}

// SubjectKey returns the canonical persisted identity key used to partition
// actor-owned state such as idempotency records. User and Service Principal
// IDs intentionally live in separate namespaces.
func (a Actor) SubjectKey() (string, bool) {
	kind, id, ok := a.DurableSubject()
	if !ok {
		return "", false
	}
	return string(kind) + ":" + strconv.FormatInt(id, 10), true
}
