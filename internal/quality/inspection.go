package quality

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// DrawSampleInput 抽样。对物料批次抽样会按抽样量扣减批次重量台账。
type DrawSampleInput struct {
	SampleRef   string
	SubjectType string // material_lot / compound_batch
	SubjectRef  string // 物料批次编号或配制批次编号
	Kind        string // initial / retest
	RetestOfRef string // 复检时填写原抽样编号
	QtyMg       int64
	SampledAt   time.Time
	SamplerID   string
}

type DrawSampleResult struct {
	Sample     store.Sample
	Inspection store.Inspection
}

// DrawSample 登记抽样并同时开启检验单（检验单只由检验人签字，仓储仅负责抽样登记）。
// 注意：本方法只创建抽样，检验单需检验人调用 OpenInspection 开启，保证两个角色分离。
func (s *Service) DrawSample(ctx context.Context, in DrawSampleInput) (store.Sample, error) {
	if err := nonEmpty("sample_ref", in.SampleRef); err != nil {
		return store.Sample{}, err
	}
	if in.SubjectType != "material_lot" && in.SubjectType != "compound_batch" {
		return store.Sample{}, ruleError(CodeValidation, "subject_type 必须是 material_lot 或 compound_batch")
	}
	if err := nonEmpty("subject_ref", in.SubjectRef); err != nil {
		return store.Sample{}, err
	}
	if in.Kind != "initial" && in.Kind != "retest" {
		return store.Sample{}, ruleError(CodeValidation, "kind 必须是 initial 或 retest")
	}
	if in.Kind == "retest" {
		if err := nonEmpty("retest_of_ref", in.RetestOfRef); err != nil {
			return store.Sample{}, err
		}
	}
	if err := positiveWeight("qty_mg", in.QtyMg); err != nil {
		return store.Sample{}, err
	}

	sample := store.Sample{}
	err := s.withTx(ctx, func(q store.Tx) error {
		// 仓储或检验人都可以取样；配制负责人与药事负责人不代为取样。
		if _, err := requireStaff(ctx, q, in.SamplerID, RoleWarehouse, RoleInspector); err != nil {
			return err
		}

		var subjectID string
		var lot store.MaterialLot
		switch in.SubjectType {
		case "material_lot":
			var err error
			lot, err = store.MaterialLotByRefIn(ctx, q, in.SubjectRef)
			if err != nil {
				return mapStoreError(err)
			}
			if lot.State == "rejected" || lot.State == "disposed" {
				return ruleError(CodeStateConflict, "批次 %s 已%s，不能抽样", lot.LotRef, stateLabel(lot.State))
			}
			inSum, outSum, err := store.LotTotalsIn(ctx, q, lot.ID)
			if err != nil {
				return err
			}
			if inSum-outSum < in.QtyMg {
				return ruleError(CodeWeightMismatch,
					"抽样量 %d mg 超过批次 %s 余量 %d mg", in.QtyMg, lot.LotRef, inSum-outSum)
			}
			subjectID = lot.ID
		case "compound_batch":
			batch, err := store.CompoundBatchByRefIn(ctx, q, in.SubjectRef)
			if err != nil {
				return mapStoreError(err)
			}
			subjectID = batch.ID
		}

		var retestOfID string
		if in.Kind == "retest" {
			original, err := store.SampleByRefIn(ctx, q, in.RetestOfRef)
			if err != nil {
				return mapStoreError(err)
			}
			if original.SubjectType != in.SubjectType || original.SubjectID != subjectID {
				return ruleError(CodeValidation, "复检抽样必须与原抽样针对同一对象")
			}
			retestOfID = original.ID
		}

		now := s.timeNow()
		sample = store.Sample{
			ID: newID("smp"), SampleRef: in.SampleRef, SubjectType: in.SubjectType,
			SubjectID: subjectID, Kind: in.Kind, RetestOfSample: retestOfID,
			QtyMg: in.QtyMg, SampledAt: atOrNow(in.SampledAt, now), SamplerID: in.SamplerID,
		}
		if err := store.InsertSampleIn(ctx, q, sample); err != nil {
			return mapStoreError(err)
		}
		// 抽样耗料入账：只减少可用量，不产生新批次。
		if in.SubjectType == "material_lot" {
			movement := store.Movement{
				ID: newID("mov"), MovementType: "sample", Ref: sample.ID,
				CreatedAt: now, CreatedBy: in.SamplerID, Remark: "实验室抽样耗料",
			}
			item := store.MovementItem{
				ID: newID("itm"), MovementID: movement.ID, MaterialLotID: lot.ID,
				Direction: "out", WeightMg: in.QtyMg,
			}
			if err := store.InsertMovementIn(ctx, q, movement, []store.MovementItem{item}); err != nil {
				return mapStoreError(err)
			}
		}
		return nil
	})
	return sample, err
}

// OpenInspectionInput 检验人对抽样开启检验单。
type OpenInspectionInput struct {
	InspectionRef string
	SampleRef     string
	OpenedBy      string
	Remark        string
}

