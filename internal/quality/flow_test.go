package quality_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
)

// 场景一：分装与合批的重量必须逐笔可核对。
func TestSplitMergeWeightConservation(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)

	// 拆成 600g + 400g，合计必须等于领出的 1000g。
	if _, err := env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A", WeightMg: 1_000_000, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1", WeightMg: 600_000},
			{LotRef: "LOT-A2", WeightMg: 400_000},
		},
	}); err != nil {
		t.Fatalf("守恒分装失败: %v", err)
	}

	// 守恒破坏：子批次合计 999g != 领出 1000g，必须被拒绝。
	_, err := env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A1", WeightMg: 600_000, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1X", WeightMg: 599_000},
		},
	})
	if code := ruleCode(t, err); code != quality.CodeWeightMismatch {
		t.Fatalf("重量不守恒应返回 weight_mismatch，实际 %s（%v）", code, err)
	}

	// 超量领出也必须被拒绝（批次余量不足）。
	_, err = env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A1", WeightMg: 600_001, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1Y", WeightMg: 600_001},
		},
	})
	if code := ruleCode(t, err); code != quality.CodeWeightMismatch {
		t.Fatalf("超量分装应返回 weight_mismatch，实际 %s", code)
	}

	// 合批守恒：两个来源各领 300g + 200g = 新批次 500g。
	merge, err := env.svc.Merge(ctx, quality.MergeInput{
		TargetLotRef: "LOT-M", OperatorID: staff.warehouse,
		Sources: []quality.MergeSource{
			{LotRef: "LOT-A1", WeightMg: 300_000},
			{LotRef: "LOT-A2", WeightMg: 200_000},
		},
	})
	if err != nil {
		t.Fatalf("守恒合批失败: %v", err)
	}
	if merge.Target.InitialWeightMg != 500_000 {
		t.Fatalf("合批新批次重量应为 500000，实际 %d", merge.Target.InitialWeightMg)
	}

	// 台账可核对：LOT-A1 入 600g，出 = 600g 分装失败不应有记录 + 300g 合批。
	ledger, err := env.svc.LotLedger(ctx, "LOT-A1")
	if err != nil {
		t.Fatalf("查询台账失败: %v", err)
	}
	if ledger.BalanceMg != 300_000 {
		t.Fatalf("LOT-A1 余量应为 300000，实际 %d（入 %d 出 %d）",
			ledger.BalanceMg, ledger.TotalInMg, ledger.TotalOutMg)
	}
}

// 场景二：不同药材品种不能合批。
func TestMergeRejectsDifferentMaterials(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	if _, err := env.svc.RegisterMaterial(ctx, quality.RegisterMaterialInput{
		Code: "MAT-DG", Name: "当归",
	}); err != nil {
		t.Fatalf("登记当归失败: %v", err)
	}
	if _, err := env.svc.ReceiveInboundLot(ctx, quality.ReceiveInboundLotInput{
		LotRef: "LOT-B", SupplierID: "sup_gansu", MaterialCode: "MAT-DG",
		NetWeightMg: 1_000_000, ReceiverID: staff.warehouse,
	}); err != nil {
		t.Fatalf("收当归失败: %v", err)
	}
	_, err := env.svc.Merge(ctx, quality.MergeInput{
		TargetLotRef: "LOT-X", OperatorID: staff.warehouse,
		Sources: []quality.MergeSource{
			{LotRef: "LOT-A", WeightMg: 100_000},
			{LotRef: "LOT-B", WeightMg: 100_000},
		},
	})
	if code := ruleCode(t, err); code != quality.CodeValidation {
		t.Fatalf("跨品种合批应被 validation_error 拒绝，实际 %s", code)
	}
}

