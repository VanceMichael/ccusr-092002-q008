package quality

import (
	"context"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// LotLedgerView 是物料批次的内部核对视图：当前状态、重量台账合计与全部检验记录。
type LotLedgerView struct {
	Lot         store.MaterialLot
	TotalInMg   int64
	TotalOutMg  int64
	BalanceMg   int64
	Entries     []store.LedgerEntry
	Samples     []store.Sample
	Inspections []store.Inspection
	Disposals   []store.Disposal
}

// LotLedger 按来源编号查询物料批次台账（内部岗位使用），用于重量核对与质量追踪。
func (s *Service) LotLedger(ctx context.Context, lotRef string) (LotLedgerView, error) {
	if err := nonEmpty("lot_ref", lotRef); err != nil {
		return LotLedgerView{}, err
	}
	view := LotLedgerView{}
	err := s.withTx(ctx, func(q store.Tx) error {
		lot, err := store.MaterialLotByRefIn(ctx, q, lotRef)
		if err != nil {
			return mapStoreError(err)
		}
		view.Lot = lot
		inSum, outSum, err := store.LotTotalsIn(ctx, q, lot.ID)
		if err != nil {
			return err
		}
		view.TotalInMg, view.TotalOutMg, view.BalanceMg = inSum, outSum, inSum-outSum
		view.Entries, err = store.LedgerEntriesIn(ctx, q, lot.ID)
		if err != nil {
			return err
		}
		view.Samples, err = store.SamplesBySubjectIn(ctx, q, "material_lot", lot.ID)
		if err != nil {
			return err
		}
		view.Inspections, err = store.InspectionsBySubjectIn(ctx, q, "material_lot", lot.ID)
		if err != nil {
			return err
		}
		view.Disposals, err = store.DisposalsByLotIn(ctx, q, lot.ID)
		return err
	})
	return view, err
}

// LotLedgerResponse 是物料批次台账的对外（内部岗位）JSON 视图。
type LotLedgerResponse struct {
	LotRef     string            `json:"lot_ref"`
	State      string            `json:"state"`
	HadFailure bool              `json:"had_failure"`
	TotalInMg  int64             `json:"total_in_mg"`
	TotalOutMg int64             `json:"total_out_mg"`
	BalanceMg  int64             `json:"balance_mg"`
	Entries    []LedgerEntryJSON `json:"entries"`
}

type LedgerEntryJSON struct {
	MovementType string    `json:"movement_type"`
	Direction    string    `json:"direction"`
	WeightMg     int64     `json:"weight_mg"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
	Remark       string    `json:"remark,omitempty"`
}

// LotLedgerForViewer 供内部岗位查询物料批次的逐笔重量台账，用于核对拆分/合批/称量。
func (s *Service) LotLedgerForViewer(ctx context.Context, lotRef, viewerID string) (LotLedgerResponse, error) {
	if _, err := requireStaffOn(ctx, s, viewerID,
		RoleWarehouse, RoleInspector, RoleProductionManager, RolePharmacyHead); err != nil {
		return LotLedgerResponse{}, err
	}
	view, err := s.LotLedger(ctx, lotRef)
	if err != nil {
		return LotLedgerResponse{}, err
	}
	resp := LotLedgerResponse{
		LotRef: view.Lot.LotRef, State: view.Lot.State, HadFailure: view.Lot.HadFailure,
		TotalInMg: view.TotalInMg, TotalOutMg: view.TotalOutMg, BalanceMg: view.BalanceMg,
	}
	for _, entry := range view.Entries {
		resp.Entries = append(resp.Entries, LedgerEntryJSON{
			MovementType: entry.MovementType, Direction: entry.Direction,
			WeightMg: entry.WeightMg, CreatedAt: entry.CreatedAt,
			CreatedBy: entry.CreatedBy, Remark: entry.Remark,
		})
	}
	return resp, nil
}

// requireStaffOn 在无需写入的查询路径上做角色校验（只读事务）。
func requireStaffOn(ctx context.Context, s *Service, staffID string, roles ...string) (store.Staff, error) {
	var staff store.Staff
	err := s.withTx(ctx, func(q store.Tx) error {
		var err error
		staff, err = requireStaff(ctx, q, staffID, roles...)
		return err
	})
	return staff, err
}

// AffectedBatch 是一袋不合格原料实际影响到的成品批次。
type AffectedBatch struct {
	BatchRef       string `json:"batch_ref"`
	ProductName    string `json:"product_name"`
	Status         string `json:"status"`
	QualityBlocked bool   `json:"quality_blocked"`
	BlockReason    string `json:"quality_blocked_reason,omitempty"`
	Relationship   string `json:"relationship"` // direct：直接投料；downstream：经分装/合批后代投料
}

// AffectedBatches 从任一物料批次向下追踪：列出直接或经分装/合批后代使用了它的全部配制批次。
// 供药事负责人在一袋原料判不合格时，立即定位需要停用/召回的实际成品，而不是停用整个系列。
func (s *Service) AffectedBatches(ctx context.Context, lotRef, viewerID string) ([]AffectedBatch, error) {
	if err := nonEmpty("lot_ref", lotRef); err != nil {
		return nil, err
	}
	affected := []AffectedBatch{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, viewerID, RolePharmacyHead, RoleInspector, RoleWarehouse); err != nil {
			return err
		}
		lot, err := store.MaterialLotByRefIn(ctx, q, lotRef)
		if err != nil {
			return mapStoreError(err)
		}
		directBatches, err := store.BatchesUsingLotsIn(ctx, q, []string{lot.ID})
		if err != nil {
			return err
		}
		direct := map[string]bool{}
		for _, batch := range directBatches {
			direct[batch.ID] = true
		}
		descendants, err := store.DescendantLotsIn(ctx, q, lot.ID)
		if err != nil {
			return err
		}
		allBatches, err := store.BatchesUsingLotsIn(ctx, q, descendants)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, batch := range allBatches {
			if seen[batch.ID] {
				continue
			}
			seen[batch.ID] = true
			relationship := "downstream"
			if direct[batch.ID] {
				relationship = "direct"
			}
			affected = append(affected, AffectedBatch{
				BatchRef: batch.BatchRef, ProductName: batch.ProductName,
				Status: batch.Status, QualityBlocked: batch.QualityBlocked,
				BlockReason: batch.QualityBlockReason, Relationship: relationship,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return affected, nil
}
