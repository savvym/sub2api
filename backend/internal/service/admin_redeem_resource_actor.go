package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *adminServiceImpl) AdminListRedeemCodes(ctx context.Context, actor authz.Actor, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]RedeemCode, int64, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, 0, err
	}
	return s.ListRedeemCodes(ctx, page, pageSize, codeType, status, search, sortBy, sortOrder)
}

func (s *adminServiceImpl) AdminGetRedeemCode(ctx context.Context, actor authz.Actor, id int64) (*RedeemCode, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetRedeemCode(ctx, id)
}

func (s *adminServiceImpl) AdminDeleteRedeemCode(ctx context.Context, actor authz.Actor, id int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.DeleteRedeemCode(ctx, id)
}

func (s *adminServiceImpl) AdminBatchDeleteRedeemCodes(ctx context.Context, actor authz.Actor, ids []int64) (int64, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return 0, err
	}
	return s.BatchDeleteRedeemCodes(ctx, ids)
}

func (s *adminServiceImpl) AdminExpireRedeemCode(ctx context.Context, actor authz.Actor, id int64) (*RedeemCode, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ExpireRedeemCode(ctx, id)
}
