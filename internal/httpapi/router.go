package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
)

// API 装配领域服务与路由。
type API struct {
	service *quality.Service
}

func NewAPI(service *quality.Service) *API {
	return &API{service: service}
}

func (api *API) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// 基础资料
	mux.HandleFunc("POST /v1/staff", api.registerStaff)
	mux.HandleFunc("POST /v1/materials", api.registerMaterial)
	mux.HandleFunc("POST /v1/suppliers", api.registerSupplier)
	mux.HandleFunc("POST /v1/suppliers/{supplier_id}/certificates", api.addCertificate)

	// 入厂、分装、合批
	mux.HandleFunc("POST /v1/inbound-lots", api.receiveInboundLot)
	mux.HandleFunc("POST /v1/material-lots/{lot_ref}/split", api.splitLot)
	mux.HandleFunc("POST /v1/material-lots/merge", api.mergeLots)
	mux.HandleFunc("POST /v1/material-lots/{lot_ref}/disposals", api.disposeLot)

	// 抽样、检验
	mux.HandleFunc("POST /v1/samples", api.drawSample)
	mux.HandleFunc("POST /v1/inspections", api.openInspection)
	mux.HandleFunc("POST /v1/inspections/{inspection_ref}/readings", api.addReading)
	mux.HandleFunc("POST /v1/inspections/{inspection_ref}/conclude", api.concludeInspection)

	// 配制、称量、投料、放行
	mux.HandleFunc("POST /v1/compound-batches", api.createCompoundBatch)
	mux.HandleFunc("POST /v1/compound-batches/{batch_ref}/weighings", api.weighMaterial)
	mux.HandleFunc("POST /v1/compound-batches/{batch_ref}/charge", api.chargeBatch)
	mux.HandleFunc("POST /v1/compound-batches/{batch_ref}/release", api.releaseBatch)
	mux.HandleFunc("POST /v1/compound-batches/{batch_ref}/hold", api.holdBatch)
	mux.HandleFunc("POST /v1/compound-batches/{batch_ref}/reject", api.rejectBatch)

	// 查询
	mux.HandleFunc("GET /v1/internal/compound-batches/{batch_ref}/lineage", api.traceLineage)
	mux.HandleFunc("GET /v1/internal/material-lots/{lot_ref}/ledger", api.lotLedger)
	mux.HandleFunc("GET /v1/internal/material-lots/{lot_ref}/affected-batches", api.affectedBatches)
	// 外部查询：只返回合格范围与处置状态
	mux.HandleFunc("GET /v1/public/compound-batches/{batch_ref}", api.publicBatchStatus)

	return logRequests(mux)
}

// Router 保持既有入口：未装配服务时返回仅含健康检查的路由。
func Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, err error) {
	body := errorBody{}
	status := http.StatusInternalServerError
	if ruleErr, ok := quality.AsRuleError(err); ok {
		body.Error.Code = ruleErr.Code
		body.Error.Message = ruleErr.Msg
		switch ruleErr.Code {
		case quality.CodeValidation:
			status = http.StatusBadRequest
		case quality.CodeNotFound:
			status = http.StatusNotFound
		case quality.CodeConflict, quality.CodeStateConflict, quality.CodeWeightMismatch:
			status = http.StatusConflict
		case quality.CodeRoleForbidden, quality.CodeSignature:
			status = http.StatusForbidden
		case quality.CodeQualityBlocked:
			status = http.StatusUnprocessableEntity
		}
	} else {
		body.Error.Code = "internal_error"
		body.Error.Message = "内部错误"
		log.Printf("未预期错误: %v", err)
	}
	writeJSON(w, status, body)
}

func decode(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("请求体不是合法 JSON 或含未知字段")
	}
	return nil
}