func (s *Service) OpenInspection(ctx context.Context, in OpenInspectionInput) (store.Inspection, error) {
	if err := nonEmpty("inspection_ref", in.InspectionRef); err != nil {
		return store.Inspection{}, err
	}
	if err := nonEmpty("sample_ref", in.SampleRef); err != nil {
		return store.Inspection{}, err
	}
	inspection := store.Inspection{}
	err := s.withTx(ctx, func(q store.Tx) error {
		// 只有检验人能开检验单——配制负责人不能代替检验人进入检验流程。
		if _, err := requireStaff(ctx, q, in.OpenedBy, RoleInspector); err != nil {
			return err
		}
		sample, err := store.SampleByRefIn(ctx, q, in.SampleRef)
		if err != nil {
			return mapStoreError(err)
		}
		now := s.timeNow()
		inspection = store.Inspection{
			ID: newID("inp"), InspectionRef: in.InspectionRef, SampleID: sample.ID,
			OpenedAt: now, OpenedBy: in.OpenedBy, Remark: in.Remark,
		}
		return mapStoreError(store.InsertInspectionIn(ctx, q, inspection))
	})
	return inspection, err
}

// ReadingInput 录入一笔检验读数。
type ReadingInput struct {
	TestItem      string
	SpecMin       string // 合格范围下界（含），可空
	SpecMax       string // 合格范围上界（含），可空
	Unit          string
	ObservedValue string
	MeasuredAt    time.Time
	InspectorID   string
	Remark        string
}

type ReadingResult struct {
	Reading store.Reading
	// Outcome 为本笔读数按合格范围自动判定的结果。
	Outcome string
}

// AddReading 向检验单追加一笔读数。读数一经写入即不可修改或删除（数据库触发器强制）。
func (s *Service) AddReading(ctx context.Context, inspectionRef string, in ReadingInput) (ReadingResult, error) {
	if err := nonEmpty("test_item", in.TestItem); err != nil {
		return ReadingResult{}, err
	}
	if err := nonEmpty("observed_value", in.ObservedValue); err != nil {
		return ReadingResult{}, err
	}
	observed, err := decimal.NewFromString(in.ObservedValue)
	if err != nil {
		return ReadingResult{}, ruleError(CodeValidation, "observed_value 必须是数值，实际为 %q", in.ObservedValue)
	}
	outcome, err := evaluateReading(in.SpecMin, in.SpecMax, observed)
	if err != nil {
		return ReadingResult{}, err
	}

	result := ReadingResult{}
	err = s.withTx(ctx, func(q store.Tx) error {
		// 签字人必须是检验人；配制负责人/药事负责人的账号在这里直接被拒绝。
		if _, err := requireStaff(ctx, q, in.InspectorID, RoleInspector); err != nil {
			return err
		}
		inspection, err := store.InspectionByRefIn(ctx, q, inspectionRef)
		if err != nil {
			return mapStoreError(err)
		}
		if inspection.Verdict != "" {
			return ruleError(CodeStateConflict, "检验单 %s 已作出结论，不得再追加读数", inspectionRef)
		}
		seq, err := store.MaxReadingSeqIn(ctx, q, inspection.ID)
		if err != nil {
			return err
		}
		now := s.timeNow()
		reading := store.Reading{
			ID: newID("rdg"), InspectionID: inspection.ID, Seq: seq + 1,
			TestItem: in.TestItem, SpecMin: in.SpecMin, SpecMax: in.SpecMax, Unit: in.Unit,
			ObservedValue: in.ObservedValue, Outcome: outcome,
			MeasuredAt: atOrNow(in.MeasuredAt, now), InspectorID: in.InspectorID, Remark: in.Remark,
		}
		if err := store.InsertReadingIn(ctx, q, reading); err != nil {
			return mapStoreError(err)
		}
		// 出现失败读数立即挂起对应物料批次：失败事实先于结论生效，避免结论前被领用。
		if outcome == "fail" {
			sample, err := store.SampleIn(ctx, q, inspection.SampleID)
			if err != nil {
				return err
			}
			if sample.SubjectType == "material_lot" {
				if err := s.markFailureDownstream(ctx, q, sample.SubjectID,
					"检验读数失败："+in.TestItem); err != nil {
					return err
				}
			}
		}
		result = ReadingResult{Reading: reading, Outcome: outcome}
		return nil
	})
	return result, err
}

// ConcludeInspectionInput 检验人签字结论。
type ConcludeInspectionInput struct {
	InspectionRef string
	InspectorID   string
	ConcludedAt   time.Time
	Remark        string
}

type ConcludeResult struct {
	Verdict    string
	Inspection store.Inspection
}

