package httpui

import (
	"embed"
	"net/http"
	"strconv"
	"strings"

	"stage-rigging-clearance/internal/application"
)

//go:embed static/*
var assets embed.FS

func (h *Handler) ReusablePlansHandler(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.ReusablePlans(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": v})
}

func (h *Handler) SearchCertificatesHandler(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	var sequence *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("sequence")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "凭据序号必须为正整数")
			return
		}
		sequence = &value
	}
	v, err := h.service.SearchCertificates(r.Context(), sequence, r.URL.Query().Get("digest"), r.URL.Query().Get("prefix"), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := assets.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "页面资源不可用", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok"})
}
func (h *Handler) ListSessionsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.ListSessions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": v})
}
func (h *Handler) CreateSessionHandler(w http.ResponseWriter, r *http.Request) {
	var c application.CreateSessionCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.CreateSession(r.Context(), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (h *Handler) UpdateSessionHandler(w http.ResponseWriter, r *http.Request) {
	var c application.UpdateSessionCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.UpdateSession(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) WorkbenchHandler(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	v, err := h.service.Workbench(r.Context(), r.PathValue("sessionID"), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Handler) AddItemHandler(w http.ResponseWriter, r *http.Request) {
	var c application.AddItemCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.AddItem(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (h *Handler) PreflightItemsHandler(w http.ResponseWriter, r *http.Request) {
	var c application.BulkItemPreflightCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.PreflightBulkItems(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) ImportItemsHandler(w http.ResponseWriter, r *http.Request) {
	var c application.BulkImportItemsCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.ImportItems(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (h *Handler) ReviseItemHandler(w http.ResponseWriter, r *http.Request) {
	var c application.ReviseItemCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.ReviseItem(r.Context(), r.PathValue("sessionID"), r.PathValue("itemID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) RetireItemHandler(w http.ResponseWriter, r *http.Request) {
	var c application.RetireItemCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.RetireItem(r.Context(), r.PathValue("sessionID"), r.PathValue("itemID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) SubmitInspectionHandler(w http.ResponseWriter, r *http.Request) {
	var c application.SubmitInspectionCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.SubmitInspection(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (h *Handler) PreflightInspectionsHandler(w http.ResponseWriter, r *http.Request) {
	var c application.BatchInspectionPreflightCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.PreflightBatchInspections(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) BatchInspectionsHandler(w http.ResponseWriter, r *http.Request) {
	var c application.BatchInspectionCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.SubmitBatchInspections(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (h *Handler) RemediateHandler(w http.ResponseWriter, r *http.Request) {
	var c application.RemediateCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.Remediate(r.Context(), r.PathValue("sessionID"), r.PathValue("hazardID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Handler) ReinspectHandler(w http.ResponseWriter, r *http.Request) {
	var c application.ReinspectCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.Reinspect(r.Context(), r.PathValue("sessionID"), r.PathValue("hazardID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (h *Handler) ReassignHazardHandler(w http.ResponseWriter, r *http.Request) {
	var c application.ReassignHazardCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.ReassignHazard(r.Context(), r.PathValue("sessionID"), r.PathValue("hazardID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) FreezePreviewHandler(w http.ResponseWriter, r *http.Request) {
	var c application.ReviewPreviewCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.PreviewFreeze(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (h *Handler) FreezeHandler(w http.ResponseWriter, r *http.Request) {
	var c application.FreezeCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.Freeze(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (h *Handler) IssueHandler(w http.ResponseWriter, r *http.Request) {
	var c application.IssueCommand
	if !decode(w, r, &c) {
		return
	}
	v, err := h.service.Issue(r.Context(), r.PathValue("sessionID"), c)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (h *Handler) AuditHandler(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	v, err := h.service.Audit(r.Context(), r.PathValue("sessionID"), limit, offset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": v, "limit": limit, "offset": offset})
}
