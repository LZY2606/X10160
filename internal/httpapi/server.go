package httpapi

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
	"calibration-trace/internal/service"
	"calibration-trace/internal/store"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	sub, _ := fs.Sub(staticFiles, "static")
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
	api := func(pattern string, h http.HandlerFunc) { s.mux.HandleFunc(pattern, h) }
	api("GET /api/state", s.getState)
	api("POST /api/units", s.createUnit)
	api("POST /api/devices", s.createDevice)
	api("POST /api/references", s.createReference)
	api("POST /api/curves", s.createCurve)
	s.mux.HandleFunc("PUT /api/curves/{id}", s.updateCurve)
	api("POST /api/curves/{id}/publish", s.publishCurve)
	api("POST /api/curves/{id}/clone", s.cloneCurve)
	api("GET /api/curves/{id}/coverage", s.coverage)
	api("POST /api/readings/import", s.importReadings)
	api("GET /api/trace", s.trace)
	api("POST /api/compare", s.compare)
	api("POST /api/convert", s.convert)
	api("GET /api/export", s.exportData)
	api("POST /api/import-data", s.importData)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func errorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func fail(w http.ResponseWriter, err error) {
	writeJSON(w, errorStatus(err), map[string]string{"error": err.Error()})
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.svc.Snapshot())
}

func (s *Server) createUnit(w http.ResponseWriter, r *http.Request) {
	var in model.Unit
	if err := decodeJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.CreateUnit(in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in model.Device
	if err := decodeJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.CreateDevice(in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) createReference(w http.ResponseWriter, r *http.Request) {
	var in model.ReferencePoint
	if err := decodeJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.CreateReference(in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) createCurve(w http.ResponseWriter, r *http.Request) {
	var in model.CurveVersion
	if err := decodeJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.CreateCurve(in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}

type updateCurveRequest struct {
	ExpectedRevision int                `json:"expected_revision"`
	Curve            model.CurveVersion `json:"curve"`
}

func (s *Server) updateCurve(w http.ResponseWriter, r *http.Request) {
	var req updateCurveRequest
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.UpdateCurve(r.PathValue("id"), req.ExpectedRevision, req.Curve)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) publishCurve(w http.ResponseWriter, r *http.Request) {
	var req service.PublishRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			fail(w, err)
			return
		}
	}
	out, err := s.svc.PublishCurve(r.PathValue("id"), req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

type cloneRequest struct {
	ID string `json:"id"`
}

func (s *Server) cloneCurve(w http.ResponseWriter, r *http.Request) {
	var req cloneRequest
	if r.ContentLength != 0 {
		_ = decodeJSON(r, &req)
	}
	out, err := s.svc.CloneCurve(r.PathValue("id"), req.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, out)
}

func (s *Server) coverage(w http.ResponseWriter, r *http.Request) {
	d := s.svc.Snapshot()
	for _, c := range d.Curves {
		if c.ID == r.PathValue("id") {
			writeJSON(w, 200, map[string]any{"curve_id": c.ID, "fingerprint": c.Fingerprint, "coverage": calib.CurveCoverage(c)})
			return
		}
	}
	fail(w, errors.New("curve not found"))
}

type importReadingsRequest struct {
	Rows []model.ReadingRow `json:"rows"`
}

func (s *Server) importReadings(w http.ResponseWriter, r *http.Request) {
	var req importReadingsRequest
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.ImportReadings(req.Rows)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) trace(w http.ResponseWriter, r *http.Request) {
	raw, result, refs, curve, err := s.svc.Trace(r.URL.Query().Get("device_id"), r.URL.Query().Get("sample_id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"raw_reading": raw, "result": result, "reference_points": refs, "curve_version": curve})
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	var req service.CompareRequest
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	out, err := s.svc.CompareCurves(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

type convertRequest struct {
	Value float64 `json:"value"`
	From  string  `json:"from_unit_id"`
	To    string  `json:"to_unit_id"`
}

func (s *Server) convert(w http.ResponseWriter, r *http.Request) {
	var req convertRequest
	if err := decodeJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	d := s.svc.Snapshot()
	var from, to model.Unit
	for _, u := range d.Units {
		if u.ID == req.From {
			from = u
		}
		if u.ID == req.To {
			to = u
		}
	}
	if from.ID == "" || to.ID == "" {
		fail(w, errors.New("unit not found"))
		return
	}
	v, err := calib.ConvertValue(req.Value, from, to)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"value": v, "unit_id": to.ID, "dimension": to.Dimension})
}

func (s *Server) exportData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="calibration-trace-export.json"`)
	writeJSON(w, 200, s.svc.Export())
}

func (s *Server) importData(w http.ResponseWriter, r *http.Request) {
	var d model.Data
	if err := decodeJSON(r, &d); err != nil {
		fail(w, err)
		return
	}
	if err := s.svc.ImportData(d); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "imported"})
}

var _ = strings.TrimSpace
var _ = strconv.Itoa
