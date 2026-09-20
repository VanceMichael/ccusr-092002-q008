package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// 本文件提供可在事务内使用的查询，供领域服务在一个事务里完成全部裁决与写入。

func CompoundBatchByRefIn(ctx context.Context, q querier, ref string) (CompoundBatch, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+compoundBatchColumns+` FROM compound_batches WHERE batch_ref = ?`, ref)
	return scanCompoundBatch(row)
}

func CompoundBatchIn(ctx context.Context, q querier, id string) (CompoundBatch, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+compoundBatchColumns+` FROM compound_batches WHERE id = ?`, id)
	return scanCompoundBatch(row)
}

const compoundBatchColumns = `id, batch_ref, formula_ref, formula_revision, product_name,
    production_manager_id, status, quality_blocked, COALESCE(quality_blocked_reason,''), created_at`

func scanCompoundBatch(row rowScanner) (CompoundBatch, error) {
	var batch CompoundBatch
	var blocked int
	var createdAt string
	if err := row.Scan(&batch.ID, &batch.BatchRef, &batch.FormulaRef, &batch.FormulaRevision,
		&batch.ProductName, &batch.ProductionManager, &batch.Status, &blocked,
		&batch.QualityBlockReason, &createdAt); err != nil {
		return CompoundBatch{}, mapExecError(err)
	}
	batch.QualityBlocked = blocked == 1
	batch.CreatedAt = parseTime(createdAt)
	return batch, nil
}

// LotTotalsIn 返回事务视角下某批次的入、出量合计。
func LotTotalsIn(ctx context.Context, q querier, lotID string) (inSum, outSum int64, err error) {
	row := q.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(CASE WHEN direction='in' THEN weight_mg END),0),
		    COALESCE(SUM(CASE WHEN direction='out' THEN weight_mg END),0)
		 FROM lot_movement_items WHERE material_lot_id = ?`, lotID)
	err = row.Scan(&inSum, &outSum)
	return
}

// MaxReadingSeqIn 返回事务视角下检验单的最大读数序号。
func MaxReadingSeqIn(ctx context.Context, q querier, inspectionID string) (int, error) {
	row := q.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0) FROM test_readings WHERE inspection_id = ?`, inspectionID)
	var seq int
	return seq, row.Scan(&seq)
}

func ReadingsIn(ctx context.Context, q querier, inspectionID string) ([]Reading, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, inspection_id, seq, test_item, COALESCE(spec_min,''), COALESCE(spec_max,''),
		        COALESCE(unit,''), observed_value, outcome, measured_at, inspector_id, COALESCE(remark,'')
		 FROM test_readings WHERE inspection_id=? ORDER BY seq`, inspectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var readings []Reading
	for rows.Next() {
		var reading Reading
		var measuredAt string
		if err := rows.Scan(&reading.ID, &reading.InspectionID, &reading.Seq, &reading.TestItem,
			&reading.SpecMin, &reading.SpecMax, &reading.Unit, &reading.ObservedValue,
			&reading.Outcome, &measuredAt, &reading.InspectorID, &reading.Remark); err != nil {
			return nil, err
		}
		reading.MeasuredAt = parseTime(measuredAt)
		readings = append(readings, reading)
	}
	return readings, rows.Err()
}

func InspectionIn(ctx context.Context, q querier, id string) (Inspection, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, inspection_ref, sample_id, opened_at, opened_by,
		        COALESCE(concluded_at,''), COALESCE(verdict,''), COALESCE(concluded_by,''), COALESCE(remark,'')
		 FROM inspections WHERE id = ?`, id)
	return scanInspection(row)
}

// DescendantLotsIn 可在事务内向下追踪派生批次。
func DescendantLotsIn(ctx context.Context, q querier, lotID string) ([]string, error) {
	return traverseLots(ctx, q, lotID, true)
}

// AncestorLotsIn 可在事务内向上追溯来源批次。
func AncestorLotsIn(ctx context.Context, q querier, lotID string) ([]string, error) {
	return traverseLots(ctx, q, lotID, false)
}

