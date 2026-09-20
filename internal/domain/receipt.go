package domain

import (
	"context"
	"database/sql"
	"errors"
)

// RegisterReceipt 登记来货批次的供应商凭证、产地与来货重量。
// 初始处置状态为 pending，未经质量判定不得分装使用。
func (s *Service) RegisterReceipt(ctx context.Context, callerID string, in ReceiptInput) (string, error) {
	if in.ReceiptRef == "" || in.MaterialCode == "" || in.Supplier == "" || in.SupplierVoucher == "" {
		return "", fail(400, "invalid_input", "来货批号、药材编码、供应商、凭证编号必填")
	}
	if in.ReceivedWeight <= 0 {
		return "", fail(400, "invalid_weight", "来货重量必须为正数")
	}
	receivedAt := in.ReceivedAt
	if receivedAt == "" {
		receivedAt = nowISO()
	}
	var receiptID string
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
		receiptID = newID()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO receipts
			   (id, receipt_ref, material_id, supplier, supplier_voucher, origin, received_weight, received_at, recorded_by, disposition)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
			receiptID, in.ReceiptRef, materialID, in.Supplier, in.SupplierVoucher,
			in.Origin, in.ReceivedWeight, receivedAt, actor.ID)
		return mapUnique(err, "来货批号已存在")
	})
	return receiptID, err
}

// TakeSample 对来货批次抽样。一次来货可多次抽样，每次抽样后可录入多轮检验。
func (s *Service) TakeSample(ctx context.Context, callerID string, receiptRef string, in SampleInput) (string, error) {
	if in.SampleRef == "" {
		return "", fail(400, "invalid_input", "sample_ref 必填")
	}
	var sampleID string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleInspector)
		if err != nil {
			return err
		}
		var receiptID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM receipts WHERE receipt_ref = ?`, receiptRef).
			Scan(&receiptID)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "receipt_not_found", "来货批次不存在")
		}
		if err != nil {
			return err
		}
		sampleID = newID()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO samples (id, receipt_id, sample_ref, taken_at, taken_by, note)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			sampleID, receiptID, in.SampleRef, nowISO(), actor.ID, in.Note)
		return mapUnique(err, "抽样编号已存在")
	})
	return sampleID, err
}

// RecordInspection 录入一轮检验读数。合格判定由系统按药材合格范围计算，调用方无法自行声明合格。
// 同一抽样第二轮起自动标记为复检（is_retest=1），新轮次永不覆盖旧读数。
func (s *Service) RecordInspection(ctx context.Context, callerID, sampleRef string, in InspectionInput) (*InspectionRoundView, error) {
	view := &InspectionRoundView{Measured: in.Measured, Method: in.Method, Note: in.Note}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleInspector)
		if err != nil {
			return err
		}
		var sampleID, materialID string
		err = tx.QueryRowContext(ctx,
			`SELECT s.id, r.material_id FROM samples s JOIN receipts r ON r.id = s.receipt_id
			 WHERE s.sample_ref = ?`, sampleRef).
			Scan(&sampleID, &materialID)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "sample_not_found", "抽样记录不存在")
		}
		if err != nil {
			return err
		}
		var specMin, specMax float64
		var specUnit string
		if err := tx.QueryRowContext(ctx,
			`SELECT spec_min, spec_max, spec_unit FROM materials WHERE id = ?`, materialID).
			Scan(&specMin, &specMax, &specUnit); err != nil {
			return err
		}
		var maxRound int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(round_no), 0) FROM inspections WHERE sample_id = ?`, sampleID).
			Scan(&maxRound); err != nil {
			return err
		}
		round := maxRound + 1
		outcome := "pass"
		if in.Measured < specMin || in.Measured > specMax {
			outcome = "fail"
		}
		view.RoundNo, view.Outcome, view.IsRetest, view.Unit = round, outcome, round > 1, specUnit
		view.InspectedBy, view.InspectedAt = actor.Name, nowISO()
		// 触发器保证旧行不可改：首败读数物理留存。
		_, err = tx.ExecContext(ctx,
			`INSERT INTO inspections
			   (id, sample_id, round_no, measured, unit, outcome, is_retest, method, inspected_by, inspected_at, note)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID(), sampleID, round, in.Measured, specUnit, outcome, b2i(round > 1),
			in.Method, actor.ID, view.InspectedAt, in.Note)
		return mapUnique(err, "该轮次检验已存在")
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// DecideReceipt 由药事负责人对来货批次作处置判定。
// 规则：只要存在任何一轮失败读数（含已被复检“翻盘”的首检失败），即不得放行；
// 且必须至少有一轮合格读数才可放行。
func (s *Service) DecideReceipt(ctx context.Context, callerID, receiptRef, target, note string) error {
	if target != "released" && target != "rejected" && target != "quarantined" {
		return fail(400, "invalid_decision", "处置状态只能是 released/rejected/quarantined")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		actor, err := s.authenticate(ctx, tx, callerID, RoleQARelease)
		if err != nil {
			return err
		}
		var receiptID, current string
		err = tx.QueryRowContext(ctx, `SELECT id, disposition FROM receipts WHERE receipt_ref = ?`, receiptRef).
			Scan(&receiptID, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "receipt_not_found", "来货批次不存在")
		}
		if err != nil {
			return err
		}
		var totalRounds, failRounds, passRounds int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*),
			        SUM(CASE WHEN i.outcome = 'fail' THEN 1 ELSE 0 END),
			        SUM(CASE WHEN i.outcome = 'pass' THEN 1 ELSE 0 END)
			 FROM inspections i
			 JOIN samples sm ON sm.id = i.sample_id
			 WHERE sm.receipt_id = ?`, receiptID).
			Scan(&totalRounds, &failRounds, &passRounds); err != nil {
			return err
		}
		if target == "released" {
			switch {
			case totalRounds == 0:
				return fail(422, "no_inspection", "尚无任何检验读数，不能放行")
			case failRounds > 0:
				return fail(422, "failed_reading_present",
					"存在失败读数（含首检失败），复检不得覆盖首败，该来货批次不能放行")
			case passRounds == 0:
				return fail(422, "no_passing_reading", "没有合格读数，不能放行")
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE receipts SET disposition = ?, disposition_note = ? WHERE id = ?`,
			target, note, receiptID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO receipt_decisions (id, receipt_id, from_state, to_state, decided_by, decided_at, note)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			newID(), receiptID, current, target, actor.ID, nowISO(), note)
		return err
	})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
