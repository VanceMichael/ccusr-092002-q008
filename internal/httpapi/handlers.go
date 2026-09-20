package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

func errorEnvelope(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}

func (api *API) registerStaff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	staff, err := api.service.RegisterStaff(r.Context(), quality.RegisterStaffInput{
		ID: body.ID, Name: body.Name, Role: body.Role,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": staff.ID, "name": staff.Name, "role": staff.Role, "active": staff.Active,
	})
}

func (api *API) registerMaterial(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	material, err := api.service.RegisterMaterial(r.Context(), quality.RegisterMaterialInput{
		Code: body.Code, Name: body.Name,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": material.ID, "code": material.Code, "name": material.Name,
	})
}

func (api *API) registerSupplier(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Origin       string `json:"origin"`
		LicenseScope string `json:"license_scope"`
		OperatorID   string `json:"operator_id"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	supplier, err := api.service.RegisterSupplier(r.Context(), quality.RegisterSupplierInput{
		ID: body.ID, Name: body.Name, Origin: body.Origin,
		LicenseScope: body.LicenseScope, OperatorID: body.OperatorID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": supplier.ID, "name": supplier.Name, "origin": supplier.Origin,
		"license_scope": supplier.LicenseScope,
	})
}

func (api *API) addCertificate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CertType     string `json:"cert_type"`
		CertRef      string `json:"cert_ref"`
		LicenseScope string `json:"license_scope"`
		IssuedAt     string `json:"issued_at"`
		ExpiresAt    string `json:"expires_at"`
		OperatorID   string `json:"operator_id"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	issuedAt, err := parseOptionalTime(body.IssuedAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "issued_at 时间格式错误"))
		return
	}
	expiresAt, err := parseOptionalTime(body.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "expires_at 时间格式错误"))
		return
	}
	cert, err := api.service.AddCertificate(r.Context(), quality.AddCertificateInput{
		SupplierID: r.PathValue("supplier_id"), CertType: body.CertType,
		CertRef: body.CertRef, LicenseScope: body.LicenseScope,
		IssuedAt: issuedAt, ExpiresAt: expiresAt, OperatorID: body.OperatorID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": cert.ID, "supplier_id": cert.SupplierID,
		"cert_type": cert.CertType, "cert_ref": cert.CertRef,
	})
}

