package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type directResourceSettingRepo struct {
	SettingRepository
	value         string
	getValueCalls int
	setCalls      int
}

type directResourceActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s directResourceActorStore) LoadSubjectSnapshot(context.Context, authz.SubjectRef) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s directResourceActorStore) LoadServicePrincipalSubjectSnapshotByCode(context.Context, string) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (r *directResourceSettingRepo) GetValue(context.Context, string) (string, error) {
	r.getValueCalls++
	if r.value == "" {
		return "", ErrSettingNotFound
	}
	return r.value, nil
}

func (r *directResourceSettingRepo) Set(_ context.Context, _, value string) error {
	r.setCalls++
	r.value = value
	return nil
}

func (r *directResourceSettingRepo) totalCalls() int {
	return r.getValueCalls + r.setCalls
}

type directResourceMonitorRepo struct {
	ChannelMonitorRepository
	monitor      *ChannelMonitor
	listCalls    int
	getCalls     int
	createCalls  int
	updateCalls  int
	deleteCalls  int
	recoverCalls int
}

type directResourceTemplateRepo struct {
	ChannelMonitorRequestTemplateRepository
	template            *ChannelMonitorRequestTemplate
	listCalls           int
	getCalls            int
	createCalls         int
	updateCalls         int
	deleteCalls         int
	applyCalls          int
	countCalls          int
	listAssociatedCalls int
}

func (r *directResourceTemplateRepo) List(context.Context, ChannelMonitorRequestTemplateListParams) ([]*ChannelMonitorRequestTemplate, error) {
	r.listCalls++
	return []*ChannelMonitorRequestTemplate{}, nil
}

func (r *directResourceTemplateRepo) GetByID(context.Context, int64) (*ChannelMonitorRequestTemplate, error) {
	r.getCalls++
	if r.template == nil {
		return nil, ErrChannelMonitorTemplateNotFound
	}
	clone := *r.template
	return &clone, nil
}

func (r *directResourceTemplateRepo) Create(_ context.Context, template *ChannelMonitorRequestTemplate) error {
	r.createCalls++
	template.ID = 19
	clone := *template
	r.template = &clone
	return nil
}

func (r *directResourceTemplateRepo) Update(_ context.Context, template *ChannelMonitorRequestTemplate) error {
	r.updateCalls++
	clone := *template
	r.template = &clone
	return nil
}

func (r *directResourceTemplateRepo) Delete(context.Context, int64) error {
	r.deleteCalls++
	return nil
}

func (r *directResourceTemplateRepo) ApplyToMonitors(context.Context, int64, []int64) (int64, error) {
	r.applyCalls++
	return 1, nil
}

func (r *directResourceTemplateRepo) CountAssociatedMonitors(context.Context, int64) (int64, error) {
	r.countCalls++
	return 1, nil
}

func (r *directResourceTemplateRepo) ListAssociatedMonitors(context.Context, int64) ([]*AssociatedMonitorBrief, error) {
	r.listAssociatedCalls++
	return []*AssociatedMonitorBrief{}, nil
}

func (r *directResourceTemplateRepo) totalCalls() int {
	return r.listCalls + r.getCalls + r.createCalls + r.updateCalls + r.deleteCalls +
		r.applyCalls + r.countCalls + r.listAssociatedCalls
}

func (r *directResourceMonitorRepo) List(context.Context, ChannelMonitorListParams) ([]*ChannelMonitor, int64, error) {
	r.listCalls++
	if r.monitor == nil {
		return []*ChannelMonitor{}, 0, nil
	}
	clone := *r.monitor
	return []*ChannelMonitor{&clone}, 1, nil
}

func (r *directResourceMonitorRepo) GetByID(context.Context, int64) (*ChannelMonitor, error) {
	r.getCalls++
	if r.monitor == nil {
		return nil, ErrChannelMonitorNotFound
	}
	clone := *r.monitor
	return &clone, nil
}

func (r *directResourceMonitorRepo) Create(_ context.Context, monitor *ChannelMonitor) error {
	r.createCalls++
	monitor.ID = int64(100 + r.createCalls)
	return nil
}

func (r *directResourceMonitorRepo) Update(_ context.Context, monitor *ChannelMonitor) error {
	r.updateCalls++
	clone := *monitor
	r.monitor = &clone
	return nil
}

func (r *directResourceMonitorRepo) Delete(context.Context, int64) error {
	r.deleteCalls++
	return nil
}

func (r *directResourceMonitorRepo) FindByDuplicateOperationID(context.Context, string) (*ChannelMonitor, error) {
	r.recoverCalls++
	return nil, nil
}

func (r *directResourceMonitorRepo) totalCalls() int {
	return r.listCalls + r.getCalls + r.createCalls + r.updateCalls + r.deleteCalls + r.recoverCalls
}

