package quality

import (
	"context"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// CreateCompoundBatchInput 开立院内制剂配制批次（成品批）。
type CreateCompoundBatchInput struct {
	BatchRef        string
	FormulaRef      string
	FormulaRevision int
	ProductName     string
	ManagerID       string
}

func (s *Service) CreateCompoundBatch(ctx context.Context, in CreateCompoundBatchInput) (store.CompoundBatch, error) {
	if err := nonEmpty("batch_ref", in.BatchRef); err != nil {
		return store.CompoundBatch{}, err
	}
	if err := nonEmpty("formula_ref", in.FormulaRef); err != nil {
		return store.CompoundBatch{}, err
	}
	if in.FormulaRevision <= 0 {
		return store.CompoundBatch{}, ruleError(CodeValidation, "formula_revision 必须为正整数")
	}
	if err := nonEmpty("product_name", in.ProductName); err != nil {
		return store.CompoundBatch{}, err
	}

	batch := store.CompoundBatch{}
	err := s.withTx(ctx, func(q store.Tx) error {
		// 配制批次由配制负责人开立；该角色在检验签字处会被拒绝。
		if _, err := requireStaff(ctx, q, in.ManagerID, RoleProductionManager); err != nil {
			return err
		}
		batch = store.CompoundBatch{
			ID: newID("bat"), BatchRef: in.BatchRef, FormulaRef: in.FormulaRef,
			FormulaRevision: in.FormulaRevision, ProductName: in.ProductName,
			ProductionManager: in.ManagerID, Status: "planned", CreatedAt: s.timeNow(),
		}
		return mapStoreError(store.InsertCompoundBatchIn(ctx, q, batch))
	})
	return batch, err
}

// WeighMaterialInput 称量：从某物料批次领出指定重量，供配制批次使用。
type WeighMaterialInput struct {
	BatchRef  string
	LotRef    string
	WeightMg  int64
	WeigherID string
	Remark    string
}

type WeighResult struct {
	Weighing store.Weighing
	Lot      store.MaterialLot
}

// WeighMaterial 做投料前称量。只有"可用"且未挂失败标记的物料可以被称量；
// 称量重量逐笔登记台账，保证可核对。
func (s *Service) WeighMaterial(ctx context.Context, in WeighMaterialInput) (WeighResult, error) {
	if err := nonEmpty("batch_ref", in.BatchRef); err != nil {
		return WeighResult{}, err
	}
	if err := nonEmpty("lot_ref", in.LotRef); err != nil {
		return WeighResult{}, err
	}
	if err := positiveWeight("weight_mg", in.WeightMg); err != nil {
		return WeighResult{}, err
	}

	result := WeighResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.WeigherID, RoleWarehouse); err != nil {
			return err
		}
		batch, err := store.CompoundBatchByRefIn(ctx, q, in.BatchRef)
		if err != nil {
			return mapStoreError(err)
		}
		if batch.Status != "planned" {
			return ruleError(CodeStateConflict,
				"配制批次 %s 当前状态为 %s，只有待投料批次允许继续称量",
				batch.BatchRef, stateLabel(batch.Status))
		}
		lot, err := store.MaterialLotByRefIn(ctx, q, in.LotRef)
		if err != nil {
			return mapStoreError(err)
		}
		if lot.HadFailure {
			return ruleError(CodeQualityBlocked,
				"物料批次 %s 存在不合格记录（含其谱系上游），禁止称量领用", lot.LotRef)
		}
		if lot.State != "available" {
			return ruleError(CodeStateConflict,
				"物料批次 %s 当前状态为 %s，只有检验合格可用的批次才能称量",
				lot.LotRef, stateLabel(lot.State))
		}
		inSum, outSum, err := store.LotTotalsIn(ctx, q, lot.ID)
		if err != nil {
			return err
		}
		if inSum-outSum < in.WeightMg {
			return ruleError(CodeWeightMismatch,
				"物料批次 %s 余量仅 %d mg，无法称量 %d mg", lot.LotRef, inSum-outSum, in.WeightMg)
		}

		now := s.timeNow()
		movement := store.Movement{
			ID: newID("mov"), MovementType: "weigh",
			CreatedAt: now, CreatedBy: in.WeigherID,
			Remark: orRemark(in.Remark, "配制称量领出（批次 "+batch.BatchRef+"）"),
		}
		item := store.MovementItem{
			ID: newID("itm"), MovementID: movement.ID, MaterialLotID: lot.ID,
			Direction: "out", WeightMg: in.WeightMg,
		}
		if err := store.InsertMovementIn(ctx, q, movement, []store.MovementItem{item}); err != nil {
			return mapStoreError(err)
		}
		weighing := store.Weighing{
			ID: newID("wgh"), CompoundBatchID: batch.ID, MaterialLotID: lot.ID,
			MovementItemID: item.ID, WeightMg: in.WeightMg, WeighedAt: now,
		}
		if err := store.InsertWeighingIn(ctx, q, weighing); err != nil {
			return mapStoreError(err)
		}
		result = WeighResult{Weighing: weighing, Lot: lot}
		return nil
	})
	return result, err
}

