package domain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// CreateBatch 由配制负责人按某个方剂组方版本开批。
func (s *Service) CreateBatch(ctx context.Context, callerID string, in BatchInput) (string, error) {
	if in.BatchRef == "" || in.FormulaRef == "" {
		return "", fail(400, "invalid_input", "batch_ref、formula_ref 必填")
	}
	if in.Revision < 1 {
		return "", fail(400, "invalid_input", "revision 须 ≥ 1")
	}
	var batchID string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleCompoundingLead)
		if err != nil {
			return err
		}
		var formulaID string
		err = tx.QueryRowContext(ctx,
			`SELECT id FROM formulas WHERE formula_ref = ? AND revision = ?`,
			in.FormulaRef, in.Revision).Scan(&formulaID)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "formula_not_found", "方剂版本不存在")
		}
		if err != nil {
			return err
		}
		batchID = newID()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO batches (id, batch_ref, formula_id, lead_id, planned_at, status)
			 VALUES (?, ?, ?, ?, ?, 'planned')`,
			batchID, in.BatchRef, formulaID, actor.ID, nowISO())
		return mapUnique(err, "配制批号已存在")
	})
	return batchID, err
}

// AddWeighing 记录一次称量投料。来源只能是分装容器或合批批（二选一）。
// 除重量守恒外，投料当时即要求上游来货已判定放行：拒收/待判/隔离的原料不得投料。
// 放行时仍会再做一次全链复核，以防投料后上游状态变化。
func (s *Service) AddWeighing(ctx context.Context, callerID, batchRef string, in WeighingInput) error {
	if in.MaterialCode == "" || in.Weight <= 0 {
		return fail(400, "invalid_input", "药材编码与正投料重量必填")
	}
	if (in.ContainerRef == "" && in.MergeRef == "") || (in.ContainerRef != "" && in.MergeRef != "") {
		return fail(400, "invalid_input", "container_ref 与 merge_ref 必须且只能填写一个")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleCompoundingLead)
		if err != nil {
			return err
		}
		var batchID, formulaID, leadID, status string
		err = tx.QueryRowContext(ctx,
			`SELECT id, formula_id, lead_id, status FROM batches WHERE batch_ref = ?`, batchRef).
			Scan(&batchID, &formulaID, &leadID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "batch_not_found", "配制批次不存在")
		}
		if err != nil {
			return err
		}
		if actor.ID != leadID {
			return fail(403, "not_batch_lead", "只有该批配制负责人可以记录投料")
		}
		if status == "released" {
			return fail(422, "batch_locked", "批次已放行，禁止再补投料")
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

		var containerID, mergeID sql.NullString
		if in.ContainerRef != "" {
			var cid, receiptID, cMaterial string
			var cWeight float64
			err = tx.QueryRowContext(ctx,
				`SELECT c.id, c.receipt_id, r.material_id, c.weight
				   FROM containers c JOIN receipts r ON r.id = c.receipt_id
				  WHERE c.container_ref = ?`, in.ContainerRef).
				Scan(&cid, &receiptID, &cMaterial, &cWeight)
			if errors.Is(err, sql.ErrNoRows) {
				return fail(404, "container_not_found", "分装容器不存在")
			}
			if err != nil {
				return err
			}
			if cMaterial != materialID {
				return fail(422, "material_mismatch", "容器内药材与投料药材不一致")
			}
			if err := requireReceiptReleased(ctx, tx, receiptID); err != nil {
				return err
			}
			remaining, err := containerRemaining(ctx, tx, cid, cWeight)
			if err != nil {
				return err
			}
			if in.Weight > remaining+weightEps {
				return fail(422, "weight_exceeded", "投料重量超过容器剩余重量，重量无法核对")
			}
			containerID.Valid, containerID.String = true, cid
		} else {
			var mid, mMaterial string
			var mOutput float64
			err = tx.QueryRowContext(ctx,
				`SELECT id, material_id, output_weight FROM merges WHERE merge_ref = ?`, in.MergeRef).
				Scan(&mid, &mMaterial, &mOutput)
			if errors.Is(err, sql.ErrNoRows) {
				return fail(404, "merge_not_found", "合批批不存在")
			}
			if err != nil {
				return err
			}
			if mMaterial != materialID {
				return fail(422, "material_mismatch", "合批药材与投料药材不一致")
			}
			// 合批的每个来源来货都必须已放行——一袋不合格原料不得随合批混入。
			rows, qerr := tx.QueryContext(ctx,
				`SELECT r.id, r.disposition
				   FROM merge_sources ms
				   JOIN containers c ON c.id = ms.container_id
				   JOIN receipts r ON r.id = c.receipt_id
				  WHERE ms.merge_id = ?`, mid)
			if qerr != nil {
				return qerr
			}
			for rows.Next() {
				var receiptID, disposition string
				if err := rows.Scan(&receiptID, &disposition); err != nil {
					rows.Close()
					return err
				}
				if err := requireReceiptReleased(ctx, tx, receiptID); err != nil {
					rows.Close()
					return err
				}
			}
			rows.Close()
			remaining, err := mergeRemaining(ctx, tx, mid, mOutput)
			if err != nil {
				return err
			}
			if in.Weight > remaining+weightEps {
				return fail(422, "weight_exceeded", "投料重量超过合批剩余重量，重量无法核对")
			}
			mergeID.Valid, mergeID.String = true, mid
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO weighings (id, batch_id, material_id, container_id, merge_id, weight, weighed_at, weighed_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), batchID, materialID, nullable(containerID), nullable(mergeID),
			in.Weight, nowISO(), actor.ID); err != nil {
			return err
		}
		if status == "planned" {
			_, err = tx.ExecContext(ctx, `UPDATE batches SET status = 'in_progress' WHERE id = ?`, batchID)
			return err
		}
		return nil
	})
}

// requireReceiptReleased 校验来货批次处置状态。rejected 给出最明确的阻断信息。
func requireReceiptReleased(ctx context.Context, tx *sql.Tx, receiptID string) error {
	var disposition string
	err := tx.QueryRowContext(ctx, `SELECT disposition FROM receipts WHERE id = ?`, receiptID).
		Scan(&disposition)
	if err != nil {
		return err
	}
	switch disposition {
	case "released":
		return nil
	case "rejected":
		return fail(422, "upstream_rejected", "上游来货批次已判定不合格，禁止下游使用")
	default:
		return fail(422, "upstream_not_released", "上游来货批次尚未放行（"+disposition+"），禁止投料")
	}
}

func nullable(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// FinishCompounding 配制负责人完成配制并登记成品收得重量。
func (s *Service) FinishCompounding(ctx context.Context, callerID, batchRef string, in FinishBatchInput) error {
	if in.YieldWeight <= 0 {
		return fail(400, "invalid_weight", "成品收得重量必须为正数")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleCompoundingLead)
		if err != nil {
			return err
		}
		var batchID, leadID, status string
		err = tx.QueryRowContext(ctx, `SELECT id, lead_id, status FROM batches WHERE batch_ref = ?`, batchRef).
			Scan(&batchID, &leadID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "batch_not_found", "配制批次不存在")
		}
		if err != nil {
			return err
		}
		if actor.ID != leadID {
			return fail(403, "not_batch_lead", "只有该批配制负责人可以完成配制")
		}
		if status != "in_progress" {
			return fail(422, "invalid_status", "只有投料中的批次可以完成配制，当前状态: "+status)
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE batches SET status = 'compounded', yield_weight = ? WHERE id = ?`,
			in.YieldWeight, batchID)
		return err
	})
}

