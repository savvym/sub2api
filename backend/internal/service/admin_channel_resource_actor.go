package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// AdminListChannels exposes channel topology only after the admin request actor
// has crossed the resource service boundary.
func (s *ChannelService) AdminListChannels(
	ctx context.Context,
	actor authz.Actor,
	params pagination.PaginationParams,
	status, search string,
) ([]Channel, *pagination.PaginationResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, nil, err
	}
	return s.List(ctx, params, status, search)
}

func (s *ChannelService) AdminGetChannel(ctx context.Context, actor authz.Actor, id int64) (*Channel, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetByID(ctx, id)
}

func (s *ChannelService) AdminCreateChannel(ctx context.Context, actor authz.Actor, input *CreateChannelInput) (*Channel, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Create(ctx, input)
}

func (s *ChannelService) AdminUpdateChannel(ctx context.Context, actor authz.Actor, id int64, input *UpdateChannelInput) (*Channel, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.Update(ctx, id, input)
}

func (s *ChannelService) AdminDeleteChannel(ctx context.Context, actor authz.Actor, id int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.Delete(ctx, id)
}
