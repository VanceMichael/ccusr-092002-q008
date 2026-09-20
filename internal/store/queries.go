package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// querier 让同一组查询既能在普通连接上运行，也能在事务内运行。
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ---------------------------------------------------------------------------
// 基础资料
// ---------------------------------------------------------------------------

func (s *Store) CreateStaff(ctx context.Context, staff Staff) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO staff(id, name, role, active, created_at) VALUES (?,?,?,?,?)`,
		staff.ID, staff.Name, staff.Role, boolInt(staff.Active), nowISO(staff.CreatedAt))
	return mapExecError(err)
}

func (s *Store) GetStaff(ctx context.Context, id string) (Staff, error) {
	row := s.DB.QueryRowContext(ctx,
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

func (s *Store) CreateMaterial(ctx context.Context, material Material) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO materials(id, code, name) VALUES (?,?,?)`,
		material.ID, material.Code, material.Name)
	return mapExecError(err)
}

func (s *Store) GetMaterialByCode(ctx context.Context, code string) (Material, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, code, name FROM materials WHERE code = ?`, code)
	var material Material
	if err := row.Scan(&material.ID, &material.Code, &material.Name); err != nil {
		return Material{}, mapExecError(err)
	}
	return material, nil
}

func (s *Store) CreateSupplier(ctx context.Context, supplier Supplier) error {
	return mapExecError(insertSupplier(ctx, s.DB, supplier))
}

func insertSupplier(ctx context.Context, q querier, supplier Supplier) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO suppliers(id, name, origin, license_scope, created_at, created_by)
		 VALUES (?,?,?,?,?,?)`,
		supplier.ID, supplier.Name, supplier.Origin, supplier.LicenseScope,
		nowISO(supplier.CreatedAt), supplier.CreatedBy)
	return mapExecError(err)
}

func (s *Store) GetSupplier(ctx context.Context, id string) (Supplier, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, name, origin, license_scope, created_at, created_by FROM suppliers WHERE id = ?`, id)
	var supplier Supplier
	var createdAt string
	if err := row.Scan(&supplier.ID, &supplier.Name, &supplier.Origin,
		&supplier.LicenseScope, &createdAt, &supplier.CreatedBy); err != nil {
		return Supplier{}, mapExecError(err)
	}
	supplier.CreatedAt = parseTime(createdAt)
	return supplier, nil
}

func (s *Store) AddCertificate(ctx context.Context, cert Certificate) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO supplier_certificates
		    (id, supplier_id, cert_type, cert_ref, license_scope, issued_at, expires_at, recorded_at, recorded_by)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		cert.ID, cert.SupplierID, cert.CertType, cert.CertRef, cert.LicenseScope,
		nullableTime(cert.IssuedAt), nullableTime(cert.ExpiresAt),
		nowISO(cert.RecordedAt), cert.RecordedBy)
	return mapExecError(err)
}

func (s *Store) ListCertificates(ctx context.Context, supplierID string) ([]Certificate, error) {
	rows, err := s.DB.QueryContext(ctx,
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

func scanCertificate(rows *sql.Rows) (Certificate, error) {
	var cert Certificate
	var issuedAt, expiresAt, recordedAt sql.NullString
	if err := rows.Scan(&cert.ID, &cert.SupplierID, &cert.CertType, &cert.CertRef,
		&cert.LicenseScope, &issuedAt, &expiresAt, &recordedAt, &cert.RecordedBy); err != nil {
		return Certificate{}, err
	}
	cert.IssuedAt = parseNullTime(issuedAt)
	cert.ExpiresAt = parseNullTime(expiresAt)
	cert.RecordedAt = parseTime(recordedAt.String)
	return cert, nil
}

// ---------------------------------------------------------------------------
// 入厂批次与物料批次
// ---------------------------------------------------------------------------

func insertInboundLot(ctx context.Context, q querier, in InboundLot) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO inbound_lots
		    (id, lot_ref, supplier_id, material_id, net_weight_mg, received_at, receiver_id, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		in.ID, in.LotRef, in.SupplierID, in.MaterialID, in.NetWeightMg,
		nowISO(in.ReceivedAt), in.ReceiverID, nowISO(in.CreatedAt))
	return mapExecError(err)
}

func (s *Store) GetInboundLot(ctx context.Context, id string) (InboundLot, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, lot_ref, supplier_id, material_id, net_weight_mg, received_at, receiver_id, created_at
		 FROM inbound_lots WHERE id = ?`, id)
	return scanInbound(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInbound(row rowScanner) (InboundLot, error) {
	var in InboundLot
	var receivedAt, createdAt string
	if err := row.Scan(&in.ID, &in.LotRef, &in.SupplierID, &in.MaterialID,
		&in.NetWeightMg, &receivedAt, &in.ReceiverID, &createdAt); err != nil {
		return InboundLot{}, mapExecError(err)
	}
	in.ReceivedAt = parseTime(receivedAt)
	in.CreatedAt = parseTime(createdAt)
	return in, nil
}

func insertMaterialLot(ctx context.Context, q querier, lot MaterialLot) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO material_lots
		    (id, lot_ref, material_id, root_inbound_lot_id, initial_weight_mg,
		     state, had_failure, created_at, created_by)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		lot.ID, lot.LotRef, lot.MaterialID, nullableString(lot.RootInboundLotID),
		lot.InitialWeightMg, lot.State, boolInt(lot.HadFailure),
		nowISO(lot.CreatedAt), lot.CreatedBy)
	return mapExecError(err)
}