type directResourceEncryptor struct{}

func (directResourceEncryptor) Encrypt(plaintext string) (string, error) {
	return "ENC:" + plaintext, nil
}

func (directResourceEncryptor) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "ENC:"), nil
}

type directResourceOpsRepo struct {
	OpsRepository
	listCalls          int
	createRuleCalls    int
	updateRuleCalls    int
	deleteRuleCalls    int
	createSilenceCalls int
}

func (r *directResourceOpsRepo) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	r.listCalls++
	return []*OpsAlertRule{}, nil
}

func (r *directResourceOpsRepo) CreateAlertRule(_ context.Context, rule *OpsAlertRule) (*OpsAlertRule, error) {
	r.createRuleCalls++
	return rule, nil
}

func (r *directResourceOpsRepo) UpdateAlertRule(_ context.Context, rule *OpsAlertRule) (*OpsAlertRule, error) {
	r.updateRuleCalls++
	return rule, nil
}

func (r *directResourceOpsRepo) DeleteAlertRule(context.Context, int64) error {
	r.deleteRuleCalls++
	return nil
}

func (r *directResourceOpsRepo) CreateAlertSilence(_ context.Context, silence *OpsAlertSilence) (*OpsAlertSilence, error) {
	r.createSilenceCalls++
	return silence, nil
}

func (r *directResourceOpsRepo) totalCalls() int {
	return r.listCalls + r.createRuleCalls + r.updateRuleCalls + r.deleteRuleCalls + r.createSilenceCalls
}

