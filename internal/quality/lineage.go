package quality

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// LineageTrace 是药事负责人从任一成品反查到的完整证据链。
type LineageTrace struct {
	Batch            BatchLineage        `json:"batch"`
	BatchInspections []InspectionLineage `json:"batch_inspections"`
	Chargings        []ChargingLineage   `json:"chargings"`
	Lots             []LotLineage        `json:"lots"`
	Movements        []MovementLineage   `json:"movements"`
	Releases         []ReleaseLineage    `json:"release_decisions"`
}

type BatchLineage struct {
	BatchRef          string    `json:"batch_ref"`
	FormulaRef        string    `json:"formula_ref"`
	FormulaRevision   int       `json:"formula_revision"`
	ProductName       string    `json:"product_name"`
	Status            string    `json:"status"`
	QualityBlocked    bool      `json:"quality_blocked"`
	BlockReason       string    `json:"quality_blocked_reason,omitempty"`
	ProductionManager string    `json:"production_manager_id"`
	CreatedAt         time.Time `json:"created_at"`
}

type ChargingLineage struct {
	MaterialLotRef string    `json:"material_lot_ref"`
	MaterialCode   string    `json:"material_code"`
	MaterialName   string    `json:"material_name"`
	WeightMg       int64     `json:"weight_mg"`
	ChargedAt      time.Time `json:"charged_at"`
	ChargedBy      string    `json:"charged_by"`
}

type LotLineage struct {
	LotRef          string              `json:"lot_ref"`
	MaterialCode    string              `json:"material_code"`
	MaterialName    string              `json:"material_name"`
	State           string              `json:"state"`
	HadFailure      bool                `json:"had_failure"`
	InitialWeightMg int64               `json:"initial_weight_mg"`
	BalanceMg       int64               `json:"balance_mg"`
	Inbound         *InboundLineage     `json:"inbound,omitempty"`
	Samples         []SampleLineage     `json:"samples"`
	Inspections     []InspectionLineage `json:"inspections"`
	Disposals       []DisposalLineage   `json:"disposals"`
}

type InboundLineage struct {
	LotRef       string            `json:"lot_ref"`
	SupplierID   string            `json:"supplier_id"`
	SupplierName string            `json:"supplier_name"`
	Origin       string            `json:"origin"`
	LicenseScope string            `json:"license_scope"`
	Certificates []CertificateLine `json:"certificates"`
	NetWeightMg  int64             `json:"net_weight_mg"`
	ReceivedAt   time.Time         `json:"received_at"`
	ReceiverID   string            `json:"receiver_id"`
}