// AddFinishedInspection 录入成品检验。检验人不得是配制负责人；
// 多轮检验追加保存，任一轮不合格都将阻断放行。
func (s *Service) AddFinishedInspection(ctx context.Context, callerID, batchRef string, in FinishedInspectionInput) error {
	if in.Outcome != "pass" && in.Outcome != "fail" {
		return fail(400, "invalid_input", "outcome 只能为 pass 或 fail")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleInspector)
		if err != nil {
			return err
		}
		var batchID, leadID, status string
		err = tx.QueryRowContext(ctx, `SELECT id, lead_id, status FROM batches WHERE batch_ref = ?`, batchRef).
			Scan(&batchID, &leadID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "batch_not_found", "配制批次不存在")
		}
		if err != nil {
			return err
		}
		if status != "compounded" && status != "inspected" {
			return fail(422, "invalid_status", "批次尚未完成配制，不能录入成品检验")
		}
		// 配制负责人不能代替检验人签字；数据库触发器 trg_finished_inspection_not_self 是第二道防线。
		if actor.ID == leadID {
			return fail(403, "inspector_is_lead", "配制负责人不能代替检验人签字")
		}
		var maxRound int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(round_no), 0) FROM finished_inspections WHERE batch_id = ?`,
			batchID).Scan(&maxRound); err != nil {
			return err
		}
		// 触发器 trg_finished_inspection_not_self 在数据库层再次防止配制负责人自检。
		_, err = tx.ExecContext(ctx,
			`INSERT INTO finished_inspections (id, batch_id, round_no, outcome, measured, inspector_id, inspected_at, note)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), batchID, maxRound+1, in.Outcome, in.Measured, actor.ID, nowISO(), in.Note)
		if err != nil && isTriggerError(err, "成品检验人不得是该批次的配制负责人") {
			return fail(403, "inspector_is_lead", "配制负责人不能代替检验人签字")
		}
		if err != nil {
			return err
		}
		if status != "inspected" {
			_, err = tx.ExecContext(ctx, `UPDATE batches SET status = 'inspected' WHERE id = ?`, batchID)
		}
		return err
	})
}