func (s *Store) GetMaterialLot(ctx context.Context, id string) (MaterialLot, error) {
	return MaterialLotIn(ctx, s.DB, id)
}

func (s *Store) GetMaterialLotByRef(ctx context.Context, ref string) (MaterialLot, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+materialLotColumns+` FROM material_lots WHERE lot_ref = ?`, ref)
	return scanMaterialLot(row)
}

const materialLotColumns = `id, lot_ref, material_id, COALESCE(root_inbound_lot_id,''),
    initial_weight_mg, state, had_failure, created_at, created_by`

// MaterialLotIn 可在事务内读取物料批次。
func MaterialLotIn(ctx context.Context, q querier, id string) (MaterialLot, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+materialLotColumns+` FROM material_lots WHERE id = ?`, id)
	return scanMaterialLot(row)
}

// MaterialLotByRefIn 可在事务内按来源编号读取物料批次。
func MaterialLotByRefIn(ctx context.Context, q querier, ref string) (MaterialLot, error) {
	row := q.QueryRowContext(ctx,
		`SELECT `+materialLotColumns+` FROM material_lots WHERE lot_ref = ?`, ref)
	return scanMaterialLot(row)
}

func scanMaterialLot(row rowScanner) (MaterialLot, error) {
	var lot MaterialLot
	var hadFailure int
	var createdAt string
	if err := row.Scan(&lot.ID, &lot.LotRef, &lot.MaterialID, &lot.RootInboundLotID,
		&lot.InitialWeightMg, &lot.State, &hadFailure, &createdAt, &lot.CreatedBy); err != nil {
		return MaterialLot{}, mapExecError(err)
	}
	lot.HadFailure = hadFailure == 1
	lot.CreatedAt = parseTime(createdAt)
	return lot, nil
}

// ---------------------------------------------------------------------------
// 重量台账
// ---------------------------------------------------------------------------

type Movement struct {
	ID           string
	MovementType string
	Ref          string
	CreatedAt    time.Time
	CreatedBy    string
	Remark       string
}

type MovementItem struct {
	ID            string
	MovementID    string
	MaterialLotID string
	Direction     string
	WeightMg      int64
}

func insertMovement(ctx context.Context, q querier, m Movement) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO lot_movements(id, movement_type, ref, created_at, created_by, remark)
		 VALUES (?,?,?,?,?,?)`,
		m.ID, m.MovementType, nullableString(m.Ref), nowISO(m.CreatedAt), m.CreatedBy,
		nullableString(m.Remark))
	return mapExecError(err)
}

func insertMovementItem(ctx context.Context, q querier, item MovementItem) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO lot_movement_items(id, movement_id, material_lot_id, direction, weight_mg)
		 VALUES (?,?,?,?,?)`,
		item.ID, item.MovementID, item.MaterialLotID, item.Direction, item.WeightMg)
	return mapExecError(err)
}