// 场景三：第一次失败读数永久保留，复检合格也不能覆盖或清除。
func TestFailedReadingCannotBeOverwrittenByRetest(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)

	// 初次抽样：读数超标。
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: "SMP-1", SubjectType: "material_lot", SubjectRef: "LOT-A",
		Kind: "initial", QtyMg: 1000, SamplerID: staff.warehouse,
	}); err != nil {
		t.Fatalf("初次抽样失败: %v", err)
	}
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: "INP-1", SampleRef: "SMP-1", OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("开单失败: %v", err)
	}
	if _, err := env.svc.AddReading(ctx, "INP-1", quality.ReadingInput{
		TestItem: "含量测定", SpecMin: "90", SpecMax: "110", Unit: "%",
		ObservedValue: "72", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("失败读数录入失败: %v", err)
	}
	first, err := env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: "INP-1", InspectorID: staff.inspector,
	})
	if err != nil || first.Verdict != "unqualified" {
		t.Fatalf("首次检验应判不合格，verdict=%v err=%v", first.Verdict, err)
	}

	// 尝试直接改判原检验单——必须被拒绝。
	_, err = env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: "INP-1", InspectorID: staff.inspector,
	})
	if code := ruleCode(t, err); code != quality.CodeStateConflict {
		t.Fatalf("已结论检验单不得重结，实际 %s", code)
	}

	// 走正规复检：新抽样 + 新检验单，读数合格。
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: "SMP-2", SubjectType: "material_lot", SubjectRef: "LOT-A",
		Kind: "retest", RetestOfRef: "SMP-1", QtyMg: 1000, SamplerID: staff.inspector,
	}); err != nil {
		t.Fatalf("复检抽样失败: %v", err)
	}
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: "INP-2", SampleRef: "SMP-2", OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("复检开单失败: %v", err)
	}
	if _, err := env.svc.AddReading(ctx, "INP-2", quality.ReadingInput{
		TestItem: "含量测定", SpecMin: "90", SpecMax: "110", Unit: "%",
		ObservedValue: "98", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("复检读数失败: %v", err)
	}
	retest, err := env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: "INP-2", InspectorID: staff.inspector,
	})
	if err != nil || retest.Verdict != "qualified" {
		t.Fatalf("复检应判合格，verdict=%v err=%v", retest.Verdict, err)
	}

	// 关键断言：失败事实仍在，批次不得因复检合格而自动解锁。
	ledger, err := env.svc.LotLedger(ctx, "LOT-A")
	if err != nil {
		t.Fatalf("查台账失败: %v", err)
	}
	if !ledger.Lot.HadFailure {
		t.Fatal("第一次失败读数被复检覆盖：had_failure 被清除")
	}
	if ledger.Lot.State == "available" {
		t.Fatalf("出现过失败读数的批次复检后仍应挂起，实际状态 %s", ledger.Lot.State)
	}

	// 原始失败读数仍可逐笔查到（不可删除/修改）。
	foundFail := false
	for _, inspection := range ledger.Inspections {
		if inspection.InspectionRef != "INP-1" {
			continue
		}
		for _, reading := range inspection.Readings {
			if reading.Outcome == "fail" && reading.ObservedValue == "72" {
				foundFail = true
			}
		}
	}
	if !foundFail {
		t.Fatal("第一次失败读数 72 在复检后丢失")
	}
}

// 场景四：角色不可越权——配制负责人不能代替检验人签字，
// 检验人/配制负责人不能放行成品，仓储不能放行。
func TestRoleSeparation(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: "SMP-1", SubjectType: "material_lot", SubjectRef: "LOT-A",
		Kind: "initial", QtyMg: 1000, SamplerID: staff.warehouse,
	}); err != nil {
		t.Fatalf("抽样失败: %v", err)
	}

	// 配制负责人尝试开检验单——必须 403。
	_, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: "INP-X", SampleRef: "SMP-1", OpenedBy: staff.manager,
	})
	if code := ruleCode(t, err); code != quality.CodeRoleForbidden {
		t.Fatalf("配制负责人不得开检验单，实际 %s", code)
	}

	// 检验人正常开单、读数。
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: "INP-1", SampleRef: "SMP-1", OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("检验人开单失败: %v", err)
	}
	// 配制负责人尝试录入读数——必须 403。
	_, err = env.svc.AddReading(ctx, "INP-1", quality.ReadingInput{
		TestItem: "性状", SpecMin: "90", SpecMax: "110",
		ObservedValue: "100", InspectorID: staff.manager,
	})
	if code := ruleCode(t, err); code != quality.CodeRoleForbidden {
		t.Fatalf("配制负责人不得录入检验读数，实际 %s", code)
	}

	// 药事负责人尝试签检验结论——必须 403（只有检验人能签）。
	if _, err := env.svc.AddReading(ctx, "INP-1", quality.ReadingInput{
		TestItem: "性状", SpecMin: "90", SpecMax: "110",
		ObservedValue: "100", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("检验人读数失败: %v", err)
	}
	_, err = env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: "INP-1", InspectorID: staff.head,
	})
	if code := ruleCode(t, err); code != quality.CodeRoleForbidden {
		t.Fatalf("药事负责人不得代替检验人签结论，实际 %s", code)
	}
}