func TestDirectResourceAdminFacadesRejectMissingActorBeforeDependencies(t *testing.T) {
	ctx := context.Background()
	actor := authz.Actor{}

	settings := &directResourceSettingRepo{}
	content := NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
	_, err := content.AdminGetContentModerationConfig(ctx, actor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = content.AdminUpdateContentModerationConfig(ctx, actor, UpdateContentModerationConfigInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = content.AdminTestContentModerationAPIKeys(ctx, actor, TestContentModerationAPIKeysInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, settings.totalCalls())

	templateRepo := &directResourceTemplateRepo{}
	templates := NewChannelMonitorRequestTemplateService(templateRepo)
	_, err = templates.AdminListChannelMonitorRequestTemplates(ctx, actor, ChannelMonitorRequestTemplateListParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = templates.AdminGetChannelMonitorRequestTemplate(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = templates.AdminCreateChannelMonitorRequestTemplate(ctx, actor, ChannelMonitorRequestTemplateCreateParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = templates.AdminUpdateChannelMonitorRequestTemplate(ctx, actor, 1, ChannelMonitorRequestTemplateUpdateParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.ErrorIs(t, templates.AdminDeleteChannelMonitorRequestTemplate(ctx, actor, 1), ErrAdminResourceActorUnavailable)
	_, err = templates.AdminCountAssociatedChannelMonitors(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = templates.AdminListAssociatedChannelMonitors(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = templates.AdminApplyChannelMonitorRequestTemplate(ctx, actor, 1, []int64{2})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, templateRepo.totalCalls())

	monitors := &directResourceMonitorRepo{}
	monitorService := NewChannelMonitorService(monitors, directResourceEncryptor{})
	_, _, err = monitorService.AdminListChannelMonitors(ctx, actor, ChannelMonitorListParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = monitorService.AdminGetChannelMonitor(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = monitorService.AdminCreateChannelMonitor(ctx, actor, ChannelMonitorCreateParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = monitorService.AdminUpdateChannelMonitor(ctx, actor, 1, ChannelMonitorUpdateParams{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.ErrorIs(t, monitorService.AdminDeleteChannelMonitor(ctx, actor, 1), ErrAdminResourceActorUnavailable)
	_, err = monitorService.AdminDuplicateChannelMonitor(ctx, actor, 1, 2, "", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = monitorService.AdminRecoverDuplicateChannelMonitor(ctx, actor, 1, "", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, monitors.totalCalls())

	opsRepo := &directResourceOpsRepo{}
	ops := &OpsService{opsRepo: opsRepo}
	_, err = ops.AdminListAlertRules(ctx, actor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = ops.AdminCreateAlertRule(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = ops.AdminUpdateAlertRule(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.ErrorIs(t, ops.AdminDeleteAlertRule(ctx, actor, 0), ErrAdminResourceActorUnavailable)
	_, err = ops.AdminCreateAlertSilence(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, opsRepo.totalCalls())
}

func TestDirectResourceAdminFacadesAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: directResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: directResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()

			settings := &directResourceSettingRepo{}
			content := NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
			allGroups := false
			groupIDs := []int64{9, 3, 9}
			proxyID := int64(17)
			view, err := content.AdminUpdateContentModerationConfig(ctx, testCase.actor, UpdateContentModerationConfigInput{
				AllGroups: &allGroups,
				GroupIDs:  &groupIDs,
				ProxyID:   &proxyID,
			})
			require.NoError(t, err)
			require.Equal(t, []int64{3, 9}, view.GroupIDs)
			require.NotNil(t, view.ProxyID)
			require.Equal(t, proxyID, *view.ProxyID)
			view, err = content.AdminGetContentModerationConfig(ctx, testCase.actor)
			require.NoError(t, err)
			require.Equal(t, []int64{3, 9}, view.GroupIDs)
			_, err = content.AdminTestContentModerationAPIKeys(ctx, testCase.actor, TestContentModerationAPIKeysInput{})
			require.NoError(t, err)

			templateRepo := &directResourceTemplateRepo{template: directResourceTemplateFixture()}
			templates := NewChannelMonitorRequestTemplateService(templateRepo)
			_, err = templates.AdminListChannelMonitorRequestTemplates(ctx, testCase.actor, ChannelMonitorRequestTemplateListParams{})
			require.NoError(t, err)
			_, err = templates.AdminGetChannelMonitorRequestTemplate(ctx, testCase.actor, 7)
			require.NoError(t, err)
			_, err = templates.AdminCreateChannelMonitorRequestTemplate(ctx, testCase.actor, directResourceTemplateCreateParams())
			require.NoError(t, err)
			_, err = templates.AdminUpdateChannelMonitorRequestTemplate(ctx, testCase.actor, 19, ChannelMonitorRequestTemplateUpdateParams{})
			require.NoError(t, err)
			require.NoError(t, templates.AdminDeleteChannelMonitorRequestTemplate(ctx, testCase.actor, 19))
			_, err = templates.AdminCountAssociatedChannelMonitors(ctx, testCase.actor, 19)
			require.NoError(t, err)
			_, err = templates.AdminListAssociatedChannelMonitors(ctx, testCase.actor, 19)
			require.NoError(t, err)
			_, err = templates.AdminApplyChannelMonitorRequestTemplate(ctx, testCase.actor, 19, []int64{5})
			require.NoError(t, err)

			monitorRepo := &directResourceMonitorRepo{monitor: directResourceMonitorFixture()}
			monitorService := NewChannelMonitorService(monitorRepo, directResourceEncryptor{})
			_, _, err = monitorService.AdminListChannelMonitors(ctx, testCase.actor, ChannelMonitorListParams{})
			require.NoError(t, err)
			_, err = monitorService.AdminGetChannelMonitor(ctx, testCase.actor, 8)
			require.NoError(t, err)
			_, err = monitorService.AdminCreateChannelMonitor(ctx, testCase.actor, directResourceMonitorCreateParams())
			require.NoError(t, err)
			_, err = monitorService.AdminUpdateChannelMonitor(ctx, testCase.actor, 8, ChannelMonitorUpdateParams{})
			require.NoError(t, err)
			require.NoError(t, monitorService.AdminDeleteChannelMonitor(ctx, testCase.actor, 8))
			actorScope, ok := testCase.actor.SubjectKey()
			require.True(t, ok)
			_, err = monitorService.AdminDuplicateChannelMonitor(ctx, testCase.actor, 8, 41, actorScope, "")
			require.NoError(t, err)
			_, err = monitorService.AdminRecoverDuplicateChannelMonitor(ctx, testCase.actor, 8, actorScope, "")
			require.NoError(t, err)

			opsRepo := &directResourceOpsRepo{}
			ops := &OpsService{opsRepo: opsRepo}
			_, err = ops.AdminListAlertRules(ctx, testCase.actor)
			require.NoError(t, err)
			rule := &OpsAlertRule{ID: 1, Filters: map[string]any{"group_id": float64(9)}}
			_, err = ops.AdminCreateAlertRule(ctx, testCase.actor, rule)
			require.NoError(t, err)
			_, err = ops.AdminUpdateAlertRule(ctx, testCase.actor, rule)
			require.NoError(t, err)
			require.NoError(t, ops.AdminDeleteAlertRule(ctx, testCase.actor, rule.ID))
			_, err = ops.AdminCreateAlertSilence(ctx, testCase.actor, &OpsAlertSilence{
				RuleID:   1,
				Platform: "openai",
				GroupID:  &groupIDs[0],
				Until:    time.Now().Add(time.Hour),
			})
			require.NoError(t, err)
			require.Equal(t, 5, opsRepo.totalCalls())
		})
	}
}

func directResourceTestActor(t testing.TB, kind authz.SubjectKind, id int64) authz.Actor {
	t.Helper()
	subject, err := authz.NewSubjectRef(kind, id)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       1,
		CurrentLegacyAdmin: kind == authz.SubjectKindUser,
		Configuration:      configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(directResourceActorStore{snapshot: snapshot})
	if kind == authz.SubjectKindServicePrincipal {
		actor, resolveErr := resolver.ResolveServicePrincipal(
			context.Background(),
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
		require.NoError(t, resolveErr)
		return actor
	}
	actor, resolveErr := resolver.ResolveUser(context.Background(), id, authz.AuthMethodJWT)
	require.NoError(t, resolveErr)
	return actor
}

func directResourceMonitorFixture() *ChannelMonitor {
	return &ChannelMonitor{
		ID:               8,
		Name:             "existing",
		Provider:         MonitorProviderOpenAI,
		APIMode:          MonitorAPIModeChatCompletions,
		Endpoint:         "https://api.openai.com",
		APIKey:           "ENC:secret",
		PrimaryModel:     "gpt-4o-mini",
		Enabled:          true,
		IntervalSeconds:  60,
		CheckMode:        MonitorCheckModeProbe,
		BodyOverrideMode: MonitorBodyOverrideModeOff,
	}
}

func directResourceMonitorCreateParams() ChannelMonitorCreateParams {
	return ChannelMonitorCreateParams{
		Name:             "created",
		Provider:         MonitorProviderOpenAI,
		APIMode:          MonitorAPIModeChatCompletions,
		Endpoint:         "https://api.openai.com",
		APIKey:           "secret",
		PrimaryModel:     "gpt-4o-mini",
		Enabled:          true,
		IntervalSeconds:  60,
		CheckMode:        MonitorCheckModeProbe,
		BodyOverrideMode: MonitorBodyOverrideModeOff,
	}
}

func directResourceTemplateFixture() *ChannelMonitorRequestTemplate {
	return &ChannelMonitorRequestTemplate{
		ID:               7,
		Name:             "existing",
		Provider:         MonitorProviderOpenAI,
		APIMode:          MonitorAPIModeChatCompletions,
		BodyOverrideMode: MonitorBodyOverrideModeOff,
	}
}

func directResourceTemplateCreateParams() ChannelMonitorRequestTemplateCreateParams {
	return ChannelMonitorRequestTemplateCreateParams{
		Name:             "created",
		Provider:         MonitorProviderOpenAI,
		APIMode:          MonitorAPIModeChatCompletions,
		BodyOverrideMode: MonitorBodyOverrideModeOff,
	}
}

func TestDirectResourceAdminFacadesValidateActorInFirstStatement(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	filename := filepath.Join(filepath.Dir(currentFile), "admin_direct_resource_config.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	require.NoError(t, err)

	expected := map[string]bool{
		"AdminGetContentModerationConfig":          false,
		"AdminUpdateContentModerationConfig":       false,
		"AdminTestContentModerationAPIKeys":        false,
		"AdminListChannelMonitorRequestTemplates":  false,
		"AdminGetChannelMonitorRequestTemplate":    false,
		"AdminCreateChannelMonitorRequestTemplate": false,
		"AdminUpdateChannelMonitorRequestTemplate": false,
		"AdminDeleteChannelMonitorRequestTemplate": false,
		"AdminCountAssociatedChannelMonitors":      false,
		"AdminListAssociatedChannelMonitors":       false,
		"AdminApplyChannelMonitorRequestTemplate":  false,
		"AdminListChannelMonitors":                 false,
		"AdminGetChannelMonitor":                   false,
		"AdminCreateChannelMonitor":                false,
		"AdminUpdateChannelMonitor":                false,
		"AdminDeleteChannelMonitor":                false,
		"AdminDuplicateChannelMonitor":             false,
		"AdminRecoverDuplicateChannelMonitor":      false,
		"AdminListAlertRules":                      false,
		"AdminCreateAlertRule":                     false,
		"AdminUpdateAlertRule":                     false,
		"AdminDeleteAlertRule":                     false,
		"AdminCreateAlertSilence":                  false,
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		if _, required := expected[function.Name.Name]; !required {
			continue
		}
		require.NotEmpty(t, function.Body.List, function.Name.Name)
		require.Truef(t, directResourceStatementCalls(function.Body.List[0], "ValidateAdminResourceActor"),
			"%s must validate the actor in its first statement", function.Name.Name)
		expected[function.Name.Name] = true
	}
	for name, checked := range expected {
		require.Truef(t, checked, "%s was not checked", name)
	}
}

func directResourceStatementCalls(statement ast.Stmt, functionName string) bool {
	found := false
	ast.Inspect(statement, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == functionName {
			found = true
			return false
		}
		return true
	})
	return found
}
