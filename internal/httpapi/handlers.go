package httpapi

import (
	"net/http"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/domain"
)

func (a *API) bootstrap(w http.ResponseWriter, r *http.Request) {
	var in domain.CreateUserInput
	if !decode(w, r, &in) {
		return
	}
	actor, err := a.svc.Bootstrap(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, actor)
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.CreateUserInput
	if !decode(w, r, &in) {
		return
	}
	actor, err := a.svc.CreateUser(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, actor)
}

func (a *API) createMaterial(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.CreateMaterialInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.CreateMaterial(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) createFormula(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.CreateFormulaInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.CreateFormula(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) registerReceipt(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.ReceiptInput
	if !decode(w, r, &in) {
		return
	}
	id, err := a.svc.RegisterReceipt(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "receipt_ref": in.ReceiptRef})
}

func (a *API) getReceipt(w http.ResponseWriter, r *http.Request, caller string) {
	view, err := a.svc.GetReceiptQuality(r.Context(), caller, r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) takeSample(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.SampleInput
	if !decode(w, r, &in) {
		return
	}
	id, err := a.svc.TakeSample(r.Context(), caller, r.PathValue("ref"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "sample_ref": in.SampleRef})
}

func (a *API) recordInspection(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.InspectionInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.RecordInspection(r.Context(), caller, r.PathValue("ref"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) decideReceipt(w http.ResponseWriter, r *http.Request, caller string) {
	var body struct {
		Target string `json:"target"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := a.svc.DecideReceipt(r.Context(), caller, r.PathValue("ref"), body.Target, body.Note); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"receipt_ref": r.PathValue("ref"), "disposition": body.Target})
}

func (a *API) dispense(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.DispenseInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.Dispense(r.Context(), caller, r.PathValue("ref"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) merge(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.MergeInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.Merge(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) createBatch(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.BatchInput
	if !decode(w, r, &in) {
		return
	}
	id, err := a.svc.CreateBatch(r.Context(), caller, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "batch_ref": in.BatchRef})
}

func (a *API) getBatch(w http.ResponseWriter, r *http.Request, caller string) {
	view, err := a.svc.GetBatchForUser(r.Context(), caller, r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) addWeighing(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.WeighingInput
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.AddWeighing(r.Context(), caller, r.PathValue("ref"), in); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"batch_ref": r.PathValue("ref"), "status": "recorded"})
}

func (a *API) finishBatch(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.FinishBatchInput
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.FinishCompounding(r.Context(), caller, r.PathValue("ref"), in); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"batch_ref": r.PathValue("ref"), "status": "compounded"})
}

func (a *API) finishedInspection(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.FinishedInspectionInput
	if !decode(w, r, &in) {
		return
	}
	if err := a.svc.AddFinishedInspection(r.Context(), caller, r.PathValue("ref"), in); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"batch_ref": r.PathValue("ref"), "outcome": in.Outcome})
}

func (a *API) releaseBatch(w http.ResponseWriter, r *http.Request, caller string) {
	var in domain.ReleaseInput
	if !decode(w, r, &in) {
		return
	}
	view, err := a.svc.ReleaseBatch(r.Context(), caller, r.PathValue("ref"), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) lineage(w http.ResponseWriter, r *http.Request, caller string) {
	view, err := a.svc.TraceLineage(r.Context(), caller, r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) impact(w http.ResponseWriter, r *http.Request, caller string) {
	view, err := a.svc.Impact(r.Context(), caller, r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) weightCheck(w http.ResponseWriter, r *http.Request, caller string) {
	view, err := a.svc.WeightCheck(r.Context(), caller)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) publicReceipt(w http.ResponseWriter, r *http.Request) {
	view, err := a.svc.PublicReceipt(r.Context(), r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) publicBatch(w http.ResponseWriter, r *http.Request) {
	view, err := a.svc.PublicBatch(r.Context(), r.PathValue("ref"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
