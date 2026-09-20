package quality

import (
	"context"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// SplitInput 分装（拆分）：把一个物料批次拆成若干子袋，各子袋重量之和必须等于领出重量。
type SplitInput struct {
	SourceLotRef string
	WeightMg     int64 // 从源批次领出的重量
	Parts        []SplitPart
	OperatorID   string
	Remark       string
}

type SplitPart struct {
	LotRef   string
	WeightMg int64
}

type SplitResult struct {
	MovementID string
	Parts      []store.MaterialLot
}

func (s *Service) Split(ctx context.Context, in SplitInput) (SplitResult, error) {
	if err := nonEmpty("source_lot_ref", in.SourceLotRef); err != nil {
		return SplitResult{}, err
	}
	if err := positiveWeight("weight_mg", in.WeightMg); err != nil {
		return SplitResult{}, err
	}
	if len(in.Parts) == 0 {
		return SplitResult{}, ruleError(CodeValidation, "分装至少需要一个子批次")
	}
	var partSum int64
	refs := map[string]bool{}
	for _, part := range in.Parts {
		if err := positiveWeight("parts[].weight_mg", part.WeightMg); err != nil {
			return SplitResult{}, err
		}
		if err := nonEmpty("parts[].lot_ref", part.LotRef); err != nil {
			return SplitResult{}, err
		}
		if refs[part.LotRef] {
			return SplitResult{}, ruleError(CodeConflict, "子批次编号重复：%s", part.LotRef)
		}
		refs[part.LotRef] = true
		partSum += part.WeightMg
	}
	if partSum != in.WeightMg {
		return SplitResult{}, ruleError(CodeWeightMismatch,
			"分装重量不守恒：领出 %d mg，子批次合计 %d mg（每毫克都必须可核对）", in.WeightMg, partSum)
	}

	result := SplitResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.OperatorID, RoleWarehouse); err != nil {
			return err
		}
		source, err := store.MaterialLotByRefIn(ctx, q, in.SourceLotRef)
		if err != nil {
			return mapStoreError(err)
		}
		if source.State == "rejected" || source.State == "disposed" {
			return ruleError(CodeStateConflict, "源批次 %s 已%s，禁止分装", source.LotRef, stateLabel(source.State))
		}
		inSum, outSum, err := store.LotTotalsIn(ctx, q, source.ID)
		if err != nil {
			return err
		}
		if inSum-outSum < in.WeightMg {
			return ruleError(CodeWeightMismatch,
				"源批次 %s 可用余量仅 %d mg，无法领出 %d mg",
				source.LotRef, inSum-outSum, in.WeightMg)
		}

		now := s.timeNow()
		movement := store.Movement{
			ID: newID("mov"), MovementType: "split", Ref: source.ID,
			CreatedAt: now, CreatedBy: in.OperatorID,
			Remark: orRemark(in.Remark, "分装拆分"),
		}
		items := []store.MovementItem{{
			ID: newID("itm"), MovementID: movement.ID, MaterialLotID: source.ID,
			Direction: "out", WeightMg: in.WeightMg,
		}}
		parts := make([]store.MaterialLot, 0, len(in.Parts))
		for _, part := range in.Parts {
			child := store.MaterialLot{
				ID: newID("lot"), LotRef: part.LotRef, MaterialID: source.MaterialID,
				RootInboundLotID: source.RootInboundLotID, InitialWeightMg: part.WeightMg,
				// 失败事实随谱系传播：父批次挂过时，子批次也必须保持隔离。
				State:      childState(source),
				HadFailure: source.HadFailure,
				CreatedAt:  now,
				CreatedBy:  in.OperatorID,
			}
			if err := store.InsertMaterialLotIn(ctx, q, child); err != nil {
				return mapStoreError(err)
			}
			items = append(items, store.MovementItem{
				ID: newID("itm"), MovementID: movement.ID, MaterialLotID: child.ID,
				Direction: "in", WeightMg: part.WeightMg,
			})
			parts = append(parts, child)
		}
		if err := store.InsertMovementIn(ctx, q, movement, items); err != nil {
			return mapStoreError(err)
		}
		result = SplitResult{MovementID: movement.ID, Parts: parts}
		return nil
	})
	return result, err
}

// MergeInput 合批：把多个同品种物料批次合并成一个新批次，新批重量必须等于各来源领出重量之和。
type MergeInput struct {
	TargetLotRef string
	Sources      []MergeSource
	OperatorID   string
	Remark       string
}

type MergeSource struct {
	LotRef   string
	WeightMg int64
}