func (api *API) receiveInboundLot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LotRef       string `json:"lot_ref"`
		SupplierID   string `json:"supplier_id"`
		MaterialCode string `json:"material_code"`
		NetWeightMg  int64  `json:"net_weight_mg"`
		ReceivedAt   string `json:"received_at"`
		ReceiverID   string `json:"receiver_id"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	receivedAt, err := parseOptionalTime(body.ReceivedAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "received_at 时间格式错误"))
		return
	}
	result, err := api.service.ReceiveInboundLot(r.Context(), quality.ReceiveInboundLotInput{
		LotRef: body.LotRef, SupplierID: body.SupplierID, MaterialCode: body.MaterialCode,
		NetWeightMg: body.NetWeightMg, ReceivedAt: derefTime(receivedAt), ReceiverID: body.ReceiverID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"inbound_lot_id": result.Inbound.ID,
		"lot_ref":        result.Lot.LotRef,
		"material":       map[string]string{"code": result.Material.Code, "name": result.Material.Name},
		"net_weight_mg":  result.Inbound.NetWeightMg,
		"state":          result.Lot.State,
	})
}

func (api *API) splitLot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WeightMg int64 `json:"weight_mg"`
		Parts    []struct {
			LotRef   string `json:"lot_ref"`
			WeightMg int64  `json:"weight_mg"`
		} `json:"parts"`
		OperatorID string `json:"operator_id"`
		Remark     string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	parts := make([]quality.SplitPart, 0, len(body.Parts))
	for _, part := range body.Parts {
		parts = append(parts, quality.SplitPart{LotRef: part.LotRef, WeightMg: part.WeightMg})
	}
	result, err := api.service.Split(r.Context(), quality.SplitInput{
		SourceLotRef: r.PathValue("lot_ref"), WeightMg: body.WeightMg,
		Parts: parts, OperatorID: body.OperatorID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	partRefs := make([]map[string]any, 0, len(result.Parts))
	for _, part := range result.Parts {
		partRefs = append(partRefs, map[string]any{
			"lot_ref": part.LotRef, "initial_weight_mg": part.InitialWeightMg, "state": part.State,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"movement_id": result.MovementID, "parts": partRefs,
	})
}

func (api *API) mergeLots(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetLotRef string `json:"target_lot_ref"`
		Sources      []struct {
			LotRef   string `json:"lot_ref"`
			WeightMg int64  `json:"weight_mg"`
		} `json:"sources"`
		OperatorID string `json:"operator_id"`
		Remark     string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	sources := make([]quality.MergeSource, 0, len(body.Sources))
	for _, src := range body.Sources {
		sources = append(sources, quality.MergeSource{LotRef: src.LotRef, WeightMg: src.WeightMg})
	}
	result, err := api.service.Merge(r.Context(), quality.MergeInput{
		TargetLotRef: body.TargetLotRef, Sources: sources,
		OperatorID: body.OperatorID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"movement_id":       result.MovementID,
		"target_lot_ref":    result.Target.LotRef,
		"initial_weight_mg": result.Target.InitialWeightMg,
		"state":             result.Target.State,
	})
}

func (api *API) disposeLot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DispositionType string `json:"disposition_type"`
		OperatorID      string `json:"operator_id"`
		Remark          string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	disposal, err := api.service.DisposeLot(r.Context(), quality.DisposeLotInput{
		LotRef: r.PathValue("lot_ref"), DispositionType: body.DispositionType,
		OperatorID: body.OperatorID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": disposal.ID, "disposition_type": disposal.DispositionType,
	})
}

func (api *API) drawSample(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SampleRef   string `json:"sample_ref"`
		SubjectType string `json:"subject_type"`
		SubjectRef  string `json:"subject_ref"`
		Kind        string `json:"kind"`
		RetestOfRef string `json:"retest_of_ref"`
		QtyMg       int64  `json:"qty_mg"`
		SampledAt   string `json:"sampled_at"`
		SamplerID   string `json:"sampler_id"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	sampledAt, err := parseOptionalTime(body.SampledAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "sampled_at 时间格式错误"))
		return
	}
	sample, err := api.service.DrawSample(r.Context(), quality.DrawSampleInput{
		SampleRef: body.SampleRef, SubjectType: body.SubjectType, SubjectRef: body.SubjectRef,
		Kind: body.Kind, RetestOfRef: body.RetestOfRef, QtyMg: body.QtyMg,
		SampledAt: derefTime(sampledAt), SamplerID: body.SamplerID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": sample.ID, "sample_ref": sample.SampleRef, "kind": sample.Kind,
		"subject_type": sample.SubjectType, "qty_mg": sample.QtyMg,
	})
}

func (api *API) openInspection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InspectionRef string `json:"inspection_ref"`
		SampleRef     string `json:"sample_ref"`
		OpenedBy      string `json:"opened_by"`
		Remark        string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	inspection, err := api.service.OpenInspection(r.Context(), quality.OpenInspectionInput{
		InspectionRef: body.InspectionRef, SampleRef: body.SampleRef,
		OpenedBy: body.OpenedBy, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": inspection.ID, "inspection_ref": inspection.InspectionRef,
	})
}

func (api *API) addReading(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TestItem      string `json:"test_item"`
		SpecMin       string `json:"spec_min"`
		SpecMax       string `json:"spec_max"`
		Unit          string `json:"unit"`
		ObservedValue string `json:"observed_value"`
		MeasuredAt    string `json:"measured_at"`
		InspectorID   string `json:"inspector_id"`
		Remark        string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	measuredAt, err := parseOptionalTime(body.MeasuredAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "measured_at 时间格式错误"))
		return
	}
	result, err := api.service.AddReading(r.Context(), r.PathValue("inspection_ref"), quality.ReadingInput{
		TestItem: body.TestItem, SpecMin: body.SpecMin, SpecMax: body.SpecMax, Unit: body.Unit,
		ObservedValue: body.ObservedValue, MeasuredAt: derefTime(measuredAt),
		InspectorID: body.InspectorID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": result.Reading.ID, "seq": result.Reading.Seq, "outcome": result.Outcome,
	})
}

func (api *API) concludeInspection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InspectorID string `json:"inspector_id"`
		ConcludedAt string `json:"concluded_at"`
		Remark      string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	concludedAt, err := parseOptionalTime(body.ConcludedAt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "concluded_at 时间格式错误"))
		return
	}
	result, err := api.service.ConcludeInspection(r.Context(), quality.ConcludeInspectionInput{
		InspectionRef: r.PathValue("inspection_ref"), InspectorID: body.InspectorID,
		ConcludedAt: derefTime(concludedAt), Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"inspection_ref": result.Inspection.InspectionRef, "verdict": result.Verdict,
	})
}