// ReleaseBatch 药事负责人放行/拒收/扣留成品批次。放行时执行全链阻断复核：
// 成品检验全部合格，且血缘上的每个来货批次都处于 released。
func (s *Service) ReleaseBatch(ctx context.Context, callerID, batchRef string, in ReleaseInput) (*BatchView, error) {
	if in.Decision != "released" && in.Decision != "rejected" && in.Decision != "withheld" {
		return nil, fail(400, "invalid_decision", "decision 只能为 released/rejected/withheld")
	}
	var view *BatchView
	var blockedNow bool
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleQARelease)
		if err != nil {
			return err
		}
		var batchID, leadID, status string
		err = tx.QueryRowContext(ctx, `SELECT id, lead_id, status FROM batches WHERE batch_ref = ?`, batchRef).
			Scan(&batchID, &leadID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "batch_not_found", "配制批次不存在")
		}
		if err != nil {
			return err
		}
		if actor.ID == leadID {
			return fail(403, "release_is_lead", "药事放行签字人不得是该批配制负责人")
		}
		var maxRound int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(round_no), 0) FROM release_decisions WHERE batch_id = ?`,
			batchID).Scan(&maxRound); err != nil {
			return err
		}
		effective := in.Decision
		blockedReason := ""
		if in.Decision == "released" {
			if status != "inspected" {
				return fail(422, "not_inspected", "批次未经成品检验，不能放行")
			}
			var failRounds int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM finished_inspections WHERE batch_id = ? AND outcome = 'fail'`,
				batchID).Scan(&failRounds); err != nil {
				return err
			}
			if failRounds > 0 {
				effective, blockedReason = "blocked", "成品检验存在不合格轮次"
			} else {
				bad, lerr := blockingReceipts(ctx, tx, batchID)
				if lerr != nil {
					return lerr
				}
				if len(bad) > 0 {
					effective, blockedReason = "blocked",
						"血缘上存在未放行/不合格来货批次: "+joinRefs(bad)
				}
			}
			// 请求放行但复核未过时，把阻断事实写成 blocked 决定并提交留痕；
			// 提交后再向调用方返回 422，避免回滚抹掉这次阻断判断。
			if effective != "released" {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO release_decisions (id, batch_id, round_no, decision, decided_by, decided_at, note)
					 VALUES (?, ?, ?, 'blocked', ?, ?, ?)`,
					newID(), batchID, maxRound+1, actor.ID, nowISO(), blockedReason); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					`UPDATE batches SET status = 'blocked', blocked_reason = ? WHERE id = ?`,
					blockedReason, batchID); err != nil {
					return err
				}
				blockedNow = true
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO release_decisions (id, batch_id, round_no, decision, decided_by, decided_at, note)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newID(), batchID, maxRound+1, effective, actor.ID, nowISO(),
			in.Note); err != nil {
			if isTriggerError(err, "药事放行签字人不得是该批次的配制负责人") {
				return fail(403, "release_is_lead", "药事放行签字人不得是该批配制负责人")
			}
			return err
		}
		newStatus := "released"
		if effective != "released" {
			newStatus = "blocked"
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE batches SET status = ?, blocked_reason = ? WHERE id = ?`,
			newStatus, blockedReason, batchID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if blockedNow {
		// 阻断决定已提交；返回错误让接口响应 422。
		return nil, fail(422, "release_blocked", "放行被全链阻断复核拒绝，详见批次 blocked_reason")
	}
	view, err = s.GetBatch(ctx, batchRef)
	return view, err
}

// blockingReceipts 返回批次血缘上所有处置状态不是 released 的来货批号，
// 等价于在 批次→称量→(合批→)容器→来货 的图上做闭包查询。
func blockingReceipts(ctx context.Context, tx *sql.Tx, batchID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE upstream(container_id, merge_id) AS (
			SELECT w.container_id, w.merge_id FROM weighings w WHERE w.batch_id = ?
			UNION
			SELECT ms.container_id, NULL
			  FROM upstream u JOIN merge_sources ms ON ms.merge_id = u.merge_id
		)
		SELECT DISTINCT r.receipt_ref, r.disposition
		  FROM upstream u
		  JOIN containers c ON c.id = u.container_id
		  JOIN receipts r ON r.id = c.receipt_id
		 WHERE r.disposition <> 'released'`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref, disposition string
		if err := rows.Scan(&ref, &disposition); err != nil {
			return nil, err
		}
		refs = append(refs, ref+"("+disposition+")")
	}
	return refs, rows.Err()
}

func joinRefs(refs []string) string {
	out := ""
	for i, ref := range refs {
		if i > 0 {
			out += ", "
		}
		out += ref
	}
	return out
}

func isTriggerError(err error, message string) bool {
	return err != nil && strings.Contains(err.Error(), message)
}
