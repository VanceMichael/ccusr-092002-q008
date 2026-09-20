package quality

import (
	"context"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// PublicRange 是对外部公开的检验合格范围（只给范围，不给实测值、结论、检验人）。
type PublicRange struct {
	TestItem string `json:"test_item"`
	SpecMin  string `json:"spec_min,omitempty"`
	SpecMax  string `json:"spec_max,omitempty"`
	Unit     string `json:"unit,omitempty"`
}

// PublicBatchView 是外部查询的唯一返回形态：只有合格范围与处置状态。
// 不含供应商、产地、凭证、抽样、实测读数、检验结论、签字人员等任何内部信息。
type PublicBatchView struct {
	BatchRef          string        `json:"batch_ref"`
	QualifiedRanges   []PublicRange `json:"qualified_ranges"`
	DispositionStatus string        `json:"disposition_status"` // released / held / rejected / recalled / pending
}

// PublicBatchStatus 供外部查询成品批次。即使内部存在不合格记录，
// 对外也只回传"合格范围"和"处置状态"，不回传失败读数、供应商或人员信息。
func (s *Service) PublicBatchStatus(ctx context.Context, batchRef string) (PublicBatchView, error) {
	if err := nonEmpty("batch_ref", batchRef); err != nil {
		return PublicBatchView{}, err
	}
	view := PublicBatchView{BatchRef: batchRef, DispositionStatus: "pending"}
	err := s.withTx(ctx, func(q store.Tx) error {
		batch, err := store.CompoundBatchByRefIn(ctx, q, batchRef)
		if err != nil {
			return mapStoreError(err)
		}

		// 合格范围：取最新一份已结论成品检验单录入的各项目范围，去重后对外展示。
		inspections, err := store.InspectionsBySubjectIn(ctx, q, "compound_batch", batch.ID)
		if err != nil {
			return err
		}
		ranges := []PublicRange{}
		seen := map[string]bool{}
		for i := len(inspections) - 1; i >= 0; i-- {
			inspection := inspections[i]
			if inspection.Verdict == "" {
				continue
			}
			for _, reading := range inspection.Readings {
				key := reading.TestItem + "|" + reading.SpecMin + "|" + reading.SpecMax + "|" + reading.Unit
				if seen[key] {
					continue
				}
				seen[key] = true
				ranges = append(ranges, PublicRange{
					TestItem: reading.TestItem, SpecMin: reading.SpecMin,
					SpecMax: reading.SpecMax, Unit: reading.Unit,
				})
			}
		}
		view.QualifiedRanges = ranges

		// 处置状态：已放行又被阻断的必须显示召回；已阻断但未放行的显示暂缓；
		// 否则以最新放行决策为准，再退回批次状态。
		if batch.Status == "recalled" {
			view.DispositionStatus = "recalled"
			return nil
		}
		if batch.QualityBlocked {
			view.DispositionStatus = "held"
			return nil
		}
		decisions, err := store.ReleaseDecisionsIn(ctx, q, batch.ID)
		if err != nil {
			return err
		}
		if len(decisions) > 0 {
			view.DispositionStatus = decisions[len(decisions)-1].Decision
			return nil
		}
		switch batch.Status {
		case "released":
			view.DispositionStatus = "released"
		case "rejected":
			view.DispositionStatus = "rejected"
		case "recalled":
			view.DispositionStatus = "recalled"
		default:
			view.DispositionStatus = "pending"
		}
		return nil
	})
	return view, err
}
