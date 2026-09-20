package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/domain"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// harness 提供内存 SQLite、已迁移的库、四类岗位账号与 JSON 请求助手。
type harness struct {
	t       *testing.T
	db      *sql.DB
	handler http.Handler
	users   map[string]string // 角色 -> 用户工号
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	h := &harness{t: t, db: db, handler: New(domain.NewService(db)),
		users: map[string]string{}}

	// 首位账号通过自举接口建立，其余由首位账号创建。
	first := map[string]string{"id": "u-wh", "name": "王库管", "role": "warehouse"}
	code, _ := h.post("/v1/bootstrap", "", first)
	if code != http.StatusCreated {
		t.Fatalf("自举首位用户失败: %d", code)
	}
	others := []map[string]string{
		{"id": "u-insp", "name": "李检验", "role": "inspector"},
		{"id": "u-insp2", "name": "张复验", "role": "inspector"},
		{"id": "u-lead", "name": "赵配制", "role": "compounding_lead"},
		{"id": "u-qa", "name": "孙药事", "role": "qa_release"},
	}
	for _, u := range others {
		code, body := h.post("/v1/users", "u-wh", u)
		if code != http.StatusCreated {
			t.Fatalf("创建用户 %s 失败: %d %v", u["id"], code, body)
		}
	}
	h.users = map[string]string{
		"warehouse": "u-wh", "inspector": "u-insp", "inspector2": "u-insp2",
		"lead": "u-lead", "qa": "u-qa",
	}
	return h
}

// request 发起一次 HTTP 调用，返回状态码与解码后的 JSON（nil 表示非 JSON）。
func (h *harness) request(method, path, user string, payload any) (int, map[string]any) {
	h.t.Helper()
	var reader *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			h.t.Fatalf("编码请求体失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-User-ID", user)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	}
	return rec.Code, decoded
}

func (h *harness) get(path, user string) (int, map[string]any) {
	return h.request(http.MethodGet, path, user, nil)
}

func (h *harness) post(path, user string, payload any) (int, map[string]any) {
	return h.request(http.MethodPost, path, user, payload)
}

func expectCode(t *testing.T, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("状态码 = %d, 期望 %d，响应 = %v", got, want, body)
	}
}
