package domain

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

func weightsEqual(a, b float64) bool { return math.Abs(a-b) <= weightEps }

// Dispense 从来货批次分装成若干容器。
// 守恒规则：累计分装重量不得超过来货登记重量。
// 即使来货批次事后被判不合格，分装记录也保留——它是下游阻断追溯的事实来源。
func (s *Service) Dispense(ctx context.Context, callerID, receiptRef string, in DispenseInput) (*DispenseView, error) {
	if len(in.Splits) == 0 {
		return nil, fail(400, "invalid_input", "至少需要一条分装记录")
	}
	total := 0.0
	for _, split := range in.Splits {
		if split.ContainerRef == "" || split.Weight <= 0 {
			return nil, fail(400, "invalid_input", "每条分装需要容器编号与正重量")
		}
		total += split.Weight
	}
	view := &DispenseView{ReceiptRef: receiptRef}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleWarehouse)
		if err != nil {
			return err
		}
		var receiptID string
		var received float64
		err = tx.QueryRowContext(ctx,
			`SELECT id, received_weight FROM receipts WHERE receipt_ref = ?`, receiptRef).
			Scan(&receiptID, &received)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "receipt_not_found", "来货批次不存在")
		}
		if err != nil {
			return err
		}
		existing, err := dispensedTotal(ctx, tx, receiptID)
		if err != nil {
			return err
		}
		if existing+total > received+weightEps {
			return fail(422, "weight_exceeded",
				"分装总重超过来货重量：已分装+本次 > 来货，重量无法核对")
		}
		for _, split := range in.Splits {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO containers (id, receipt_id, container_ref, weight, created_at, created_by)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				newID(), receiptID, split.ContainerRef, split.Weight, nowISO(), actor.ID); err != nil {
				return mapUnique(err, "容器编号已存在: "+split.ContainerRef)
			}
			view.Containers = append(view.Containers, ContainerView{
				ContainerRef: split.ContainerRef, Weight: split.Weight})
		}
		view.ReceivedWeight, view.DispensedTotal = received, existing+total
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

func dispensedTotal(ctx context.Context, tx *sql.Tx, receiptID string) (float64, error) {
	var total sql.NullFloat64
	err := tx.QueryRowContext(ctx,
		`SELECT SUM(weight) FROM containers WHERE receipt_id = ?`, receiptID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Float64, nil
}

// Merge 将多个同药材容器合批。守恒规则：
//  1. 每个来源容器的取用量不得超过其剩余重量（原重 - 已入合批 - 已直接投料）；
//  2. 合批产出重量必须等于各来源取用量之和（容差内）；
//  3. 来源容器必须属于同一种药材。
func (s *Service) Merge(ctx context.Context, callerID string, in MergeInput) (*MergeView, error) {
	if in.MergeRef == "" || in.MaterialCode == "" {
		return nil, fail(400, "invalid_input", "合批编号与药材编码必填")
	}
	if len(in.Sources) == 0 {
		return nil, fail(400, "invalid_input", "至少需要一个合批来源")
	}
	inputTotal := 0.0
	for _, src := range in.Sources {
		if src.ContainerRef == "" || src.InputWeight <= 0 {
			return nil, fail(400, "invalid_input", "每条来源需要容器编号与正取用量")
		}
		inputTotal += src.InputWeight
	}
	if !weightsEqual(inputTotal, in.OutputWeight) {
		return nil, fail(422, "weight_mismatch",
			"合批产出重量必须等于来源取用量之和，重量无法核对")
	}
	view := &MergeView{MergeRef: in.MergeRef, MaterialCode: in.MaterialCode,
		InputTotal: inputTotal, OutputWeight: in.OutputWeight}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleWarehouse)
		if err != nil {
			return err
		}
		var materialID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM materials WHERE code = ?`, in.MaterialCode).
			Scan(&materialID)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(400, "unknown_material", "未知药材编码: "+in.MaterialCode)
		}
		if err != nil {
			return err
		}
		mergeID := newID()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO merges (id, merge_ref, material_id, output_weight, created_at, created_by, note)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			mergeID, in.MergeRef, materialID, in.OutputWeight, nowISO(), actor.ID, in.Note); err != nil {
			return mapUnique(err, "合批编号已存在: "+in.MergeRef)
		}
		for _, src := range in.Sources {
			var containerID, containerMaterial string
			var weight float64
			err = tx.QueryRowContext(ctx,
				`SELECT c.id, r.material_id, c.weight
				   FROM containers c JOIN receipts r ON r.id = c.receipt_id
				  WHERE c.container_ref = ?`, src.ContainerRef).
				Scan(&containerID, &containerMaterial, &weight)
			if errors.Is(err, sql.ErrNoRows) {
				return fail(404, "container_not_found", "容器不存在: "+src.ContainerRef)
			}
			if err != nil {
				return err
			}
			if containerMaterial != materialID {
				return fail(422, "material_mismatch",
					"容器 "+src.ContainerRef+" 的药材与合批药材不一致，禁止跨药材合批")
			}
			remaining, err := containerRemaining(ctx, tx, containerID, weight)
			if err != nil {
				return err
			}
			if src.InputWeight > remaining+weightEps {
				return fail(422, "weight_exceeded",
					"容器 "+src.ContainerRef+" 取用量超过剩余重量，重量无法核对")
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO merge_sources (id, merge_id, container_id, input_weight)
				 VALUES (?, ?, ?, ?)`,
				newID(), mergeID, containerID, src.InputWeight); err != nil {
				return err
			}
			view.Sources = append(view.Sources, MergeSourceView{
				ContainerRef: src.ContainerRef, InputWeight: src.InputWeight})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// containerRemaining 计算容器尚未被合批取用与直接投料消耗的重量。
func containerRemaining(ctx context.Context, tx *sql.Tx, containerID string, containerWeight float64) (float64, error) {
	var usedMerge, usedWeigh sql.NullFloat64
	if err := tx.QueryRowContext(ctx,
		`SELECT SUM(ms.input_weight) FROM merge_sources ms WHERE ms.container_id = ?`,
		containerID).Scan(&usedMerge); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT SUM(w.weight) FROM weighings w WHERE w.container_id = ?`,
		containerID).Scan(&usedWeigh); err != nil {
		return 0, err
	}
	return containerWeight - usedMerge.Float64 - usedWeigh.Float64, nil
}

// mergeRemaining 计算合批产出尚未被投料消耗的重量。
func mergeRemaining(ctx context.Context, tx *sql.Tx, mergeID string, outputWeight float64) (float64, error) {
	var used sql.NullFloat64
	if err := tx.QueryRowContext(ctx,
		`SELECT SUM(weight) FROM weighings WHERE merge_id = ?`, mergeID).Scan(&used); err != nil {
		return 0, err
	}
	return outputWeight - used.Float64, nil
}