func traverseLots(ctx context.Context, q querier, lotID string, downward bool) ([]string, error) {
	var query string
	if downward {
		query = `
		WITH RECURSIVE walk(id) AS (
			SELECT ?
			UNION
			SELECT in_items.material_lot_id
			FROM walk
			JOIN lot_movement_items out_items
			  ON out_items.material_lot_id = walk.id AND out_items.direction='out'
			JOIN lot_movements m
			  ON m.id = out_items.movement_id AND m.movement_type IN ('split','merge')
			JOIN lot_movement_items in_items
			  ON in_items.movement_id = m.id AND in_items.direction='in'
		)
		SELECT id FROM walk`
	} else {
		query = `
		WITH RECURSIVE walk(id) AS (
			SELECT ?
			UNION
			SELECT out_items.material_lot_id
			FROM walk
			JOIN lot_movement_items in_items
			  ON in_items.material_lot_id = walk.id AND in_items.direction='in'
			JOIN lot_movements m
			  ON m.id = in_items.movement_id AND m.movement_type IN ('split','merge')
			JOIN lot_movement_items out_items
			  ON out_items.movement_id = m.id AND out_items.direction='out'
		)
		SELECT id FROM walk`
	}
	rows, err := q.QueryContext(ctx, query, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectIDs(rows)
}

func BatchesUsingLotsIn(ctx context.Context, q querier, lotIDs []string) ([]CompoundBatch, error) {
	if len(lotIDs) == 0 {
		return nil, nil
	}
	placeholders := repeatPlaceholders(len(lotIDs))
	query := `
		SELECT DISTINCT b.id, b.batch_ref, b.formula_ref, b.formula_revision,
		       b.product_name, b.production_manager_id, b.status,
		       b.quality_blocked, COALESCE(b.quality_blocked_reason,''), b.created_at
		FROM compound_batches b
		JOIN chargings c ON c.compound_batch_id = b.id
		WHERE c.material_lot_id IN (` + placeholders + `)`
	args := make([]any, len(lotIDs))
	for i, id := range lotIDs {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batches []CompoundBatch
	for rows.Next() {
		batch, err := scanCompoundBatch(rows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

// SetBatchBlocked 标记配制批次被不合格原料阻断。
func SetBatchBlocked(ctx context.Context, q querier, batchID string, reason string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE compound_batches
		 SET quality_blocked=1, quality_blocked_reason=?,
		     status=CASE WHEN status='released' THEN 'recalled' ELSE status END
		 WHERE id=?`, reason, batchID)
	return err
}

// StaffIn 在事务内读取员工。
func StaffIn(ctx context.Context, q querier, id string) (Staff, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, name, role, active, created_at FROM staff WHERE id = ?`, id)
	var staff Staff
	var active int
	var createdAt string
	if err := row.Scan(&staff.ID, &staff.Name, &staff.Role, &active, &createdAt); err != nil {
		return Staff{}, mapExecError(err)
	}
	staff.Active = active == 1
	staff.CreatedAt = parseTime(createdAt)
	return staff, nil
}

// MaterialByCodeIn 在事务内按编码读取药材。
func MaterialByCodeIn(ctx context.Context, q querier, code string) (Material, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, code, name FROM materials WHERE code = ?`, code)
	var material Material
	if err := row.Scan(&material.ID, &material.Code, &material.Name); err != nil {
		return Material{}, mapExecError(err)
	}
	return material, nil
}

// SupplierIn 在事务内读取供应商。
func SupplierIn(ctx context.Context, q querier, id string) (Supplier, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, name, origin, license_scope, created_at, created_by
		 FROM suppliers WHERE id = ?`, id)
	var supplier Supplier
	var createdAt string
	if err := row.Scan(&supplier.ID, &supplier.Name, &supplier.Origin,
		&supplier.LicenseScope, &createdAt, &supplier.CreatedBy); err != nil {
		return Supplier{}, mapExecError(err)
	}
	supplier.CreatedAt = parseTime(createdAt)
	return supplier, nil
}

// SampleByRefIn 在事务内按来源编号读取抽样。
func SampleByRefIn(ctx context.Context, q querier, ref string) (Sample, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, sample_ref, subject_type, subject_id, kind,
		        COALESCE(retest_of_sample_id,''), qty_mg, sampled_at, sampler_id
		 FROM samples WHERE sample_ref = ?`, ref)
	return scanSample(row)
}

// SampleIn 在事务内按 ID 读取抽样。
func SampleIn(ctx context.Context, q querier, id string) (Sample, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, sample_ref, subject_type, subject_id, kind,
		        COALESCE(retest_of_sample_id,''), qty_mg, sampled_at, sampler_id
		 FROM samples WHERE id = ?`, id)
	return scanSample(row)
}

// InspectionByRefIn 在事务内按来源编号读取检验单（含读数）。
func InspectionByRefIn(ctx context.Context, q querier, ref string) (Inspection, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, inspection_ref, sample_id, opened_at, opened_by,
		        COALESCE(concluded_at,''), COALESCE(verdict,''), COALESCE(concluded_by,''), COALESCE(remark,'')
		 FROM inspections WHERE inspection_ref = ?`, ref)
	inspection, err := scanInspection(row)
	if err != nil {
		return Inspection{}, err
	}
	inspection.Readings, err = ReadingsIn(ctx, q, inspection.ID)
	if err != nil {
		return Inspection{}, err
	}
	return inspection, nil
}

// ConcludeInspectionIn 在事务内给检验单一锤定音地写入结论。
func ConcludeInspectionIn(ctx context.Context, q querier, inspectionID, verdict, concludedBy string, concludedAt time.Time) error {
	result, err := q.ExecContext(ctx,
		`UPDATE inspections SET verdict=?, concluded_by=?, concluded_at=?
		 WHERE id=? AND COALESCE(verdict,'')=''`,
		verdict, concludedBy, nowISO(concludedAt), inspectionID)
	if err != nil {
		return mapExecError(err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: 检验单已结论或不存在", ErrConflict)
	}
	return nil
}

func SetLotStateIn(ctx context.Context, q querier, lotID, state string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE material_lots SET state=? WHERE id=?`, state, lotID)
	return err
}

// MarkLotFailedIn 把批次标记为出现过不合格（永久事实，不可清除）并挂起。
func MarkLotFailedIn(ctx context.Context, q querier, lotID string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE material_lots SET had_failure=1,
		    state=CASE WHEN state IN ('rejected','disposed') THEN state ELSE 'held' END
		 WHERE id=?`, lotID)
	return err
}

// SamplesBySubjectIn 在事务内列出某对象的全部抽样。
func SamplesBySubjectIn(ctx context.Context, q querier, subjectType, subjectID string) ([]Sample, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, sample_ref, subject_type, subject_id, kind,
		        COALESCE(retest_of_sample_id,''), qty_mg, sampled_at, sampler_id
		 FROM samples WHERE subject_type=? AND subject_id=? ORDER BY sampled_at`,
		subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []Sample
	for rows.Next() {
		sample, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

// LatestFinishedInspection 返回某对象最新一份已结论检验单。
func LatestFinishedInspection(ctx context.Context, q querier, subjectType, subjectID string) (Inspection, error) {
	row := q.QueryRowContext(ctx,
		`SELECT i.id, i.inspection_ref, i.sample_id, i.opened_at, i.opened_by,
		        COALESCE(i.concluded_at,''), COALESCE(i.verdict,''), COALESCE(i.concluded_by,''), COALESCE(i.remark,'')
		 FROM inspections i
		 JOIN samples smp ON smp.id = i.sample_id
		 WHERE smp.subject_type=? AND smp.subject_id=? AND COALESCE(i.verdict,'')<>''
		 ORDER BY i.concluded_at DESC, i.id DESC LIMIT 1`, subjectType, subjectID)
	return scanInspection(row)
}

// InsertMovementIn 在事务内登记一笔台账动作及其明细。
func InsertMovementIn(ctx context.Context, q querier, movement Movement, items []MovementItem) error {
	if err := insertMovement(ctx, q, movement); err != nil {
		return err
	}
	for _, item := range items {
		item.MovementID = movement.ID
		if err := insertMovementItem(ctx, q, item); err != nil {
			return err
		}
	}
	return nil
}

// InsertSampleIn 在事务内登记抽样。
func InsertSampleIn(ctx context.Context, q querier, sample Sample) error {
	return insertSample(ctx, q, sample)
}

// InsertInspectionIn 在事务内开启检验单。
func InsertInspectionIn(ctx context.Context, q querier, inspection Inspection) error {
	return insertInspection(ctx, q, inspection)
}

// InsertReadingIn 在事务内追加检验读数。
func InsertReadingIn(ctx context.Context, q querier, reading Reading) error {
	return insertReading(ctx, q, reading)
}

// InsertWeighingIn 在事务内登记称量。
func InsertWeighingIn(ctx context.Context, q querier, weighing Weighing) error {
	return insertWeighing(ctx, q, weighing)
}

// InsertChargingIn 在事务内登记投料。
func InsertChargingIn(ctx context.Context, q querier, charging Charging) error {
	return insertCharging(ctx, q, charging)
}

// InsertReleaseDecisionIn 在事务内登记放行决策。
func InsertReleaseDecisionIn(ctx context.Context, q querier, decision ReleaseDecision) error {
	return insertReleaseDecision(ctx, q, decision)
}

// UnchargedWeighingsIn 在事务内列出尚未投料的称量。
func UnchargedWeighingsIn(ctx context.Context, q querier, batchID string) ([]Weighing, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT w.id, w.compound_batch_id, w.material_lot_id, w.movement_item_id, w.weight_mg, w.weighed_at
		 FROM weighings w
		 LEFT JOIN chargings c ON c.weighing_id = w.id
		 WHERE w.compound_batch_id=? AND c.id IS NULL
		 ORDER BY w.weighed_at`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var weighings []Weighing
	for rows.Next() {
		var weighing Weighing
		var weighedAt string
		if err := rows.Scan(&weighing.ID, &weighing.CompoundBatchID, &weighing.MaterialLotID,
			&weighing.MovementItemID, &weighing.WeightMg, &weighedAt); err != nil {
			return nil, err
		}
		weighing.WeighedAt = parseTime(weighedAt)
		weighings = append(weighings, weighing)
	}
	return weighings, rows.Err()
}

// UpdateCompoundStatusIn 在事务内更新配制批次状态。
func UpdateCompoundStatusIn(ctx context.Context, q querier, batchID, status string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE compound_batches SET status=? WHERE id=?`, status, batchID)
	return err
}

// InsertDisposalIn 在事务内登记处置。
func InsertDisposalIn(ctx context.Context, q querier, disposal Disposal) error {
	return insertDisposal(ctx, q, disposal)
}

// InsertInboundLotIn 在事务内登记入厂批次。
func InsertInboundLotIn(ctx context.Context, q querier, in InboundLot) error {
	return insertInboundLot(ctx, q, in)
}

// InsertMaterialLotIn 在事务内登记物料批次节点。
func InsertMaterialLotIn(ctx context.Context, q querier, lot MaterialLot) error {
	return insertMaterialLot(ctx, q, lot)
}

// InsertCompoundBatchIn 在事务内登记配制批次。
func InsertCompoundBatchIn(ctx context.Context, q querier, batch CompoundBatch) error {
	return insertCompoundBatch(ctx, q, batch)
}

// InsertSupplierIn 在事务内登记供应商。
func InsertSupplierIn(ctx context.Context, q querier, supplier Supplier) error {
	return insertSupplier(ctx, q, supplier)
}

// InsertCertificateIn 在事务内登记供应商凭证。
func InsertCertificateIn(ctx context.Context, q querier, cert Certificate) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO supplier_certificates
		    (id, supplier_id, cert_type, cert_ref, license_scope, issued_at, expires_at, recorded_at, recorded_by)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		cert.ID, cert.SupplierID, cert.CertType, cert.CertRef, cert.LicenseScope,
		nullableTime(cert.IssuedAt), nullableTime(cert.ExpiresAt),
		nowISO(cert.RecordedAt), cert.RecordedBy)
	return mapExecError(err)
}