type MergeResult struct {
	MovementID string
	Target     store.MaterialLot
}

func (s *Service) Merge(ctx context.Context, in MergeInput) (MergeResult, error) {
	if err := nonEmpty("target_lot_ref", in.TargetLotRef); err != nil {
		return MergeResult{}, err
	}
	if len(in.Sources) < 2 {
		return MergeResult{}, ruleError(CodeValidation, "合批至少需要两个来源批次")
	}
	var total int64
	seen := map[string]bool{}
	for _, src := range in.Sources {
		if err := nonEmpty("sources[].lot_ref", src.LotRef); err != nil {
			return MergeResult{}, err
		}
		if err := positiveWeight("sources[].weight_mg", src.WeightMg); err != nil {
			return MergeResult{}, err
		}
		if seen[src.LotRef] {
			return MergeResult{}, ruleError(CodeConflict, "来源批次重复：%s", src.LotRef)
		}
		seen[src.LotRef] = true
		total += src.WeightMg
	}

	result := MergeResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.OperatorID, RoleWarehouse); err != nil {
			return err
		}
		now := s.timeNow()
		movement := store.Movement{
			ID: newID("mov"), MovementType: "merge",
			CreatedAt: now, CreatedBy: in.OperatorID,
			Remark: orRemark(in.Remark, "合批"),
		}
		items := make([]store.MovementItem, 0, len(in.Sources))
		sourceLots := make([]store.MaterialLot, 0, len(in.Sources))
		mergedMaterial := ""
		mergedRoot := ""
		hadFailure := false
		for _, src := range in.Sources {
			lot, err := store.MaterialLotByRefIn(ctx, q, src.LotRef)
			if err != nil {
				return mapStoreError(err)
			}
			if lot.State == "rejected" || lot.State == "disposed" {
				return ruleError(CodeStateConflict, "来源批次 %s 已%s，禁止合批", lot.LotRef, stateLabel(lot.State))
			}
			inSum, outSum, err := store.LotTotalsIn(ctx, q, lot.ID)
			if err != nil {
				return err
			}
			if inSum-outSum < src.WeightMg {
				return ruleError(CodeWeightMismatch,
					"来源批次 %s 可用余量仅 %d mg，无法领出 %d mg",
					lot.LotRef, inSum-outSum, src.WeightMg)
			}
			if mergedMaterial == "" {
				mergedMaterial = lot.MaterialID
				mergedRoot = lot.RootInboundLotID
			} else if mergedMaterial != lot.MaterialID {
				return ruleError(CodeValidation,
					"只有同一药材品种才能合批：%s 与 %s 品种不一致", src.LotRef, in.Sources[0].LotRef)
			}
			if lot.HadFailure {
				hadFailure = true
			}
			items = append(items, store.MovementItem{
				ID: newID("itm"), MovementID: movement.ID, MaterialLotID: lot.ID,
				Direction: "out", WeightMg: src.WeightMg,
			})
			sourceLots = append(sourceLots, lot)
		}

		target := store.MaterialLot{
			ID: newID("lot"), LotRef: in.TargetLotRef, MaterialID: mergedMaterial,
			RootInboundLotID: mergedRoot, InitialWeightMg: total,
			State:      mergeTargetState(hadFailure),
			HadFailure: hadFailure,
			CreatedAt:  now, CreatedBy: in.OperatorID,
		}
		if err := store.InsertMaterialLotIn(ctx, q, target); err != nil {
			return mapStoreError(err)
		}
		movement.Ref = target.ID
		items = append(items, store.MovementItem{
			ID: newID("itm"), MovementID: movement.ID, MaterialLotID: target.ID,
			Direction: "in", WeightMg: total,
		})
		if err := store.InsertMovementIn(ctx, q, movement, items); err != nil {
			return mapStoreError(err)
		}
		result = MergeResult{MovementID: movement.ID, Target: target}
		return nil
	})
	return result, err
}

func childState(parent store.MaterialLot) string {
	if parent.HadFailure || parent.State == "held" {
		return "held"
	}
	if parent.State == "quarantined" {
		return "quarantined"
	}
	return "available"
}

func mergeTargetState(hadFailure bool) string {
	if hadFailure {
		return "held"
	}
	return "quarantined"
}

func stateLabel(state string) string {
	switch state {
	case "rejected":
		return "拒收"
	case "disposed":
		return "处置"
	case "held":
		return "挂起"
	case "quarantined":
		return "待检隔离"
	case "available":
		return "可用"
	}
	return state
}

func orRemark(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
