package quality_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
)

// createWeighCharge 开立成品批、称量指定物料并完成投料（不做成品检验）。
func createWeighCharge(t *testing.T, env *testEnv, batchRef, lotRef string, weightMg int64, staff staffIDs) {
	t.Helper()
	ctx := context.Background()
	if _, err := env.svc.CreateCompoundBatch(ctx, quality.CreateCompoundBatchInput{
		BatchRef: batchRef, FormulaRef: "F-BY", FormulaRevision: 1,
		ProductName: "补中益气颗粒", ManagerID: staff.manager,
	}); err != nil {
		t.Fatalf("开立成品批失败: %v", err)
	}
	if _, err := env.svc.WeighMaterial(ctx, quality.WeighMaterialInput{
		BatchRef: batchRef, LotRef: lotRef, WeightMg: weightMg, WeigherID: staff.warehouse,
	}); err != nil {
		t.Fatalf("称量失败: %v", err)
	}
	if _, err := env.svc.ChargeBatch(ctx, quality.ChargeBatchInput{
		BatchRef: batchRef, ManagerID: staff.manager,
	}); err != nil {
		t.Fatalf("投料失败: %v", err)
	}
}

// mustPassBatchQC 对成品批次做一次合格的成品检验。
func mustPassBatchQC(t *testing.T, env *testEnv, batchRef string, staff staffIDs) {
	t.Helper()
	ctx := context.Background()
	sampleRef := "smp_batch_" + batchRef
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: sampleRef, SubjectType: "compound_batch", SubjectRef: batchRef,
		Kind: "initial", QtyMg: 100, SamplerID: staff.warehouse,
	}); err != nil {
		t.Fatalf("成品抽样失败: %v", err)
	}
	inspRef := "inp_batch_" + batchRef
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: inspRef, SampleRef: sampleRef, OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("成品开检验单失败: %v", err)
	}
	if _, err := env.svc.AddReading(ctx, inspRef, quality.ReadingInput{
		TestItem: "性状", SpecMin: "90", SpecMax: "110", Unit: "%",
		ObservedValue: "100", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("成品读数失败: %v", err)
	}
	if result, err := env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: inspRef, InspectorID: staff.inspector,
	}); err != nil || result.Verdict != "qualified" {
		t.Fatalf("成品检验应合格，verdict=%v err=%v", result.Verdict, err)
	}
}

// mustFailMaterial 对物料批次做一次判定不合格的检验（独立编号，避免冲突）。
func mustFailMaterial(t *testing.T, env *testEnv, lotRef, inspRef, sampleRef string, staff staffIDs) {
	t.Helper()
	ctx := context.Background()
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: sampleRef, SubjectType: "material_lot", SubjectRef: lotRef,
		Kind: "initial", QtyMg: 1000, SamplerID: staff.inspector,
	}); err != nil {
		t.Fatalf("不合格检验抽样失败: %v", err)
	}
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: inspRef, SampleRef: sampleRef, OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("不合格检验开单失败: %v", err)
	}
	if _, err := env.svc.AddReading(ctx, inspRef, quality.ReadingInput{
		TestItem: "重金属", SpecMin: "0", SpecMax: "10", Unit: "mg/kg",
		ObservedValue: "25", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("失败读数录入失败: %v", err)
	}
	result, err := env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: inspRef, InspectorID: staff.inspector,
	})
	if err != nil || result.Verdict != "unqualified" {
		t.Fatalf("检验应判不合格，verdict=%v err=%v", result.Verdict, err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(raw)
}
