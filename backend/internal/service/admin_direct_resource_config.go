package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *ContentModerationService) AdminGetContentModerationConfig(
	ctx context.Context,
	actor authz.Actor,
) (*ContentModerationConfigView, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetConfig(ctx)
}

func (s *ContentModerationService) AdminUpdateContentModerationConfig(
	ctx context.Context,
	actor authz.Actor,
	input UpdateContentModerationConfigInput,
) (*ContentModerationConfigView, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.UpdateConfig(ctx, input)
}

func (s *ContentModerationService) AdminTestContentModerationAPIKeys(
	ctx context.Context,
	actor authz.Actor,
	input TestContentModerationAPIKeysInput,
) (*TestContentModerationAPIKeysResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.TestAPIKeys(ctx, input)
}

func (s *ChannelMonitorRequestTemplateService) AdminListChannelMonitorRequestTemplates(
	ctx context.Context,
	actor authz.Actor,
	params ChannelMonitorRequestTemplateListParams,
) ([]*ChannelMonitorRequestTemplate, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.List(ctx, params)
}

func (s *ChannelMonitorRequestTemplateService) AdminGetChannelMonitorRequestTemplate(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) (*ChannelMonitorRequestTemplate, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *ChannelMonitorRequestTemplateService) AdminCreateChannelMonitorRequestTemplate(
	ctx context.Context,
	actor authz.Actor,
	params ChannelMonitorRequestTemplateCreateParams,
) (*ChannelMonitorRequestTemplate, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Create(ctx, params)
}

func (s *ChannelMonitorRequestTemplateService) AdminUpdateChannelMonitorRequestTemplate(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	params ChannelMonitorRequestTemplateUpdateParams,
) (*ChannelMonitorRequestTemplate, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Update(ctx, id, params)
}

func (s *ChannelMonitorRequestTemplateService) AdminDeleteChannelMonitorRequestTemplate(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.Delete(ctx, id)
}

func (s *ChannelMonitorRequestTemplateService) AdminCountAssociatedChannelMonitors(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) (int64, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return 0, err
	}
	return s.CountAssociatedMonitors(ctx, id)
}

func (s *ChannelMonitorRequestTemplateService) AdminListAssociatedChannelMonitors(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) ([]*AssociatedMonitorBrief, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ListAssociatedMonitors(ctx, id)
}

func (s *ChannelMonitorRequestTemplateService) AdminApplyChannelMonitorRequestTemplate(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	monitorIDs []int64,
) (int64, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return 0, err
	}
	return s.ApplyToMonitors(ctx, id, monitorIDs)
}

func (s *ChannelMonitorService) AdminListChannelMonitors(
	ctx context.Context,
	actor authz.Actor,
	params ChannelMonitorListParams,
) ([]*ChannelMonitor, int64, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, 0, err
	}
	return s.List(ctx, params)
}

func (s *ChannelMonitorService) AdminGetChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) (*ChannelMonitor, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *ChannelMonitorService) AdminCreateChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	params ChannelMonitorCreateParams,
) (*ChannelMonitor, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Create(ctx, params)
}

func (s *ChannelMonitorService) AdminUpdateChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	params ChannelMonitorUpdateParams,
) (*ChannelMonitor, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Update(ctx, id, params)
}

func (s *ChannelMonitorService) AdminDeleteChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.Delete(ctx, id)
}

func (s *ChannelMonitorService) AdminDuplicateChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	id, createdBy int64,
	actorScope, operationKey string,
) (*ChannelMonitor, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Duplicate(ctx, id, createdBy, actorScope, operationKey)
}

func (s *ChannelMonitorService) AdminRecoverDuplicateChannelMonitor(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	actorScope, operationKey string,
) (*ChannelMonitor, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RecoverDuplicate(ctx, id, actorScope, operationKey)
}

func (s *OpsService) AdminListAlertRules(
	ctx context.Context,
	actor authz.Actor,
) ([]*OpsAlertRule, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ListAlertRules(ctx)
}

func (s *OpsService) AdminCreateAlertRule(
	ctx context.Context,
	actor authz.Actor,
	rule *OpsAlertRule,
) (*OpsAlertRule, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.CreateAlertRule(ctx, rule)
}

func (s *OpsService) AdminUpdateAlertRule(
	ctx context.Context,
	actor authz.Actor,
	rule *OpsAlertRule,
) (*OpsAlertRule, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.UpdateAlertRule(ctx, rule)
}

func (s *OpsService) AdminDeleteAlertRule(
	ctx context.Context,
	actor authz.Actor,
	id int64,
) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.DeleteAlertRule(ctx, id)
}

func (s *OpsService) AdminCreateAlertSilence(
	ctx context.Context,
	actor authz.Actor,
	silence *OpsAlertSilence,
) (*OpsAlertSilence, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.CreateAlertSilence(ctx, silence)
}
