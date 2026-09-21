package station

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"time"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	store *Store
	mux   *http.ServeMux
}

func NewServer(store *Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return logRequests(s.mux)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/units", s.listUnits)
	m.HandleFunc("POST /api/units", s.registerUnit)
	m.HandleFunc("GET /api/devices", s.listDevices)
	m.HandleFunc("POST /api/devices", s.createDevice)
	m.HandleFunc("PUT /api/devices/{id}", s.updateDevice)
	m.HandleFunc("GET /api/reference-points", s.listRefs)
	m.HandleFunc("POST /api/reference-points", s.createRef)

	m.HandleFunc("GET /api/versions", s.listVersions)
	m.HandleFunc("POST /api/versions", s.createVersion)
	m.HandleFunc("GET /api/versions/{id}", s.getVersion)
	m.HandleFunc("PUT /api/versions/{id}", s.updateVersion)
	m.HandleFunc("POST /api/versions/{id}/publish", s.publishVersion)
	m.HandleFunc("GET /api/versions/{id}/coverage", s.versionCoverage)

	m.HandleFunc("POST /api/readings/import", s.importReadings)
	m.HandleFunc("GET /api/readings", s.listReadings)
	m.HandleFunc("GET /api/readings/{id}/trace", s.trace)
	m.HandleFunc("POST /api/compare", s.compare)
	m.HandleFunc("POST /api/convert", s.convert)

	m.HandleFunc("GET /api/export", s.exportBundle)
	m.HandleFunc("POST /api/import", s.importBundle)
	m.HandleFunc("POST /api/demo-seed", s.demoSeed)

	sub, _ := fs.Sub(webFS, "web")
	m.Handle("GET /", http.FileServer(http.FS(sub)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求 JSON 无效: " + err.Error()})
		return false
	}
	return true
}

func mapError(w http.ResponseWriter, err error) {
	var nf *NotFoundError
	var cf *ConflictError
	var dm *DimMismatchError
	var un *UnknownUnitError
	switch {
	case errors.As(err, &nf):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.As(err, &cf):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.As(err, &dm), errors.As(err, &un):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error(), "code": "dimension_or_unit"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

func (s *Server) listUnits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"units": s.store.ListUnits()})
}

func (s *Server) registerUnit(w http.ResponseWriter, r *http.Request) {
	var u Unit
	if !decode(w, r, &u) {
		return
	}
	if err := s.store.RegisterUnit(u); err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"devices": s.store.ListDevices()})
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var in DeviceInput
	if !decode(w, r, &in) {
		return
	}
	d, err := s.store.RegisterDevice(in)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	var in DeviceInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	d, err := s.store.UpdateDevice(in)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) listRefs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"reference_points": s.store.ListReferencePoints(r.URL.Query().Get("device_id"))})
}

func (s *Server) createRef(w http.ResponseWriter, r *http.Request) {
	var in RefInput
	if !decode(w, r, &in) {
		return
	}
	rp, err := s.store.AddReferencePoint(in)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rp)
}

type versionOut struct {
	*CurveVersion
	Issues []ValidationIssue `json:"issues"`
}

func versionWithIssues(s *Store, v *CurveVersion) versionOut {
	return versionOut{CurveVersion: v, Issues: v.validateSegments()}
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	vs := s.store.ListVersions(r.URL.Query().Get("device_id"))
	out := make([]versionOut, len(vs))
	for i, v := range vs {
		out[i] = versionWithIssues(s.store, v)
	}
	writeJSON(w, 200, map[string]any{"versions": out})
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetVersion(r.PathValue("id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, versionWithIssues(s.store, v))
}

func (s *Server) createVersion(w http.ResponseWriter, r *http.Request) {
	var in VersionInput
	if !decode(w, r, &in) {
		return
	}
	v, issues, err := s.store.CreateVersion(in)
	if err != nil {
		mapError(w, err)
		return
	}
	if len(issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "曲线存在校验问题，未保存", "issues": issues})
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) updateVersion(w http.ResponseWriter, r *http.Request) {
	var in VersionInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	v, issues, err := s.store.UpdateVersion(in)
	if err != nil {
		mapError(w, err)
		return
	}
	if len(issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "曲线存在校验问题，未保存", "issues": issues})
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) publishVersion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if r.ContentLength != 0 {
		if !decode(w, r, &body) {
			return
		}
	} else {
		body.ExpectedRevision = -1
	}
	if body.ExpectedRevision < 0 {
		if v, err := s.store.GetVersion(r.PathValue("id")); err == nil {
			body.ExpectedRevision = v.Revision
		}
	}
	v, err := s.store.PublishVersion(r.PathValue("id"), body.ExpectedRevision)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) versionCoverage(w http.ResponseWriter, r *http.Request) {
	view, err := s.store.Coverage(r.PathValue("id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) importReadings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Readings []ReadingInput `json:"readings"`
	}
	if !decode(w, r, &body) {
		return
	}
	rep, err := s.store.BatchImport(body.Readings)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) listReadings(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	readings := s.store.ListReadings(deviceID)
	type row struct {
		Reading *Reading           `json:"reading"`
		Result  *CalibrationResult `json:"result"`
	}
	out := make([]row, 0, len(readings))
	for _, rd := range readings {
		out = append(out, row{Reading: rd, Result: s.store.Result(rd.ID)})
	}
	writeJSON(w, 200, map[string]any{"rows": out})
}

func (s *Server) trace(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Trace(r.PathValue("id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) compare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VersionA   string   `json:"version_a"`
		VersionB   string   `json:"version_b"`
		DeviceID   string   `json:"device_id"`
		ReadingIDs []string `json:"reading_ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	rep, err := s.store.CompareVersions(body.VersionA, body.VersionB, body.DeviceID, body.ReadingIDs)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) convert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value float64 `json:"value"`
		From  string  `json:"from"`
		To    string  `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	v, err := s.store.ConvertDisplay(body.Value, body.From, body.To)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"value": v, "from": body.From, "to": body.To})
}

func (s *Server) exportBundle(w http.ResponseWriter, r *http.Request) {
	b := s.store.Snapshot()
	b.ExportedAt = normTime(time.Now())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=calibration-station-export.json")
	_ = json.NewEncoder(w).Encode(b)
}

func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	var b Bundle
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "导入 JSON 无效: " + err.Error()})
		return
	}
	n, err := s.store.MergeImport(&b)
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"merged": n})
}

func (s *Server) demoSeed(w http.ResponseWriter, r *http.Request) {
	res, err := s.store.SeedDemo()
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}

var _ = atoiDefault
