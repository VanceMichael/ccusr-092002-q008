package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/httpapi"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/migrate"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

type apiHarness struct {
	t      *testing.T
	server http.Handler
	db     *sql.DB
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api.sqlite3")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate.Up(db); err != nil {
		t.Fatal(err)
	}
	api := httpapi.NewAPI(quality.NewService(store.New(db)))
	return &apiHarness{t: t, server: api.Router(), db: db}
}

func (h *apiHarness) request(method, path, body string) (int, map[string]any) {
	h.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	var parsed map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			h.t.Fatalf("响应不是 JSON: %v (body=%s)", err, string(raw))
		}
	}
	return rec.Code, parsed
}

// 完整放行链路：入厂 → 检验合格 → 称量 → 投料 → 成品检验 → 药事负责人放行 → 外部查询。
func TestFullReleaseWorkflowOverHTTP(t *testing.T) {
	h := newAPIHarness(t)

	mustStatus := func(status, want int) {
		t.Helper()
		if status != want {
			t.Fatalf("HTTP 状态码应为 %d，实际 %d", want, status)
		}
	}

	for _, staff := range []string{
		`{"id":"wh","name":"仓管","role":"warehouse"}`,
		`{"id":"qc","name":"检验师","role":"inspector"}`,
		`{"id":"pm","name":"配制负责人","role":"production_manager"}`,
		`{"id":"ph","name":"药事负责人","role":"pharmacy_head"}`,
	} {
		status, _ := h.request("POST", "/v1/staff", staff)
		mustStatus(status, http.StatusCreated)
	}
	status, _ := h.request("POST", "/v1/materials", `{"code":"HQ","name":"黄芪"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/suppliers",
		`{"id":"S1","name":"陇西药商","origin":"甘肃","license_scope":"中药材","operator_id":"wh"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/suppliers/S1/certificates",
		`{"cert_type":"许可证","cert_ref":"C1","license_scope":"中药材","operator_id":"wh"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inbound-lots",
		`{"lot_ref":"LOT-1","supplier_id":"S1","material_code":"HQ","net_weight_mg":1000000,"receiver_id":"wh"}`)
	mustStatus(status, http.StatusCreated)

	// 原料抽样、检验合格。
	status, _ = h.request("POST", "/v1/samples",
		`{"sample_ref":"SMP-1","subject_type":"material_lot","subject_ref":"LOT-1","kind":"initial","qty_mg":1000,"sampler_id":"wh"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inspections",
		`{"inspection_ref":"INP-1","sample_ref":"SMP-1","opened_by":"qc"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inspections/INP-1/readings",
		`{"test_item":"含量","spec_min":"90","spec_max":"110","unit":"%","observed_value":"101","inspector_id":"qc"}`)
	mustStatus(status, http.StatusCreated)
	status, body := h.request("POST", "/v1/inspections/INP-1/conclude", `{"inspector_id":"qc"}`)
	mustStatus(status, http.StatusOK)
	if body["verdict"] != "qualified" {
		t.Fatalf("原料检验应合格: %v", body)
	}

	// 开立成品批、称量、投料。
	status, _ = h.request("POST", "/v1/compound-batches",
		`{"batch_ref":"B1","formula_ref":"F1","formula_revision":1,"product_name":"颗粒","manager_id":"pm"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/compound-batches/B1/weighings",
		`{"lot_ref":"LOT-1","weight_mg":500000,"weigher_id":"wh"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/compound-batches/B1/charge", `{"manager_id":"pm"}`)
	mustStatus(status, http.StatusOK)

	// 成品检验合格。
	status, _ = h.request("POST", "/v1/samples",
		`{"sample_ref":"SMP-B1","subject_type":"compound_batch","subject_ref":"B1","kind":"initial","qty_mg":50,"sampler_id":"wh"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inspections",
		`{"inspection_ref":"INP-B1","sample_ref":"SMP-B1","opened_by":"qc"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inspections/INP-B1/readings",
		`{"test_item":"水分","spec_min":"0","spec_max":"9","unit":"%","observed_value":"5","inspector_id":"qc"}`)
	mustStatus(status, http.StatusCreated)
	status, _ = h.request("POST", "/v1/inspections/INP-B1/conclude", `{"inspector_id":"qc"}`)
	mustStatus(status, http.StatusOK)

	// 配制负责人试图放行——必须 403。
	status, body = h.request("POST", "/v1/compound-batches/B1/release", `{"head_id":"pm"}`)
	if status != http.StatusForbidden {
		t.Fatalf("配制负责人放行应 403，实际 %d（%v）", status, body)
	}
	// 药事负责人放行。
	status, body = h.request("POST", "/v1/compound-batches/B1/release", `{"head_id":"ph"}`)
	mustStatus(status, http.StatusOK)
	if body["decision"] != "released" {
		t.Fatalf("决策应为 released: %v", body)
	}

	// 外部查询：只含合格范围与处置状态。
	status, body = h.request("GET", "/v1/public/compound-batches/B1", "")
	mustStatus(status, http.StatusOK)
	if body["disposition_status"] != "released" {
		t.Fatalf("外部处置状态错误: %v", body["disposition_status"])
	}
	raw, _ := json.Marshal(body)
	for _, secret := range []string{"S1", "陇西", "LOT-1", "\"wh\"", "\"qc\"", "observed_value", "500000", "license"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("外部视图泄露内部信息 %q: %s", secret, string(raw))
		}
	}
	ranges, ok := body["qualified_ranges"].([]any)
	if !ok || len(ranges) != 1 {
		t.Fatalf("外部合格范围应为 1 项: %v", body["qualified_ranges"])
	}

	// 内部谱系：非药事负责人 403，药事负责人可反查。
	status, _ = h.request("GET", "/v1/internal/compound-batches/B1/lineage?viewer_id=pm", "")
	if status != http.StatusForbidden {
		t.Fatalf("内部谱系对配制负责人应 403，实际 %d", status)
	}
	status, body = h.request("GET", "/v1/internal/compound-batches/B1/lineage?viewer_id=ph", "")
	mustStatus(status, http.StatusOK)
	raw, _ = json.Marshal(body)
	for _, must := range []string{"LOT-1", "INP-1", "observed_value", "release_decisions", "陇西"} {
		if !strings.Contains(string(raw), must) {
			t.Fatalf("内部谱系缺少 %q", must)
		}
	}
}

// 重量不守恒返回 409。
func TestSplitWeightMismatchHTTP(t *testing.T) {
	h := newAPIHarness(t)
	h.request("POST", "/v1/staff", `{"id":"wh","name":"仓管","role":"warehouse"}`)
	h.request("POST", "/v1/materials", `{"code":"HQ","name":"黄芪"}`)
	h.request("POST", "/v1/suppliers",
		`{"id":"S1","name":"药商","origin":"甘肃","license_scope":"中药材","operator_id":"wh"}`)
	h.request("POST", "/v1/inbound-lots",
		`{"lot_ref":"L1","supplier_id":"S1","material_code":"HQ","net_weight_mg":1000000,"receiver_id":"wh"}`)

	status, body := h.request("POST", "/v1/material-lots/L1/split", `{
		"weight_mg": 1000000,
		"parts": [{"lot_ref":"L1A","weight_mg":900000}],
		"operator_id":"wh"
	}`)
	if status != http.StatusConflict {
		t.Fatalf("重量不守恒应 409，实际 %d（%v）", status, body)
	}
	if code, _ := body["error"].(map[string]any)["code"].(string); code != "weight_mismatch" {
		t.Fatalf("错误码应为 weight_mismatch: %v", body)
	}
}

// 未知字段请求体应被拒绝。
func TestUnknownFieldsRejected(t *testing.T) {
	h := newAPIHarness(t)
	status, _ := h.request("POST", "/v1/staff", `{"id":"x","name":"X","role":"warehouse","extra":1}`)
	if status != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", status)
	}
}

// 数据库触发器层面保证检验读数追加只读：即使绕过服务直接 UPDATE/DELETE 也会失败。
func TestReadingImmutableAtDatabaseLevel(t *testing.T) {
	h := newAPIHarness(t)
	h.request("POST", "/v1/staff", `{"id":"wh","name":"仓管","role":"warehouse"}`)
	h.request("POST", "/v1/staff", `{"id":"qc","name":"检验师","role":"inspector"}`)
	h.request("POST", "/v1/materials", `{"code":"HQ","name":"黄芪"}`)
	h.request("POST", "/v1/suppliers",
		`{"id":"S1","name":"药商","origin":"甘肃","license_scope":"中药材","operator_id":"wh"}`)
	h.request("POST", "/v1/inbound-lots",
		`{"lot_ref":"L1","supplier_id":"S1","material_code":"HQ","net_weight_mg":1000000,"receiver_id":"wh"}`)
	h.request("POST", "/v1/samples",
		`{"sample_ref":"S1","subject_type":"material_lot","subject_ref":"L1","kind":"initial","qty_mg":1000,"sampler_id":"wh"}`)
	h.request("POST", "/v1/inspections", `{"inspection_ref":"I1","sample_ref":"S1","opened_by":"qc"}`)
	h.request("POST", "/v1/inspections/I1/readings",
		`{"test_item":"x","spec_min":"1","spec_max":"9","observed_value":"5","inspector_id":"qc"}`)

	if _, err := h.db.Exec(`UPDATE test_readings SET observed_value='8' WHERE id LIKE 'rdg_%' LIMIT 1`); err == nil {
		t.Fatal("直接修改检验读数应被数据库触发器拒绝")
	}
	if _, err := h.db.Exec(`DELETE FROM test_readings`); err == nil {
		t.Fatal("直接删除检验读数应被数据库触发器拒绝")
	}
}