// CertificatesBySupplierIn 在事务内列出某供应商的凭证。
func CertificatesBySupplierIn(ctx context.Context, q querier, supplierID string) ([]Certificate, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, supplier_id, cert_type, cert_ref, license_scope, issued_at, expires_at, recorded_at, recorded_by
		 FROM supplier_certificates WHERE supplier_id = ? ORDER BY recorded_at`, supplierID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var certs []Certificate
	for rows.Next() {
		cert, err := scanCertificate(rows)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	return certs, rows.Err()
}

// InboundLotIn 在事务内按 ID 读取入厂批次。
func InboundLotIn(ctx context.Context, q querier, id string) (InboundLot, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, lot_ref, supplier_id, material_id, net_weight_mg, received_at, receiver_id, created_at
		 FROM inbound_lots WHERE id = ?`, id)
	return scanInbound(row)
}

// InspectionsBySubjectIn 列出某对象的全部检验单（含读数），按开立时间排序。
func InspectionsBySubjectIn(ctx context.Context, q querier, subjectType, subjectID string) ([]Inspection, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT i.id, i.inspection_ref, i.sample_id, i.opened_at, i.opened_by,
		        COALESCE(i.concluded_at,''), COALESCE(i.verdict,''), COALESCE(i.concluded_by,''), COALESCE(i.remark,'')
		 FROM inspections i
		 JOIN samples smp ON smp.id = i.sample_id
		 WHERE smp.subject_type=? AND smp.subject_id=?
		 ORDER BY i.opened_at, i.id`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inspections []Inspection
	for rows.Next() {
		inspection, err := scanInspection(rows)
		if err != nil {
			return nil, err
		}
		inspection.Readings, err = ReadingsIn(ctx, q, inspection.ID)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, inspection)
	}
	return inspections, rows.Err()
}

// ChargingsByBatchIn 列出配制批次的投料记录。
func ChargingsByBatchIn(ctx context.Context, q querier, batchID string) ([]Charging, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, compound_batch_id, material_lot_id, weighing_id, weight_mg, charged_at, charged_by
		 FROM chargings WHERE compound_batch_id=? ORDER BY charged_at, id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chargings []Charging
	for rows.Next() {
		var charging Charging
		var chargedAt string
		if err := rows.Scan(&charging.ID, &charging.CompoundBatchID, &charging.MaterialLotID,
			&charging.WeighingID, &charging.WeightMg, &chargedAt, &charging.ChargedBy); err != nil {
			return nil, err
		}
		charging.ChargedAt = parseTime(chargedAt)
		chargings = append(chargings, charging)
	}
	return chargings, rows.Err()
}

// ReleaseDecisionsIn 列出配制批次的全部放行决策（逐笔留痕）。
func ReleaseDecisionsIn(ctx context.Context, q querier, batchID string) ([]ReleaseDecision, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, compound_batch_id, decision, COALESCE(finished_inspection_id,''),
		        COALESCE(reason,''), decided_at, decided_by
		 FROM release_decisions WHERE compound_batch_id=? ORDER BY decided_at, id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []ReleaseDecision
	for rows.Next() {
		var decision ReleaseDecision
		var decidedAt string
		if err := rows.Scan(&decision.ID, &decision.CompoundBatchID, &decision.Decision,
			&decision.FinishedInspectionID, &decision.Reason, &decidedAt, &decision.DecidedBy); err != nil {
			return nil, err
		}
		decision.DecidedAt = parseTime(decidedAt)
		decisions = append(decisions, decision)
	}
	return decisions, rows.Err()
}

// DisposalsByLotIn 列出物料批次的处置记录。
func DisposalsByLotIn(ctx context.Context, q querier, lotID string) ([]Disposal, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, material_lot_id, disposition_type, disposed_at, disposed_by, COALESCE(remark,'')
		 FROM lot_disposals WHERE material_lot_id=? ORDER BY disposed_at, id`, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var disposals []Disposal
	for rows.Next() {
		var disposal Disposal
		var disposedAt string
		if err := rows.Scan(&disposal.ID, &disposal.MaterialLotID, &disposal.DispositionType,
			&disposedAt, &disposal.DisposedBy, &disposal.Remark); err != nil {
			return nil, err
		}
		disposal.DisposedAt = parseTime(disposedAt)
		disposals = append(disposals, disposal)
	}
	return disposals, rows.Err()
}

