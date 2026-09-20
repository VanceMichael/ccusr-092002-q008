package quality_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/migrate"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// testEnv 装配一个使用临时 SQLite 文件的服务（走真实 SQL 与触发器）。
type testEnv struct {
	svc *quality.Service
	db  *sql.DB
	now time.Time
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Up(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	svc := quality.NewService(store.New(db))
	fixed := time.Date(2026, 9, 20, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	svc.SetClock(func() time.Time { return fixed })
	return &testEnv{svc: svc, db: db, now: fixed}
}

// seedWorld 建立四个岗位员工、一个药材品种、一个供应商。
func (env *testEnv) seedWorld(t *testing.T) staffIDs {
	t.Helper()
	ctx := context.Background()
	ids := staffIDs{
		warehouse: "stf_warehouse",
		inspector: "stf_inspector",
		manager:   "stf_manager",
		head:      "stf_head",
	}
	for _, role := range []struct{ id, name, role string }{
		{ids.warehouse, "仓管员", quality.RoleWarehouse},
		{ids.inspector, "检验师", quality.RoleInspector},
		{ids.manager, "配制负责人", quality.RoleProductionManager},
		{ids.head, "药事负责人", quality.RolePharmacyHead},
	} {
		if _, err := env.svc.RegisterStaff(ctx, quality.RegisterStaffInput{
			ID: role.id, Name: role.name, Role: role.role,
		}); err != nil {
			t.Fatalf("登记员工失败: %v", err)
		}
	}
	if _, err := env.svc.RegisterMaterial(ctx, quality.RegisterMaterialInput{
		Code: "MAT-HQ", Name: "黄芪",
	}); err != nil {
		t.Fatalf("登记药材失败: %v", err)
	}
	if _, err := env.svc.RegisterSupplier(ctx, quality.RegisterSupplierInput{
		ID: "sup_gansu", Name: "甘肃某供应商", Origin: "甘肃陇西",
		LicenseScope: "中药材批发", OperatorID: ids.warehouse,
	}); err != nil {
		t.Fatalf("登记供应商失败: %v", err)
	}
	if _, err := env.svc.AddCertificate(ctx, quality.AddCertificateInput{
		SupplierID: "sup_gansu", CertType: "生产许可证", CertRef: "CERT-001",
		LicenseScope: "中药材批发", OperatorID: ids.warehouse,
	}); err != nil {
		t.Fatalf("登记凭证失败: %v", err)
	}
	return ids
}

type staffIDs struct {
	warehouse string
	inspector string
	manager   string
	head      string
}

// receiveRoot 收一袋 1 000 000 mg（1 kg）的黄芪。
func (env *testEnv) receiveRoot(t *testing.T, lotRef string, receiver string) {
	t.Helper()
	if _, err := env.svc.ReceiveInboundLot(context.Background(), quality.ReceiveInboundLotInput{
		LotRef: lotRef, SupplierID: "sup_gansu", MaterialCode: "MAT-HQ",
		NetWeightMg: 1_000_000, ReceiverID: receiver,
	}); err != nil {
		t.Fatalf("入厂收货失败: %v", err)
	}
}

func ruleCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("预期返回规则错误，实际为 nil")
	}
	ruleErr, ok := quality.AsRuleError(err)
	if !ok {
		t.Fatalf("预期领域规则错误，实际为 %T: %v", err, err)
	}
	return ruleErr.Code
}

func mustPassInitial(t *testing.T, env *testEnv, subjectType, subjectRef string, staff staffIDs) {
	t.Helper()
	ctx := context.Background()
	sampleRef := "smp_" + subjectRef
	if _, err := env.svc.DrawSample(ctx, quality.DrawSampleInput{
		SampleRef: sampleRef, SubjectType: subjectType, SubjectRef: subjectRef,
		Kind: "initial", QtyMg: 1000, SamplerID: staff.warehouse,
	}); err != nil {
		t.Fatalf("抽样失败: %v", err)
	}
	inspRef := "inp_" + subjectRef
	if _, err := env.svc.OpenInspection(ctx, quality.OpenInspectionInput{
		InspectionRef: inspRef, SampleRef: sampleRef, OpenedBy: staff.inspector,
	}); err != nil {
		t.Fatalf("开检验单失败: %v", err)
	}
	if _, err := env.svc.AddReading(ctx, inspRef, quality.ReadingInput{
		TestItem: "性状", SpecMin: "90", SpecMax: "110", Unit: "%",
		ObservedValue: "100", InspectorID: staff.inspector,
	}); err != nil {
		t.Fatalf("录入合格读数失败: %v", err)
	}
	if result, err := env.svc.ConcludeInspection(ctx, quality.ConcludeInspectionInput{
		InspectionRef: inspRef, InspectorID: staff.inspector,
	}); err != nil || result.Verdict != "qualified" {
		t.Fatalf("初次检验应合格，verdict=%v err=%v", result.Verdict, err)
	}
}
