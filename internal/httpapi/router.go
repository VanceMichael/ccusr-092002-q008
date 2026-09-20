package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/domain"
)

// API 组装路由。内部接口要求 X-User-ID；/public 下的外部接口不鉴权且字段最小化。
type API struct {
	svc *domain.Service
}

func New(svc *domain.Service) http.Handler {
	api := &API{svc: svc}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// 一次性自举：仅当系统中还没有任何用户时可用。
	mux.HandleFunc("POST /v1/bootstrap", api.bootstrap)
	mux.HandleFunc("POST /v1/users", requireHeader(api.createUser))

	// 主数据
	mux.HandleFunc("POST /v1/materials", requireHeader(api.createMaterial))
	mux.HandleFunc("POST /v1/formulas", requireHeader(api.createFormula))

	// 来货 → 抽样 → 检验 → 处置
	mux.HandleFunc("POST /v1/receipts", requireHeader(api.registerReceipt))
	mux.HandleFunc("GET /v1/receipts/{ref}", requireHeader(api.getReceipt))
	mux.HandleFunc("POST /v1/receipts/{ref}/samples", requireHeader(api.takeSample))
	mux.HandleFunc("POST /v1/samples/{ref}/inspections", requireHeader(api.recordInspection))
	mux.HandleFunc("POST /v1/receipts/{ref}/decision", requireHeader(api.decideReceipt))

	// 分装 / 合批
	mux.HandleFunc("POST /v1/receipts/{ref}/dispense", requireHeader(api.dispense))
	mux.HandleFunc("POST /v1/merges", requireHeader(api.merge))

	// 配制批次：投料 → 完成配制 → 成品检验 → 放行
	mux.HandleFunc("POST /v1/batches", requireHeader(api.createBatch))
	mux.HandleFunc("GET /v1/batches/{ref}", requireHeader(api.getBatch))
	mux.HandleFunc("POST /v1/batches/{ref}/weighings", requireHeader(api.addWeighing))
	mux.HandleFunc("POST /v1/batches/{ref}/finish", requireHeader(api.finishBatch))
	mux.HandleFunc("POST /v1/batches/{ref}/inspections", requireHeader(api.finishedInspection))
	mux.HandleFunc("POST /v1/batches/{ref}/release", requireHeader(api.releaseBatch))

	// 追溯与核对
	mux.HandleFunc("GET /v1/batches/{ref}/lineage", requireHeader(api.lineage))
	mux.HandleFunc("GET /v1/receipts/{ref}/impact", requireHeader(api.impact))
	mux.HandleFunc("GET /v1/weight-check", requireHeader(api.weightCheck))

	// 外部查询：无身份信息，只返回合格范围与处置状态。
	mux.HandleFunc("GET /public/v1/receipts/{ref}", api.publicReceipt)
	mux.HandleFunc("GET /public/v1/batches/{ref}", api.publicBatch)

	return mux
}

// actorID 从请求头取出操作人工号。
func actorID(r *http.Request) string { return r.Header.Get("X-User-ID") }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError 把领域错误映射为对应 HTTP 状态码；未知错误按 500 处理且不回显细节。
func writeError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if errors.As(err, &de) {
		writeJSON(w, de.Status, map[string]string{"code": de.Code, "message": de.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"code":    "internal_error",
		"message": "服务器内部错误",
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"code": "invalid_json", "message": "请求体不是合法 JSON 或含未知字段: " + err.Error()})
		return false
	}
	return true
}

// handler 是携带操作者工号的内部处理函数签名。
type handler func(w http.ResponseWriter, r *http.Request, caller string)

// requireHeader 强制内部接口携带 X-User-ID；具体角色由领域服务判定。
func requireHeader(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-User-ID") == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"code": "unauthenticated", "message": "缺少 X-User-ID 请求头"})
			return
		}
		h(w, r, r.Header.Get("X-User-ID"))
	}
}