func (api *API) createCompoundBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchRef        string `json:"batch_ref"`
		FormulaRef      string `json:"formula_ref"`
		FormulaRevision int    `json:"formula_revision"`
		ProductName     string `json:"product_name"`
		ManagerID       string `json:"manager_id"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	batch, err := api.service.CreateCompoundBatch(r.Context(), quality.CreateCompoundBatchInput{
		BatchRef: body.BatchRef, FormulaRef: body.FormulaRef,
		FormulaRevision: body.FormulaRevision, ProductName: body.ProductName,
		ManagerID: body.ManagerID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": batch.ID, "batch_ref": batch.BatchRef,
		"formula_ref": batch.FormulaRef, "formula_revision": batch.FormulaRevision,
		"product_name": batch.ProductName, "status": batch.Status,
	})
}

func (api *API) weighMaterial(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LotRef    string `json:"lot_ref"`
		WeightMg  int64  `json:"weight_mg"`
		WeigherID string `json:"weigher_id"`
		Remark    string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	result, err := api.service.WeighMaterial(r.Context(), quality.WeighMaterialInput{
		BatchRef: r.PathValue("batch_ref"), LotRef: body.LotRef, WeightMg: body.WeightMg,
		WeigherID: body.WeigherID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": result.Weighing.ID, "material_lot_ref": result.Lot.LotRef,
		"weight_mg": result.Weighing.WeightMg,
	})
}

func (api *API) chargeBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ManagerID string `json:"manager_id"`
		Remark    string `json:"remark"`
	}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	result, err := api.service.ChargeBatch(r.Context(), quality.ChargeBatchInput{
		BatchRef: r.PathValue("batch_ref"), ManagerID: body.ManagerID, Remark: body.Remark,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"batch_ref":      result.Batch.BatchRef,
		"status":         result.Batch.Status,
		"charged_mg":     result.TotalMg,
		"charging_count": len(result.Chargings),
	})
}

func (api *API) releaseBatch(w http.ResponseWriter, r *http.Request) {
	api.handleRelease(w, r, api.service.ReleaseBatch)
}

func (api *API) holdBatch(w http.ResponseWriter, r *http.Request) {
	api.handleRelease(w, r, api.service.HoldBatch)
}

func (api *API) rejectBatch(w http.ResponseWriter, r *http.Request) {
	api.handleRelease(w, r, api.service.RejectBatch)
}

func (api *API) handleRelease(
	w http.ResponseWriter,
	r *http.Request,
	decide func(context.Context, quality.ReleaseBatchInput) (store.ReleaseDecision, error),
) {
	body := struct {
		HeadID string `json:"head_id"`
		Reason string `json:"reason"`
	}{}
	if err := decode(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, err.Error()))
		return
	}
	decision, err := decide(r.Context(), quality.ReleaseBatchInput{
		BatchRef: r.PathValue("batch_ref"), HeadID: body.HeadID, Reason: body.Reason,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id": decision.ID, "decision": decision.Decision,
	})
}

func (api *API) traceLineage(w http.ResponseWriter, r *http.Request) {
	viewerID := r.URL.Query().Get("viewer_id")
	if viewerID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "viewer_id 查询参数必填"))
		return
	}
	trace, err := api.service.TraceBatch(r.Context(), r.PathValue("batch_ref"), viewerID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (api *API) publicBatchStatus(w http.ResponseWriter, r *http.Request) {
	view, err := api.service.PublicBatchStatus(r.Context(), r.PathValue("batch_ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (api *API) lotLedger(w http.ResponseWriter, r *http.Request) {
	viewerID := r.URL.Query().Get("viewer_id")
	if viewerID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "viewer_id 查询参数必填"))
		return
	}
	view, err := api.service.LotLedgerForViewer(r.Context(), r.PathValue("lot_ref"), viewerID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (api *API) affectedBatches(w http.ResponseWriter, r *http.Request) {
	viewerID := r.URL.Query().Get("viewer_id")
	if viewerID == "" {
		writeJSON(w, http.StatusBadRequest, errorEnvelope(quality.CodeValidation, "viewer_id 查询参数必填"))
		return
	}
	batches, err := api.service.AffectedBatches(r.Context(), r.PathValue("lot_ref"), viewerID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lot_ref": r.PathValue("lot_ref"), "affected_batches": batches,
	})
}

func parseOptionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