// ChargeBatchInput 配制投料：把某配制批次下尚未投料的称量记录一次性投入配制。
type ChargeBatchInput struct {
	BatchRef  string
	ManagerID string
	Remark    string
}

type ChargeResult struct {
	Batch     store.CompoundBatch
	Chargings []store.Charging
	TotalMg   int64
}

// ChargeBatch 由配制负责人执行投料；称量人（仓储）与投料确认人（配制负责人）必须为不同岗位。
// 投料瞬间再次校验所有物料是否已被不合格阻断——不合格样品必须阻断所有下游批次。
func (s *Service) ChargeBatch(ctx context.Context, in ChargeBatchInput) (ChargeResult, error) {
	if err := nonEmpty("batch_ref", in.BatchRef); err != nil {
		return ChargeResult{}, err
	}
	result := ChargeResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		manager, err := requireStaff(ctx, q, in.ManagerID, RoleProductionManager)
		if err != nil {
			return err
		}
		batch, err := store.CompoundBatchByRefIn(ctx, q, in.BatchRef)
		if err != nil {
			return mapStoreError(err)
		}
		if batch.Status != "planned" {
			return ruleError(CodeStateConflict, "配制批次 %s 状态为 %s，只有待投料批次允许投料",
				batch.BatchRef, stateLabel(batch.Status))
		}
		if batch.QualityBlocked {
			return ruleError(CodeQualityBlocked, "配制批次 %s 已被不合格原料阻断：%s",
				batch.BatchRef, batch.QualityBlockReason)
		}

		weighings, err := store.UnchargedWeighingsIn(ctx, q, batch.ID)
		if err != nil {
			return err
		}
		if len(weighings) == 0 {
			return ruleError(CodeValidation, "配制批次 %s 没有待投料的称量记录", batch.BatchRef)
		}

		now := s.timeNow()
		chargings := make([]store.Charging, 0, len(weighings))
		var total int64
		for _, weighing := range weighings {
			// 称量之后、投料之前可能出现不合格判定，此处逐笔再校验。
			lot, err := store.MaterialLotIn(ctx, q, weighing.MaterialLotID)
			if err != nil {
				return err
			}
			if lot.HadFailure {
				return ruleError(CodeQualityBlocked,
					"物料批次 %s 在称量后被判定不合格（或其谱系上游出现不合格），配制批次 %s 禁止投料",
					lot.LotRef, batch.BatchRef)
			}
			charging := store.Charging{
				ID: newID("chg"), CompoundBatchID: batch.ID, MaterialLotID: weighing.MaterialLotID,
				WeighingID: weighing.ID, WeightMg: weighing.WeightMg,
				ChargedAt: now, ChargedBy: manager.ID,
			}
			if err := store.InsertChargingIn(ctx, q, charging); err != nil {
				return mapStoreError(err)
			}
			chargings = append(chargings, charging)
			total += weighing.WeightMg
		}
		if err := store.UpdateCompoundStatusIn(ctx, q, batch.ID, "charged"); err != nil {
			return err
		}
		batch.Status = "charged"
		result = ChargeResult{Batch: batch, Chargings: chargings, TotalMg: total}
		return nil
	})
	return result, err
}