// 场景五：不合格样品必须阻断其所有下游批次的放行——
// 包含分装后代、合批后代、已投料未放行批次，以及已放行批次的召回。
func TestFailureBlocksAllDownstream(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)

	// LOT-A 先检验合格、分装成 A1/A2。
	mustPassInitial(t, env, "material_lot", "LOT-A", staff)
	if _, err := env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A", WeightMg: 990_000, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1", WeightMg: 600_000},
			{LotRef: "LOT-A2", WeightMg: 390_000},
		},
	}); err != nil {
		t.Fatalf("分装失败: %v", err)
	}

	// 开立成品批 B1：称量 300g A1 并投料。
	if _, err := env.svc.CreateCompoundBatch(ctx, quality.CreateCompoundBatchInput{
		BatchRef: "B1", FormulaRef: "F-BY", FormulaRevision: 1,
		ProductName: "补中益气颗粒", ManagerID: staff.manager,
	}); err != nil {
		t.Fatalf("开成品批失败: %v", err)
	}
	if _, err := env.svc.WeighMaterial(ctx, quality.WeighMaterialInput{
		BatchRef: "B1", LotRef: "LOT-A1", WeightMg: 300_000, WeigherID: staff.warehouse,
	}); err != nil {
		t.Fatalf("称量失败: %v", err)
	}
	if _, err := env.svc.ChargeBatch(ctx, quality.ChargeBatchInput{
		BatchRef: "B1", ManagerID: staff.manager,
	}); err != nil {
		t.Fatalf("投料失败: %v", err)
	}
	// 成品检验合格后药事负责人放行。
	mustPassBatchQC(t, env, "B1", staff)
	if _, err := env.svc.ReleaseBatch(ctx, quality.ReleaseBatchInput{
		BatchRef: "B1", HeadID: staff.head,
	}); err != nil {
		t.Fatalf("正常放行失败: %v", err)
	}

	// 开立成品批 B2：称量 A2 但暂不投料。
	if _, err := env.svc.CreateCompoundBatch(ctx, quality.CreateCompoundBatchInput{
		BatchRef: "B2", FormulaRef: "F-BY", FormulaRevision: 1,
		ProductName: "补中益气颗粒", ManagerID: staff.manager,
	}); err != nil {
		t.Fatalf("开成品批 B2 失败: %v", err)
	}
	if _, err := env.svc.WeighMaterial(ctx, quality.WeighMaterialInput{
		BatchRef: "B2", LotRef: "LOT-A2", WeightMg: 200_000, WeigherID: staff.warehouse,
	}); err != nil {
		t.Fatalf("B2 称量失败: %v", err)
	}

	// 此时对根批次 LOT-A 抽样复检发现不合格（污染可能在留样复检中暴露）。
	mustFailMaterial(t, env, "LOT-A", "INP-FAIL", "SMP-FAIL", staff)

	// 全部派生物料批次必须挂起并带失败标记。
	for _, ref := range []string{"LOT-A1", "LOT-A2"} {
		ledger, err := env.svc.LotLedger(ctx, ref)
		if err != nil {
			t.Fatalf("查 %s 失败: %v", ref, err)
		}
		if !ledger.Lot.HadFailure {
			t.Fatalf("%s 应随上游不合格被标记失败", ref)
		}
	}

	// B1 已放行 → 必须转召回，且不得重新放行。
	trace, err := env.svc.TraceBatch(ctx, "B1", staff.head)
	if err != nil {
		t.Fatalf("反查 B1 失败: %v", err)
	}
	if trace.Batch.Status != "recalled" || !trace.Batch.QualityBlocked {
		t.Fatalf("B1 应被召回并标记阻断，实际 status=%s blocked=%v",
			trace.Batch.Status, trace.Batch.QualityBlocked)
	}
	_, err = env.svc.ReleaseBatch(ctx, quality.ReleaseBatchInput{
		BatchRef: "B1", HeadID: staff.head,
	})
	if code := ruleCode(t, err); code != quality.CodeQualityBlocked {
		t.Fatalf("召回批次不得重新放行，实际 %s（%v）", code, err)
	}

	// 外部查询必须把已放行又被污染的批次反映为 recalled，而不是仍显示 released。
	publicView, err := env.svc.PublicBatchStatus(ctx, "B1")
	if err != nil {
		t.Fatalf("外部查询失败: %v", err)
	}
	if publicView.DispositionStatus != "recalled" {
		t.Fatalf("污染后外部处置状态应为 recalled，实际 %s", publicView.DispositionStatus)
	}

	// B2 称量后、投料前被阻断：投料必须失败。
	_, err = env.svc.ChargeBatch(ctx, quality.ChargeBatchInput{
		BatchRef: "B2", ManagerID: staff.manager,
	})
	if code := ruleCode(t, err); code != quality.CodeQualityBlocked {
		t.Fatalf("使用不合格物料的 B2 投料应被阻断，实际 %s（%v）", code, err)
	}

	// 派生物料也不能再被称量领用。
	_, err = env.svc.WeighMaterial(ctx, quality.WeighMaterialInput{
		BatchRef: "B2", LotRef: "LOT-A1", WeightMg: 1000, WeigherID: staff.warehouse,
	})
	if code := ruleCode(t, err); code != quality.CodeQualityBlocked {
		t.Fatalf("挂失败标记的物料应禁止称量，实际 %s", code)
	}
}

