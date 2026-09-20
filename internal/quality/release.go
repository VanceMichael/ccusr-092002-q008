package quality

import (
	"context"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// DisposeLotInput 对不合格物料批次执行处置（销毁/退回供应商/灭活）。
type DisposeLotInput struct {
	LotRef          string
	DispositionType string
	OperatorID      string
	Remark          string
}

// DisposeLot 登记处置并把批次置为 disposed；处置后不能再被称量或合批。
func (s *Service) DisposeLot(ctx context.Context, in DisposeLotInput) (store.Disposal, error) {
	if err := nonEmpty("lot_ref", in.LotRef); err != nil {
		return store.Disposal{}, err
	}
	switch in.DispositionType {
	case "destroy", "return_to_supplier", "deactivated":
	default:
		return store.Disposal{}, ruleError(CodeValidation, "未知处置方式：%s", in.DispositionType)
	}

	disposal := store.Disposal{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.OperatorID, RoleWarehouse, RolePharmacyHead); err != nil {
			return err
		}
		lot, err := store.MaterialLotByRefIn(ctx, q, in.LotRef)
		if err != nil {
			return mapStoreError(err)
		}
		if lot.State == "disposed" {
			return ruleError(CodeStateConflict, "批次 %s 已完成处置，不能重复处置", lot.LotRef)
		}
		if !lot.HadFailure && lot.State != "rejected" && lot.State != "held" {
			return ruleError(CodeStateConflict,
				"批次 %s 未被判定不合格或挂起，不能走不合格处置流程", lot.LotRef)
		}
		now := s.timeNow()
		disposal = store.Disposal{
			ID: newID("dsp"), MaterialLotID: lot.ID,
			DispositionType: in.DispositionType, DisposedAt: now,
			DisposedBy: in.OperatorID, Remark: in.Remark,
		}
		if err := store.InsertDisposalIn(ctx, q, disposal); err != nil {
			return mapStoreError(err)
		}
		return store.SetLotStateIn(ctx, q, lot.ID, "disposed")
	})
	return disposal, err
}

// ReleaseBatchInput 药事负责人放行成品。
type ReleaseBatchInput struct {
	BatchRef string
	HeadID   string
	Reason   string
}

// ReleaseBatch 只有药事负责人可以签发放行；配制负责人放行在角色层即被拒绝。
// 放行前强制三道闸：成品检验合格、无不合格原料阻断、每张配方物料当前都没有失败记录。
func (s *Service) ReleaseBatch(ctx context.Context, in ReleaseBatchInput) (store.ReleaseDecision, error) {
	return s.decideRelease(ctx, in.BatchRef, in.HeadID, "released", in.Reason)
}

// HoldBatch 药事负责人暂缓放行。
func (s *Service) HoldBatch(ctx context.Context, in ReleaseBatchInput) (store.ReleaseDecision, error) {
	return s.decideRelease(ctx, in.BatchRef, in.HeadID, "held", in.Reason)
}

// RejectBatch 药事负责人拒绝放行成品。
func (s *Service) RejectBatch(ctx context.Context, in ReleaseBatchInput) (store.ReleaseDecision, error) {
	return s.decideRelease(ctx, in.BatchRef, in.HeadID, "rejected", in.Reason)
}

func (s *Service) decideRelease(ctx context.Context, batchRef, headID, decision, reason string) (store.ReleaseDecision, error) {
	if err := nonEmpty("batch_ref", batchRef); err != nil {
		return store.ReleaseDecision{}, err
	}
	result := store.ReleaseDecision{}
	err := s.withTx(ctx, func(q store.Tx) error {
		// 放行签字权专属药事负责人；配制负责人、检验人、仓储在此一律被拒绝。
		if _, err := requireStaff(ctx, q, headID, RolePharmacyHead); err != nil {
			return err
		}
		batch, err := store.CompoundBatchByRefIn(ctx, q, batchRef)
		if err != nil {
			return mapStoreError(err)
		}
		if batch.Status == "rejected" {
			return ruleError(CodeStateConflict, "配制批次 %s 已拒收，不能再作出 %s 决策",
				batch.BatchRef, decisionLabel(decision))
		}
		if decision == "released" {
			if batch.QualityBlocked {
				return ruleError(CodeQualityBlocked,
					"配制批次 %s 被不合格原料阻断，禁止放行：%s", batch.BatchRef, batch.QualityBlockReason)
			}
			if batch.Status == "recalled" {
				return ruleError(CodeQualityBlocked, "配制批次 %s 已召回，不能重新放行", batch.BatchRef)
			}
			// 闸一：成品必须有最新合格检验结论。
			finished, err := store.LatestFinishedInspection(ctx, q, "compound_batch", batch.ID)
			if err != nil {
				return ruleError(CodeStateConflict, "成品尚未取得检验结论，不能放行")
			}
			if finished.Verdict != "qualified" {
				return ruleError(CodeQualityBlocked, "成品最新检验结论为 %s，不能放行", finished.Verdict)
			}
			// 闸二：每张配方的物料在放行此刻再次核对失败标记。
			chargings, err := store.ChargingsByBatchIn(ctx, q, batch.ID)
			if err != nil {
				return err
			}
			if len(chargings) == 0 {
				return ruleError(CodeStateConflict, "配制批次 %s 尚无投料记录，不能放行", batch.BatchRef)
			}
			for _, charging := range chargings {
				lot, err := store.MaterialLotIn(ctx, q, charging.MaterialLotID)
				if err != nil {
					return err
				}
				if lot.HadFailure {
					return ruleError(CodeQualityBlocked,
						"物料批次 %s（或其谱系上游）存在不合格记录，配制批次 %s 禁止放行",
						lot.LotRef, batch.BatchRef)
				}
			}
		}

		now := s.timeNow()
		release := store.ReleaseDecision{
			ID: newID("rel"), CompoundBatchID: batch.ID, Decision: decision,
			Reason: reason, DecidedAt: now, DecidedBy: headID,
		}
		if err := store.InsertReleaseDecisionIn(ctx, q, release); err != nil {
			return mapStoreError(err)
		}
		switch decision {
		case "released":
			if err := store.UpdateCompoundStatusIn(ctx, q, batch.ID, "released"); err != nil {
				return err
			}
		case "rejected":
			if err := store.UpdateCompoundStatusIn(ctx, q, batch.ID, "rejected"); err != nil {
				return err
			}
		}
		result = release
		return nil
	})
	return result, err
}

func decisionLabel(decision string) string {
	switch decision {
	case "released":
		return "放行"
	case "held":
		return "暂缓放行"
	case "rejected":
		return "拒收"
	case "recalled":
		return "召回"
	}
	return decision
}