// ConcludeInspection 由检验人对检验单下结论：有任一失败读数即只能判不合格。
// 复检是"新抽样+新检验单"，本单结论与读数永久保留，不允许重开或改写。
func (s *Service) ConcludeInspection(ctx context.Context, in ConcludeInspectionInput) (ConcludeResult, error) {
	result := ConcludeResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.InspectorID, RoleInspector); err != nil {
			return err
		}
		inspection, err := store.InspectionByRefIn(ctx, q, in.InspectionRef)
		if err != nil {
			return mapStoreError(err)
		}
		if inspection.Verdict != "" {
			return ruleError(CodeStateConflict, "检验单 %s 已结论（%s），不得重结",
				inspection.InspectionRef, inspection.Verdict)
		}
		if len(inspection.Readings) == 0 {
			return ruleError(CodeStateConflict, "检验单没有任何读数，不能作出结论")
		}
		verdict := "qualified"
		for _, reading := range inspection.Readings {
			if reading.Outcome == "fail" {
				verdict = "unqualified"
				break
			}
		}

		now := s.timeNow()
		if err := store.ConcludeInspectionIn(ctx, q, inspection.ID, verdict, in.InspectorID,
			atOrNow(in.ConcludedAt, now)); err != nil {
			return mapStoreError(err)
		}
		if in.Remark != "" {
			// 备注随结论一起留痕（不改变既有读数）。
			if _, err := q.ExecContext(ctx,
				`UPDATE inspections SET remark=? WHERE id=?`, in.Remark, inspection.ID); err != nil {
				return err
			}
		}

		sample, err := store.SampleIn(ctx, q, inspection.SampleID)
		if err != nil {
			return err
		}
		switch {
		case verdict == "unqualified" && sample.SubjectType == "material_lot":
			if err := s.markFailureDownstream(ctx, q, sample.SubjectID,
				"检验结论：不合格（检验单 "+inspection.InspectionRef+"）"); err != nil {
				return err
			}
		case verdict == "unqualified" && sample.SubjectType == "compound_batch":
			if err := store.SetBatchBlocked(ctx, q, sample.SubjectID,
				"成品检验不合格（检验单 "+inspection.InspectionRef+"）"); err != nil {
				return err
			}
			if err := store.UpdateCompoundStatusIn(ctx, q, sample.SubjectID, "rejected"); err != nil {
				return err
			}
		case verdict == "qualified" && sample.SubjectType == "compound_batch":
			batch, err := store.CompoundBatchIn(ctx, q, sample.SubjectID)
			if err != nil {
				return mapStoreError(err)
			}
			// 正常流程下成品先检验后放行；只有待检状态才进入"检验合格"。
			if batch.Status == "charged" || batch.Status == "planned" {
				if err := store.UpdateCompoundStatusIn(ctx, q, batch.ID, "qc_passed"); err != nil {
					return err
				}
			}
		case verdict == "qualified" && sample.SubjectType == "material_lot":
			lot, err := store.MaterialLotIn(ctx, q, sample.SubjectID)
			if err != nil {
				return err
			}
			// 曾有失败读数的批次永不因合格结论自动解锁——复检不能覆盖第一次失败。
			if !lot.HadFailure && lot.State == "quarantined" {
				if err := store.SetLotStateIn(ctx, q, lot.ID, "available"); err != nil {
					return err
				}
			}
		}
		inspection.Verdict = verdict
		inspection.ConcludedBy = in.InspectorID
		result = ConcludeResult{Verdict: verdict, Inspection: inspection}
		return nil
	})
	return result, err
}

// markFailureDownstream 把不合格事实沿分装/合批谱系向下传播：
// 所有派生物料批次永久挂起，所有已使用这些物料的配制批次被阻断；已放行的转为召回。
func (s *Service) markFailureDownstream(ctx context.Context, q store.Tx, lotID, reason string) error {
	descendants, err := store.DescendantLotsIn(ctx, q, lotID)
	if err != nil {
		return err
	}
	for _, id := range descendants {
		if err := store.MarkLotFailedIn(ctx, q, id); err != nil {
			return err
		}
	}
	batches, err := store.BatchesUsingLotsIn(ctx, q, descendants)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		if err := store.SetBatchBlocked(ctx, q, batch.ID, reason); err != nil {
			return err
		}
	}
	return nil
}

// evaluateReading 按合格范围（含边界）判定读数。未提供范围时无法判定，拒绝录入以免事后争议。
func evaluateReading(specMin, specMax string, observed decimal.Decimal) (string, error) {
	if specMin == "" && specMax == "" {
		return "", ruleError(CodeValidation, "必须提供合格范围 spec_min/spec_max 至少一项")
	}
	if specMin != "" {
		minValue, err := decimal.NewFromString(specMin)
		if err != nil {
			return "", ruleError(CodeValidation, "spec_min 不是数值：%q", specMin)
		}
		if observed.LessThan(minValue) {
			return "fail", nil
		}
	}
	if specMax != "" {
		maxValue, err := decimal.NewFromString(specMax)
		if err != nil {
			return "", ruleError(CodeValidation, "spec_max 不是数值：%q", specMax)
		}
		if observed.GreaterThan(maxValue) {
			return "fail", nil
		}
	}
	return "pass", nil
}