// 场景六：外部查询只能看到合格范围与处置状态，看不到供应商、读数、结论等。
func TestPublicViewIsMinimal(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	mustPassInitial(t, env, "material_lot", "LOT-A", staff)
	createWeighCharge(t, env, "B1", "LOT-A", 300_000, staff)
	mustPassBatchQC(t, env, "B1", staff)
	if _, err := env.svc.ReleaseBatch(ctx, quality.ReleaseBatchInput{
		BatchRef: "B1", HeadID: staff.head, Reason: "符合放行条件",
	}); err != nil {
		t.Fatalf("放行失败: %v", err)
	}

	view, err := env.svc.PublicBatchStatus(ctx, "B1")
	if err != nil {
		t.Fatalf("外部查询失败: %v", err)
	}
	if view.DispositionStatus != "released" {
		t.Fatalf("外部处置状态应为 released，实际 %s", view.DispositionStatus)
	}
	if len(view.QualifiedRanges) != 1 || view.QualifiedRanges[0].TestItem != "性状" {
		t.Fatalf("外部视图应只含 1 项合格范围，实际 %+v", view.QualifiedRanges)
	}
	serialized := mustJSON(t, view)
	for _, secret := range []string{"sup_gansu", "甘肃", "stf_", "observed", "100", "unqualified", "cert"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("外部视图泄露内部字段 %q：%s", secret, serialized)
		}
	}
}

// 场景七：药事负责人能从任一成品反查所用药材与每次质量判断。
func TestLineageTraceFromFinishedBatch(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	mustPassInitial(t, env, "material_lot", "LOT-A", staff)

	// 分装后合批，再用于成品，验证谱系能跨越两层移动还原。
	if _, err := env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A", WeightMg: 990_000, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1", WeightMg: 500_000},
			{LotRef: "LOT-A2", WeightMg: 490_000},
		},
	}); err != nil {
		t.Fatalf("分装失败: %v", err)
	}
	if _, err := env.svc.Merge(ctx, quality.MergeInput{
		TargetLotRef: "LOT-M", OperatorID: staff.warehouse,
		Sources: []quality.MergeSource{
			{LotRef: "LOT-A1", WeightMg: 200_000},
			{LotRef: "LOT-A2", WeightMg: 200_000},
		},
	}); err != nil {
		t.Fatalf("合批失败: %v", err)
	}
	// 合批后的新批次必须重新检验合格才能领用。
	mustPassInitial(t, env, "material_lot", "LOT-M", staff)
	createWeighCharge(t, env, "B1", "LOT-M", 350_000, staff)
	mustPassBatchQC(t, env, "B1", staff)
	if _, err := env.svc.ReleaseBatch(ctx, quality.ReleaseBatchInput{
		BatchRef: "B1", HeadID: staff.head,
	}); err != nil {
		t.Fatalf("放行失败: %v", err)
	}

	trace, err := env.svc.TraceBatch(ctx, "B1", staff.head)
	if err != nil {
		t.Fatalf("反查失败: %v", err)
	}
	if trace.Batch.BatchRef != "B1" || trace.Batch.FormulaRef != "F-BY" {
		t.Fatalf("成品信息错误: %+v", trace.Batch)
	}
	if len(trace.Chargings) != 1 || trace.Chargings[0].MaterialLotRef != "LOT-M" {
		t.Fatalf("投料明细应为 LOT-M，实际 %+v", trace.Chargings)
	}
	lotRefs := map[string]bool{}
	for _, lot := range trace.Lots {
		lotRefs[lot.LotRef] = true
	}
	for _, expected := range []string{"LOT-A", "LOT-A1", "LOT-A2", "LOT-M"} {
		if !lotRefs[expected] {
			t.Fatalf("谱系缺少物料批次 %s，实际 %v", expected, lotRefs)
		}
	}
	// 入厂凭证、每次质量判断、放行决策都应可反查。
	if trace.Lots[0].Inbound == nil && trace.Lots[len(trace.Lots)-1].Inbound == nil {
		// 任一根批次带凭证即可；这里直接全量找。
	}
	foundInbound := false
	foundInspection := false
	for _, lot := range trace.Lots {
		if lot.Inbound != nil && lot.Inbound.LotRef == "LOT-A" {
			foundInbound = true
		}
		for _, inspection := range lot.Inspections {
			if inspection.Verdict == "qualified" {
				foundInspection = true
			}
		}
	}
	if !foundInbound {
		t.Fatal("反查结果缺少 LOT-A 的入厂凭证")
	}
	if !foundInspection {
		t.Fatal("反查结果缺少原料合格检验记录")
	}
	if len(trace.Releases) != 1 || trace.Releases[0].Decision != "released" {
		t.Fatalf("放行决策反查错误: %+v", trace.Releases)
	}
	if len(trace.Movements) != 2 {
		t.Fatalf("应反查到分装、合批两笔移动，实际 %d", len(trace.Movements))
	}

	// 非药事负责人不能看完整谱系。
	_, err = env.svc.TraceBatch(ctx, "B1", staff.manager)
	if code := ruleCode(t, err); code != quality.CodeRoleForbidden {
		t.Fatalf("配制负责人不得查看完整放行谱系，实际 %s", code)
	}
}