// LedgerEntriesIn 在事务内返回某物料批次按时间排列的逐笔重量台账。
func LedgerEntriesIn(ctx context.Context, q querier, lotID string) ([]LedgerEntry, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT m.id, m.movement_type, COALESCE(m.ref,''), i.direction, i.weight_mg,
		        m.created_at, m.created_by, COALESCE(m.remark,'')
		 FROM lot_movement_items i
		 JOIN lot_movements m ON m.id = i.movement_id
		 WHERE i.material_lot_id = ?
		 ORDER BY m.created_at, m.id`, lotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []LedgerEntry
	for rows.Next() {
		var entry LedgerEntry
		var createdAt string
		if err := rows.Scan(&entry.MovementID, &entry.MovementType, &entry.Ref,
			&entry.Direction, &entry.WeightMg, &createdAt, &entry.CreatedBy, &entry.Remark); err != nil {
			return nil, err
		}
		entry.CreatedAt = parseTime(createdAt)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// MaterialIn 在事务内按 ID 读取药材品种。
func MaterialIn(ctx context.Context, q querier, id string) (Material, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, code, name FROM materials WHERE id = ?`, id)
	var material Material
	if err := row.Scan(&material.ID, &material.Code, &material.Name); err != nil {
		return Material{}, mapExecError(err)
	}
	return material, nil
}

