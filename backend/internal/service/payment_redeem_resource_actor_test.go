package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type paymentRedeemActorRepoStub struct {
	RedeemCodeRepository
	createCalls      int
	getByCodeCalls   int
	batchUpdateCalls int
}

func (s *paymentRedeemActorRepoStub) Create(_ context.Context, _ *RedeemCode) error {
	s.createCalls++
	return nil
}

func (s *paymentRedeemActorRepoStub) GetByCode(_ context.Context, code string) (*RedeemCode, error) {
	s.getByCodeCalls++
	return &RedeemCode{
		ID:     1,
		Code:   code,
		Type:   RedeemTypeBalance,
		Value:  1,
		Status: StatusDisabled,
	}, nil
}

func (s *paymentRedeemActorRepoStub) BatchUpdate(_ context.Context, ids []int64, _ RedeemCodeBatchUpdateFields) (int64, error) {
	s.batchUpdateCalls++
	return int64(len(ids)), nil
}

func (s *paymentRedeemActorRepoStub) totalCalls() int {
	return s.createCalls + s.getByCodeCalls + s.batchUpdateCalls
}

func TestPaymentPlanAdminFacadesRejectMissingActorBeforeEntAccess(t *testing.T) {
	svc := &PaymentConfigService{}
	ctx := context.Background()
	missingActor := authz.Actor{}

	operations := []struct {
		name string
		call func() error
	}{
		{name: "list", call: func() error { _, err := svc.AdminListPlans(ctx, missingActor); return err }},
		{name: "group info", call: func() error {
			_, err := svc.AdminGetGroupInfoMap(ctx, missingActor, []*dbent.SubscriptionPlan{{GroupID: 7}})
			return err
		}},
		{name: "create", call: func() error { _, err := svc.AdminCreatePlan(ctx, missingActor, CreatePlanRequest{}); return err }},
		{name: "update", call: func() error { _, err := svc.AdminUpdatePlan(ctx, missingActor, 7, UpdatePlanRequest{}); return err }},
		{name: "delete", call: func() error { return svc.AdminDeletePlan(ctx, missingActor, 7) }},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			require.ErrorIs(t, operation.call(), ErrAdminResourceActorUnavailable)
		})
	}
}

func TestPaymentPlanAdminFacadesAcceptTrustedActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			group, err := client.Group.Create().SetName("payment plan actor group").Save(ctx)
			require.NoError(t, err)
			svc := &PaymentConfigService{entClient: client}

			created, err := svc.AdminCreatePlan(ctx, testCase.actor, CreatePlanRequest{
				GroupID:      int64(group.ID),
				Name:         "Actor Plan",
				Price:        10,
				ValidityDays: 30,
				ValidityUnit: "day",
				ForSale:      true,
			})
			require.NoError(t, err)

			plans, err := svc.AdminListPlans(ctx, testCase.actor)
			require.NoError(t, err)
			require.Len(t, plans, 1)
			groupInfo, err := svc.AdminGetGroupInfoMap(ctx, testCase.actor, plans)
			require.NoError(t, err)
			require.Equal(t, group.Name, groupInfo[int64(group.ID)].Name)

			updatedName := "Updated Actor Plan"
			updated, err := svc.AdminUpdatePlan(ctx, testCase.actor, int64(created.ID), UpdatePlanRequest{Name: &updatedName})
			require.NoError(t, err)
			require.Equal(t, updatedName, updated.Name)
			require.NoError(t, svc.AdminDeletePlan(ctx, testCase.actor, int64(created.ID)))
		})
	}
}

func TestRedeemAdminFacadesRejectMissingActorBeforeRepositoryAccess(t *testing.T) {
	repo := &paymentRedeemActorRepoStub{}
	redeemSvc := &RedeemService{redeemRepo: repo}
	adminSvc := &adminServiceImpl{redeemCodeRepo: repo}
	ctx := context.Background()
	missingActor := authz.Actor{}

	operations := []struct {
		name string
		call func() error
	}{
		{name: "get by code", call: func() error { _, err := redeemSvc.AdminGetByCode(ctx, missingActor, ""); return err }},
		{name: "create code", call: func() error { return redeemSvc.AdminCreateCode(ctx, missingActor, nil) }},
		{name: "redeem", call: func() error { _, err := redeemSvc.AdminRedeem(ctx, missingActor, 0, ""); return err }},
		{name: "batch update", call: func() error { _, err := redeemSvc.AdminBatchUpdate(ctx, missingActor, nil); return err }},
		{name: "generate", call: func() error { _, err := adminSvc.GenerateRedeemCodes(ctx, missingActor, nil); return err }},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			require.ErrorIs(t, operation.call(), ErrAdminResourceActorUnavailable)
		})
	}
	require.Zero(t, repo.totalCalls())
}

func TestRedeemAdminFacadesAcceptTrustedActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &paymentRedeemActorRepoStub{}
			redeemSvc := &RedeemService{redeemRepo: repo}
			ctx := context.Background()

			require.NoError(t, redeemSvc.AdminCreateCode(ctx, testCase.actor, &RedeemCode{
				Code:   "ACTOR-CODE",
				Type:   RedeemTypeBalance,
				Value:  1,
				Status: StatusUnused,
			}))
			_, err := redeemSvc.AdminGetByCode(ctx, testCase.actor, "ACTOR-CODE")
			require.NoError(t, err)
			notes := "actor update"
			_, err = redeemSvc.AdminBatchUpdate(ctx, testCase.actor, &RedeemCodeBatchUpdateInput{
				IDs:    []int64{1},
				Fields: RedeemCodeBatchUpdateFields{Notes: &notes},
			})
			require.NoError(t, err)
			_, err = redeemSvc.AdminRedeem(ctx, testCase.actor, 9, "ACTOR-CODE")
			require.ErrorIs(t, err, ErrRedeemCodeUsed)

			adminSvc := &adminServiceImpl{redeemCodeRepo: repo}
			codes, err := adminSvc.GenerateRedeemCodes(ctx, testCase.actor, &GenerateRedeemCodesInput{
				Count: 1,
				Type:  RedeemTypeBalance,
				Value: 10,
			})
			require.NoError(t, err)
			require.Len(t, codes, 1)
			require.Equal(t, 2, repo.createCalls)
			require.Equal(t, 2, repo.getByCodeCalls)
			require.Equal(t, 1, repo.batchUpdateCalls)
		})
	}
}