// 场景八：未取得成品合格检验结论不得放行。
func TestReleaseRequiresFinishedQC(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	mustPassInitial(t, env, "material_lot", "LOT-A", staff)
	createWeighCharge(t, env, "B1", "LOT-A", 300_000, staff)
	// 不做成品检验直接放行。
	_, err := env.svc.ReleaseBatch(ctx, quality.ReleaseBatchInput{
		BatchRef: "B1", HeadID: staff.head,
	})
	if code := ruleCode(t, err); code != quality.CodeStateConflict {
		t.Fatalf("无成品检验结论应阻止放行，实际 %s（%v）", code, err)
	}
}

// 场景九：抽样耗料也入台账，不能超过余量。
func TestSamplingConsumesLedger(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	_, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: "SMP-BIG", SubjectType: "material_lot", SubjectRef: "LOT-A",
		Kind: "initial", QtyMg: 2_000_000, SamplerID: staff.warehouse,
	})
	if code := ruleCode(t, err); code != quality.CodeWeightMismatch {
		t.Fatalf("超量抽样应被重量核对拦截，实际 %s", code)
	}
}

func TestTimeFormatHasOffset(t *testing.T) {
	env := newTestEnv(t)
	_ = env
	if _, err := time.Parse(time.RFC3339, env.now.Format(time.RFC3339)); err != nil {
		t.Fatalf("时间不符合 ISO 8601 带偏移格式: %v", err)
	}
}

// 场景十：从一袋不合格原料精确定位实际受影响的成品，而不是停用整个系列。
func TestAffectedBatchesPinpointsFinishedProducts(t *testing.T) {
	env := newTestEnv(t)
	staff := env.seedWorld(t)
	ctx := context.Background()
	env.receiveRoot(t, "LOT-A", staff.warehouse)
	mustPassInitial(t, env, "material_lot", "LOT-A", staff)
	if _, err := env.svc.Split(ctx, quality.SplitInput{
		SourceLotRef: "LOT-A", WeightMg: 990_000, OperatorID: staff.warehouse,
		Parts: []quality.SplitPart{
			{LotRef: "LOT-A1", WeightMg: 500_000},
			{LotRef: "LOT-A2", WeightMg: 490_000},
		},
	}); err != nil {
		t.Fatalf("分装失败: %v", err)
	}
	createWeighCharge(t, env, "B-A1", "LOT-A1", 200_000, staff)

	// 另一袋无关原料与成品，不应被波及。
	if _, err := env.svc.ReceiveInboundLot(ctx, quality.ReceiveInboundLotInput{
		LotRef: "LOT-B", SupplierID: "sup_gansu", MaterialCode: "MAT-HQ",
		NetWeightMg: 1_000_000, ReceiverID: staff.warehouse,
	}); err != nil {
		t.Fatalf("收 LOT-B 失败: %v", err)
	}
	mustPassInitial(t, env, "material_lot", "LOT-B", staff)
	createWeighCharge(t, env, "B-OTHER", "LOT-B", 200_000, staff)

	affected, err := env.svc.AffectedBatches(ctx, "LOT-A", staff.head)
	if err != nil {
		t.Fatalf("查询受影响成品失败: %v", err)
	}
	if len(affected) != 1 || affected[0].BatchRef != "B-A1" {
		t.Fatalf("LOT-A 应只影响 B-A1，实际 %+v", affected)
	}
	if affected[0].Relationship != "downstream" {
		t.Fatalf("经分装后代投料应为 downstream，实际 %s", affected[0].Relationship)
	}
}