// LotTotals 返回某物料批次所有台账动作的入量、出量合计。
func (s *Store) LotTotals(ctx context.Context, lotID string) (inSum, outSum int64, err error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(CASE WHEN direction='in' THEN weight_mg END),0),
		    COALESCE(SUM(CASE WHEN direction='out' THEN weight_mg END),0)
		 FROM lot_movement_items WHERE material_lot_id = ?`, lotID)
	err = row.Scan(&inSum, &outSum)
	return inSum, outSum, err
}

type LedgerEntry struct {
	MovementID   string
	MovementType string
	Ref          string
	Direction    string
	WeightMg     int64
	CreatedAt    time.Time
	CreatedBy    string
	Remark       string
}

// ListLedger 返回某物料批次按时间排列的逐笔重量台账。
func (s *Store) ListLedger(ctx context.Context, lotID string) ([]LedgerEntry, error) {
	rows, err := s.DB.QueryContext(ctx,
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

// MovementSums 用于守恒核对：某笔移动的入量、出量合计。
func (s *Store) MovementSums(ctx context.Context, movementID string) (inSum, outSum int64, err error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(CASE WHEN direction='in' THEN weight_mg END),0),
		    COALESCE(SUM(CASE WHEN direction='out' THEN weight_mg END),0)
		 FROM lot_movement_items WHERE movement_id = ?`, movementID)
	err = row.Scan(&inSum, &outSum)
	return
}

// ---------------------------------------------------------------------------
// 抽样与检验
// ---------------------------------------------------------------------------

func insertSample(ctx context.Context, q querier, sample Sample) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO samples(id, sample_ref, subject_type, subject_id, kind,
		    retest_of_sample_id, qty_mg, sampled_at, sampler_id)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		sample.ID, sample.SampleRef, sample.SubjectType, sample.SubjectID, sample.Kind,
		nullableString(sample.RetestOfSample), sample.QtyMg,
		nowISO(sample.SampledAt), sample.SamplerID)
	return mapExecError(err)
}

func (s *Store) GetSampleByRef(ctx context.Context, ref string) (Sample, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, sample_ref, subject_type, subject_id, kind,
		        COALESCE(retest_of_sample_id,''), qty_mg, sampled_at, sampler_id
		 FROM samples WHERE sample_ref = ?`, ref)
	return scanSample(row)
}

func scanSample(row rowScanner) (Sample, error) {
	var sample Sample
	var sampledAt string
	if err := row.Scan(&sample.ID, &sample.SampleRef, &sample.SubjectType, &sample.SubjectID,
		&sample.Kind, &sample.RetestOfSample, &sample.QtyMg, &sampledAt, &sample.SamplerID); err != nil {
		return Sample{}, mapExecError(err)
	}
	sample.SampledAt = parseTime(sampledAt)
	return sample, nil
}

func (s *Store) ListSamplesBySubject(ctx context.Context, subjectType, subjectID string) ([]Sample, error) {
	rows, err := s.DB.QueryContext(ctx,
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

func insertInspection(ctx context.Context, q querier, inspection Inspection) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO inspections(id, inspection_ref, sample_id, opened_at, opened_by,
		    concluded_at, verdict, concluded_by, remark)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		inspection.ID, inspection.InspectionRef, inspection.SampleID,
		nowISO(inspection.OpenedAt), inspection.OpenedBy,
		nil, nil, nil, nullableString(inspection.Remark))
	return mapExecError(err)
}

func (s *Store) GetInspectionByRef(ctx context.Context, ref string) (Inspection, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, inspection_ref, sample_id, opened_at, opened_by,
		        COALESCE(concluded_at,''), COALESCE(verdict,''), COALESCE(concluded_by,''), COALESCE(remark,'')
		 FROM inspections WHERE inspection_ref = ?`, ref)
	inspection, err := scanInspection(row)
	if err != nil {
		return Inspection{}, err
	}
	readings, err := s.listReadings(ctx, inspection.ID)
	if err != nil {
		return Inspection{}, err
	}
	inspection.Readings = readings
	return inspection, nil
}

