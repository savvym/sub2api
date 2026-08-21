package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *SettingService) AdminGetAllSettings(ctx context.Context, actor authz.Actor) (*SystemSettings, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetAllSettings(ctx)
}

func (s *SettingService) AdminGetAuthSourceDefaultSettings(ctx context.Context, actor authz.Actor) (*AuthSourceDefaultSettings, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetAuthSourceDefaultSettings(ctx)
}

func (s *SettingService) AdminUpdateSettingsWithAuthSourceDefaultsOmitting(ctx context.Context, actor authz.Actor, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings, omitted OmittedSettingKeys) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.UpdateSettingsWithAuthSourceDefaultsOmitting(ctx, settings, authDefaults, omitted)
}