type CertificateLine struct {
	CertType     string     `json:"cert_type"`
	CertRef      string     `json:"cert_ref"`
	LicenseScope string     `json:"license_scope"`
	IssuedAt     *time.Time `json:"issued_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type SampleLineage struct {
	SampleRef   string    `json:"sample_ref"`
	Kind        string    `json:"kind"`
	RetestOfRef string    `json:"retest_of_ref,omitempty"`
	QtyMg       int64     `json:"qty_mg"`
	SampledAt   time.Time `json:"sampled_at"`
	SamplerID   string    `json:"sampler_id"`
}

type InspectionLineage struct {
	InspectionRef string           `json:"inspection_ref"`
	SampleRef     string           `json:"sample_ref"`
	Verdict       string           `json:"verdict"`
	OpenedAt      time.Time        `json:"opened_at"`
	OpenedBy      string           `json:"opened_by"`
	ConcludedAt   *time.Time       `json:"concluded_at,omitempty"`
	ConcludedBy   string           `json:"concluded_by,omitempty"`
	Readings      []ReadingLineage `json:"readings"`
}

type ReadingLineage struct {
	Seq           int       `json:"seq"`
	TestItem      string    `json:"test_item"`
	SpecMin       string    `json:"spec_min,omitempty"`
	SpecMax       string    `json:"spec_max,omitempty"`
	Unit          string    `json:"unit,omitempty"`
	ObservedValue string    `json:"observed_value"`
	Outcome       string    `json:"outcome"`
	MeasuredAt    time.Time `json:"measured_at"`
	InspectorID   string    `json:"inspector_id"`
}

type DisposalLineage struct {
	DispositionType string    `json:"disposition_type"`
	DisposedAt      time.Time `json:"disposed_at"`
	DisposedBy      string    `json:"disposed_by"`
	Remark          string    `json:"remark,omitempty"`
}

type MovementLineage struct {
	MovementType string         `json:"movement_type"`
	CreatedAt    time.Time      `json:"created_at"`
	CreatedBy    string         `json:"created_by"`
	Remark       string         `json:"remark,omitempty"`
	Outs         []MovementPart `json:"outs"`
	Ins          []MovementPart `json:"ins"`
}

type MovementPart struct {
	LotRef   string `json:"lot_ref"`
	WeightMg int64  `json:"weight_mg"`
}

type ReleaseLineage struct {
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
	DecidedBy string    `json:"decided_by"`
}

// TraceBatch 供药事负责人从成品反查全部所用药材与每一次质量判断。
// 返回结果沿分装/合批谱系向上追溯到入厂批次，并保留逐笔检验读数与放行决策。
func (s *Service) TraceBatch(ctx context.Context, batchRef, viewerID string) (LineageTrace, error) {
	if err := nonEmpty("batch_ref", batchRef); err != nil {
		return LineageTrace{}, err
	}
	trace := LineageTrace{}
	err := s.withTx(ctx, func(q store.Tx) error {
		// 完整证据链只对药事负责人开放。
		if _, err := requireStaff(ctx, q, viewerID, RolePharmacyHead); err != nil {
			return err
		}
		batch, err := store.CompoundBatchByRefIn(ctx, q, batchRef)
		if err != nil {
			return mapStoreError(err)
		}
		trace.Batch = BatchLineage{
			BatchRef: batch.BatchRef, FormulaRef: batch.FormulaRef,
			FormulaRevision: batch.FormulaRevision, ProductName: batch.ProductName,
			Status: batch.Status, QualityBlocked: batch.QualityBlocked,
			BlockReason:       batch.QualityBlockReason,
			ProductionManager: batch.ProductionManager, CreatedAt: batch.CreatedAt,
		}

		chargings, err := store.ChargingsByBatchIn(ctx, q, batch.ID)
		if err != nil {
			return err
		}

		// 收集投料物料的全部谱系祖先（含合批来源），去重。
		lotIDs := map[string]bool{}
		for _, charging := range chargings {
			ancestors, err := store.AncestorLotsIn(ctx, q, charging.MaterialLotID)
			if err != nil {
				return err
			}
			for _, id := range ancestors {
				lotIDs[id] = true
			}
		}

		lotByID := map[string]store.MaterialLot{}
		materialByID := map[string]store.Material{}
		for id := range lotIDs {
			lot, err := store.MaterialLotIn(ctx, q, id)
			if err != nil {
				return err
			}
			lotByID[id] = lot
			if _, ok := materialByID[lot.MaterialID]; !ok {
				material, err := store.MaterialIn(ctx, q, lot.MaterialID)
				if err != nil {
					return err
				}
				materialByID[lot.MaterialID] = material
			}
		}

		// 投料明细（只展示实际投入成品的那一支）。
		for _, charging := range chargings {
			lot := lotByID[charging.MaterialLotID]
			material := materialByID[lot.MaterialID]
			trace.Chargings = append(trace.Chargings, ChargingLineage{
				MaterialLotRef: lot.LotRef, MaterialCode: material.Code,
				MaterialName: material.Name, WeightMg: charging.WeightMg,
				ChargedAt: charging.ChargedAt, ChargedBy: charging.ChargedBy,
			})
		}

		// 每个物料批次：入厂凭证、抽样、检验（含每次失败读数）、处置、重量余量。
		for _, lot := range lotByID {
			entry := LotLineage{
				LotRef: lot.LotRef, State: lot.State, HadFailure: lot.HadFailure,
				InitialWeightMg: lot.InitialWeightMg,
			}
			material := materialByID[lot.MaterialID]
			entry.MaterialCode = material.Code
			entry.MaterialName = material.Name
			inSum, outSum, err := store.LotTotalsIn(ctx, q, lot.ID)
			if err != nil {
				return err
			}
			entry.BalanceMg = inSum - outSum

			if lot.RootInboundLotID != "" {
				inbound, err := store.InboundLotIn(ctx, q, lot.RootInboundLotID)
				if err == nil {
					line := &InboundLineage{
						LotRef: inbound.LotRef, SupplierID: inbound.SupplierID,
						NetWeightMg: inbound.NetWeightMg, ReceivedAt: inbound.ReceivedAt,
						ReceiverID: inbound.ReceiverID,
					}
					if supplier, supErr := store.SupplierIn(ctx, q, inbound.SupplierID); supErr == nil {
						line.SupplierName = supplier.Name
						line.Origin = supplier.Origin
						line.LicenseScope = supplier.LicenseScope
					}
					if certs, certErr := store.CertificatesBySupplierIn(ctx, q, inbound.SupplierID); certErr == nil {
						for _, cert := range certs {
							line.Certificates = append(line.Certificates, CertificateLine{
								CertType: cert.CertType, CertRef: cert.CertRef,
								LicenseScope: cert.LicenseScope,
								IssuedAt:     cert.IssuedAt, ExpiresAt: cert.ExpiresAt,
							})
						}
					}
					entry.Inbound = line
				} else if !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}

			samples, err := store.SamplesBySubjectIn(ctx, q, "material_lot", lot.ID)
			if err != nil {
				return err
			}
			sampleByID := map[string]store.Sample{}
			for _, sample := range samples {
				sampleByID[sample.ID] = sample
				line := SampleLineage{
					SampleRef: sample.SampleRef, Kind: sample.Kind,
					QtyMg: sample.QtyMg, SampledAt: sample.SampledAt, SamplerID: sample.SamplerID,
				}
				if sample.RetestOfSample != "" {
					if original, ok := sampleByID[sample.RetestOfSample]; ok {
						line.RetestOfRef = original.SampleRef
					}
				}
				entry.Samples = append(entry.Samples, line)
			}

			inspections, err := store.InspectionsBySubjectIn(ctx, q, "material_lot", lot.ID)
			if err != nil {
				return err
			}
			for _, inspection := range inspections {
				line := InspectionLineage{
					InspectionRef: inspection.InspectionRef,
					SampleRef:     sampleByID[inspection.SampleID].SampleRef,
					Verdict:       inspection.Verdict,
					OpenedAt:      inspection.OpenedAt, OpenedBy: inspection.OpenedBy,
					ConcludedAt: inspection.ConcludedAt, ConcludedBy: inspection.ConcludedBy,
				}
				for _, reading := range inspection.Readings {
					line.Readings = append(line.Readings, ReadingLineage{
						Seq: reading.Seq, TestItem: reading.TestItem,
						SpecMin: reading.SpecMin, SpecMax: reading.SpecMax, Unit: reading.Unit,
						ObservedValue: reading.ObservedValue, Outcome: reading.Outcome,
						MeasuredAt: reading.MeasuredAt, InspectorID: reading.InspectorID,
					})
				}
				entry.Inspections = append(entry.Inspections, line)
			}

			disposals, err := store.DisposalsByLotIn(ctx, q, lot.ID)
			if err != nil {
				return err
			}
			for _, disposal := range disposals {
				entry.Disposals = append(entry.Disposals, DisposalLineage{
					DispositionType: disposal.DispositionType, DisposedAt: disposal.DisposedAt,
					DisposedBy: disposal.DisposedBy, Remark: disposal.Remark,
				})
			}
			trace.Lots = append(trace.Lots, entry)
		}

		// 分装/合批移动（含每笔重量，用于重量核对）。
		ids := make([]string, 0, len(lotIDs))
		for id := range lotIDs {
			ids = append(ids, id)
		}
		edges, err := store.MovementsAmongLotsIn(ctx, q, ids)
		if err != nil {
			return err
		}
		trace.Movements = groupMovementEdges(edges, lotByID)

		// 成品批次自身的检验与放行决策。
		batchInspections, err := store.InspectionsBySubjectIn(ctx, q, "compound_batch", batch.ID)
		if err != nil {
			return err
		}
		for _, inspection := range batchInspections {
			line := InspectionLineage{
				InspectionRef: inspection.InspectionRef,
				Verdict:       inspection.Verdict,
				OpenedAt:      inspection.OpenedAt, OpenedBy: inspection.OpenedBy,
				ConcludedAt: inspection.ConcludedAt, ConcludedBy: inspection.ConcludedBy,
			}
			for _, reading := range inspection.Readings {
				line.Readings = append(line.Readings, ReadingLineage{
					Seq: reading.Seq, TestItem: reading.TestItem,
					SpecMin: reading.SpecMin, SpecMax: reading.SpecMax, Unit: reading.Unit,
					ObservedValue: reading.ObservedValue, Outcome: reading.Outcome,
					MeasuredAt: reading.MeasuredAt, InspectorID: reading.InspectorID,
				})
			}
			trace.BatchInspections = append(trace.BatchInspections, line)
		}

		decisions, err := store.ReleaseDecisionsIn(ctx, q, batch.ID)
		if err != nil {
			return err
		}
		for _, decision := range decisions {
			trace.Releases = append(trace.Releases, ReleaseLineage{
				Decision: decision.Decision, Reason: decision.Reason,
				DecidedAt: decision.DecidedAt, DecidedBy: decision.DecidedBy,
			})
		}
		return nil
	})
	if err != nil {
		return LineageTrace{}, err
	}
	sortTrace(&trace)
	return trace, nil
}

func groupMovementEdges(edges []store.MovementEdge, lotByID map[string]store.MaterialLot) []MovementLineage {
	byID := map[string]*MovementLineage{}
	order := []string{}
	for _, edge := range edges {
		line, ok := byID[edge.MovementID]
		if !ok {
			line = &MovementLineage{
				MovementType: edge.MovementType, CreatedAt: edge.CreatedAt,
				CreatedBy: edge.CreatedBy, Remark: edge.Remark,
			}
			byID[edge.MovementID] = line
			order = append(order, edge.MovementID)
		}
		part := MovementPart{LotRef: lotByID[edge.MaterialLotID].LotRef, WeightMg: edge.WeightMg}
		if edge.Direction == "out" {
			line.Outs = append(line.Outs, part)
		} else {
			line.Ins = append(line.Ins, part)
		}
	}
	movements := make([]MovementLineage, 0, len(order))
	for _, id := range order {
		movements = append(movements, *byID[id])
	}
	return movements
}

func sortTrace(trace *LineageTrace) {
	sort.Slice(trace.Lots, func(i, j int) bool { return trace.Lots[i].LotRef < trace.Lots[j].LotRef })
	for i := range trace.Lots {
		sort.Slice(trace.Lots[i].Inspections, func(a, b int) bool {
			return trace.Lots[i].Inspections[a].OpenedAt.Before(trace.Lots[i].Inspections[b].OpenedAt)
		})
	}
}