func scanInspection(row rowScanner) (Inspection, error) {
	var inspection Inspection
	var openedAt, concludedAt string
	if err := row.Scan(&inspection.ID, &inspection.InspectionRef, &inspection.SampleID,
		&openedAt, &inspection.OpenedBy, &concludedAt, &inspection.Verdict,
		&inspection.ConcludedBy, &inspection.Remark); err != nil {
		return Inspection{}, mapExecError(err)
	}
	inspection.OpenedAt = parseTime(openedAt)
	if concludedAt != "" {
		ts := parseTime(concludedAt)
		inspection.ConcludedAt = &ts
	}
	return inspection, nil
}

// MaxReadingSeq 返回该检验单当前最大读数序号（追加模式）。
func (s *Store) MaxReadingSeq(ctx context.Context, inspectionID string) (int, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0) FROM test_readings WHERE inspection_id = ?`, inspectionID)
	var seq int
	return seq, row.Scan(&seq)
}

func insertReading(ctx context.Context, q querier, reading Reading) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO test_readings
		    (id, inspection_id, seq, test_item, spec_min, spec_max, unit,
		     observed_value, outcome, measured_at, inspector_id, remark)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		reading.ID, reading.InspectionID, reading.Seq, reading.TestItem,
		nullableString(reading.SpecMin), nullableString(reading.SpecMax),
		nullableString(reading.Unit), reading.ObservedValue, reading.Outcome,
		nowISO(reading.MeasuredAt), reading.InspectorID, nullableString(reading.Remark))
	return mapExecError(err)
}

func (s *Store) listReadings(ctx context.Context, inspectionID string) ([]Reading, error) {
	rows, err := s.DB.QueryContext(ctx,
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

// ConcludeInspection 将检验单从 open 置为结论状态；已结论的单子不能重结。
func (s *Store) ConcludeInspection(ctx context.Context, inspectionID, verdict, concludedBy string, concludedAt time.Time) error {
	result, err := s.DB.ExecContext(ctx,
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

// ---------------------------------------------------------------------------
// 批次状态与不合格传播
// ---------------------------------------------------------------------------

func setLotState(ctx context.Context, q querier, lotID, state string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE material_lots SET state=? WHERE id=?`, state, lotID)
	return err
}

func markLotFailed(ctx context.Context, q querier, lotID string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE material_lots SET had_failure=1,
		    state=CASE WHEN state='disposed' THEN state ELSE 'held' END
		 WHERE id=?`, lotID)
	return err
}

// DescendantLots 向下追踪派生批次（含自身）。
func (s *Store) DescendantLots(ctx context.Context, lotID string) ([]string, error) {
	return DescendantLotsIn(ctx, s.DB, lotID)
}

// AncestorLots 向上追溯来源批次（含自身）。
func (s *Store) AncestorLots(ctx context.Context, lotID string) ([]string, error) {
	return AncestorLotsIn(ctx, s.DB, lotID)
}

// BatchesUsingLots 返回投料使用了给定物料批次（任一）的配制批次。
func (s *Store) BatchesUsingLots(ctx context.Context, lotIDs []string) ([]CompoundBatch, error) {
	return BatchesUsingLotsIn(ctx, s.DB, lotIDs)
}

// ---------------------------------------------------------------------------
// 配制批次、称量、投料
// ---------------------------------------------------------------------------

func insertCompoundBatch(ctx context.Context, q querier, batch CompoundBatch) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO compound_batches
		    (id, batch_ref, formula_ref, formula_revision, product_name,
		     production_manager_id, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		batch.ID, batch.BatchRef, batch.FormulaRef, batch.FormulaRevision,
		batch.ProductName, batch.ProductionManager, batch.Status, nowISO(batch.CreatedAt))
	return mapExecError(err)
}

func (s *Store) GetCompoundBatchByRef(ctx context.Context, ref string) (CompoundBatch, error) {
	return CompoundBatchByRefIn(ctx, s.DB, ref)
}

func (s *Store) UpdateCompoundStatus(ctx context.Context, batchID, status string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE compound_batches SET status=? WHERE id=?`, status, batchID)
	return err
}