// MovementEdge 表示一次分装/合批中某物料批次在某方向上的重量关系。
type MovementEdge struct {
	MovementID    string
	MovementType  string
	Ref           string
	Remark        string
	CreatedAt     time.Time
	CreatedBy     string
	MaterialLotID string
	Direction     string
	WeightMg      int64
}

// MovementsAmongLotsIn 返回与给定物料批次集合相关的 split/merge 移动明细（含重量）。
func MovementsAmongLotsIn(ctx context.Context, q querier, lotIDs []string) ([]MovementEdge, error) {
	if len(lotIDs) == 0 {
		return nil, nil
	}
	query := `
		SELECT m.id, m.movement_type, COALESCE(m.ref,''), COALESCE(m.remark,''),
		       m.created_at, m.created_by, i.material_lot_id, i.direction, i.weight_mg
		FROM lot_movements m
		JOIN lot_movement_items i ON i.movement_id = m.id
		WHERE m.movement_type IN ('split','merge')
		  AND m.id IN (
		      SELECT movement_id FROM lot_movement_items
		      WHERE material_lot_id IN (` + repeatPlaceholders(len(lotIDs)) + `)
		  )
		ORDER BY m.created_at, m.id, CASE i.direction WHEN 'out' THEN 0 ELSE 1 END`
	args := make([]any, len(lotIDs))
	for i, id := range lotIDs {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var edges []MovementEdge
	for rows.Next() {
		var edge MovementEdge
		var createdAt string
		if err := rows.Scan(&edge.MovementID, &edge.MovementType, &edge.Ref, &edge.Remark,
			&createdAt, &edge.CreatedBy, &edge.MaterialLotID, &edge.Direction, &edge.WeightMg); err != nil {
			return nil, err
		}
		edge.CreatedAt = parseTime(createdAt)
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func repeatPlaceholders(n int) string {
	out := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

func collectIDs(rows *sql.Rows) ([]string, error) {
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
