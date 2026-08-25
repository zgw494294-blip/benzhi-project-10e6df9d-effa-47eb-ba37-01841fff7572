package httpui

import (
	"io/fs"
	"net/http"

	"stage-rigging-clearance/internal/application"
)

type Handler struct {
	service *application.Service
	mux     *http.ServeMux
}

func New(service *application.Service) *Handler {
	h := &Handler{service: service, mux: http.NewServeMux()}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *Handler) routes() {
	static, _ := fs.Sub(assets, "static")
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	h.mux.HandleFunc("GET /", h.HomeHandler)
	h.mux.HandleFunc("GET /healthz", h.HealthHandler)
	h.mux.HandleFunc("GET /api/sessions", h.ListSessionsHandler)
	h.mux.HandleFunc("POST /api/sessions", h.CreateSessionHandler)
	h.mux.HandleFunc("GET /api/reusable-plans", h.ReusablePlansHandler)
	h.mux.HandleFunc("GET /api/certificates", h.SearchCertificatesHandler)
	h.mux.HandleFunc("PATCH /api/sessions/{sessionID}", h.UpdateSessionHandler)
	h.mux.HandleFunc("GET /api/sessions/{sessionID}", h.WorkbenchHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/items", h.AddItemHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/items/preflight", h.PreflightItemsHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/items/bulk", h.ImportItemsHandler)
	h.mux.HandleFunc("PATCH /api/sessions/{sessionID}/items/{itemID}", h.ReviseItemHandler)
	h.mux.HandleFunc("DELETE /api/sessions/{sessionID}/items/{itemID}", h.RetireItemHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/inspections", h.SubmitInspectionHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/inspections/preflight", h.PreflightInspectionsHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/inspections/batch", h.BatchInspectionsHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/hazards/{hazardID}/remediation", h.RemediateHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/hazards/{hazardID}/reinspection", h.ReinspectHandler)
	h.mux.HandleFunc("PATCH /api/sessions/{sessionID}/hazards/{hazardID}/assignment", h.ReassignHazardHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/freeze-preview", h.FreezePreviewHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/freeze", h.FreezeHandler)
	h.mux.HandleFunc("POST /api/sessions/{sessionID}/certificates", h.IssueHandler)
	h.mux.HandleFunc("GET /api/sessions/{sessionID}/audit", h.AuditHandler)
}