func insertWeighing(ctx context.Context, q querier, weighing Weighing) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO weighings(id, compound_batch_id, material_lot_id, movement_item_id, weight_mg, weighed_at)
		 VALUES (?,?,?,?,?,?)`,
		weighing.ID, weighing.CompoundBatchID, weighing.MaterialLotID,
		weighing.MovementItemID, weighing.WeightMg, nowISO(weighing.WeighedAt))
	return mapExecError(err)
}

func (s *Store) GetWeighing(ctx context.Context, id string) (Weighing, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, compound_batch_id, material_lot_id, movement_item_id, weight_mg, weighed_at
		 FROM weighings WHERE id = ?`, id)
	var weighing Weighing
	var weighedAt string
	if err := row.Scan(&weighing.ID, &weighing.CompoundBatchID, &weighing.MaterialLotID,
		&weighing.MovementItemID, &weighing.WeightMg, &weighedAt); err != nil {
		return Weighing{}, mapExecError(err)
	}
	weighing.WeighedAt = parseTime(weighedAt)
	return weighing, nil
}

func (s *Store) ListWeighingsByBatch(ctx context.Context, batchID string) ([]Weighing, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, compound_batch_id, material_lot_id, movement_item_id, weight_mg, weighed_at
		 FROM weighings WHERE compound_batch_id=? ORDER BY weighed_at`, batchID)
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

// ListUnchargedWeighings 返回尚未投料的称量记录（一次称量只能投料一次）。
func (s *Store) ListUnchargedWeighings(ctx context.Context, batchID string) ([]Weighing, error) {
	rows, err := s.DB.QueryContext(ctx,
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

func insertCharging(ctx context.Context, q querier, charging Charging) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO chargings(id, compound_batch_id, material_lot_id, weighing_id, weight_mg, charged_at, charged_by)
		 VALUES (?,?,?,?,?,?,?)`,
		charging.ID, charging.CompoundBatchID, charging.MaterialLotID, charging.WeighingID,
		charging.WeightMg, nowISO(charging.ChargedAt), charging.ChargedBy)
	return mapExecError(err)
}

func (s *Store) ListChargingsByBatch(ctx context.Context, batchID string) ([]Charging, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, compound_batch_id, material_lot_id, weighing_id, weight_mg, charged_at, charged_by
		 FROM chargings WHERE compound_batch_id=? ORDER BY charged_at`, batchID)
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

// ---------------------------------------------------------------------------
// 放行与处置
// ---------------------------------------------------------------------------

func insertReleaseDecision(ctx context.Context, q querier, decision ReleaseDecision) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO release_decisions
		    (id, compound_batch_id, decision, finished_inspection_id, reason, decided_at, decided_by)
		 VALUES (?,?,?,?,?,?,?)`,
		decision.ID, decision.CompoundBatchID, decision.Decision,
		nullableString(decision.FinishedInspectionID), nullableString(decision.Reason),
		nowISO(decision.DecidedAt), decision.DecidedBy)
	return mapExecError(err)
}

func (s *Store) ListReleaseDecisions(ctx context.Context, batchID string) ([]ReleaseDecision, error) {
	rows, err := s.DB.QueryContext(ctx,
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

func insertDisposal(ctx context.Context, q querier, disposal Disposal) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO lot_disposals(id, material_lot_id, disposition_type, disposed_at, disposed_by, remark)
		 VALUES (?,?,?,?,?,?)`,
		disposal.ID, disposal.MaterialLotID, disposal.DispositionType,
		nowISO(disposal.DisposedAt), disposal.DisposedBy, nullableString(disposal.Remark))
	return mapExecError(err)
}

func (s *Store) ListDisposalsByLot(ctx context.Context, lotID string) ([]Disposal, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, material_lot_id, disposition_type, disposed_at, disposed_by, COALESCE(remark,'')
		 FROM lot_disposals WHERE material_lot_id=? ORDER BY disposed_at`, lotID)
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

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return nowISO(*value)
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	ts := parseTime(value.String)
	return &ts
}
