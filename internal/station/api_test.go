package station

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAPI(t *testing.T) (*Store, http.Handler) {
	t.Helper()
	s, _, _, _ := setupWorld(t)
	return s, NewServer(s).Handler()
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func TestAPIDeviceRegisterAndUnits(t *testing.T) {
	_, h := newTestAPI(t)
	code, body := doJSON(t, h, "POST", "/api/devices", map[string]any{
		"id": "D2", "name": "温度探头", "raw_unit": "V", "calibrated_unit": "degC"})
	if code != 201 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	// Unknown unit rejected (422 semantic/unit error).
	code, body = doJSON(t, h, "POST", "/api/devices", map[string]any{
		"id": "D3", "name": "x", "raw_unit": "V", "calibrated_unit": "nope"})
	if code != 422 {
		t.Fatalf("unknown unit code=%d", code)
	}
	// Unit dimension mismatch via convert endpoint.
	code, body = doJSON(t, h, "POST", "/api/convert", map[string]any{"value": 1, "from": "V", "to": "kPa"})
	if code != 422 || !strings.Contains(body["error"].(string), "量纲不匹配") {
		t.Fatalf("convert code=%d body=%v", code, body)
	}
}

func TestAPIBatchImportAndTrace(t *testing.T) {
	s, h := newTestAPI(t)
	_ = s
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"id": "n1", "device_id": "PT1", "raw_value": 8, "raw_unit": "V",
			"sampled_at":  now.Add(-2 * time.Hour).Format(time.RFC3339),
			"received_at": now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{"id": "n2", "device_id": "PT1", "raw_value": 99, "raw_unit": "kPa",
			"sampled_at":  now.Add(-2 * time.Hour).Format(time.RFC3339),
			"received_at": now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	code, body := doJSON(t, h, "POST", "/api/readings/import", map[string]any{"readings": rows})
	if code != 200 {
		t.Fatalf("import code=%d", code)
	}
	if body["imported"].(float64) != 1 || body["failed"].(float64) != 1 {
		t.Fatalf("report=%v", body)
	}
	code, trace := doJSON(t, h, "GET", "/api/readings/n1/trace", nil)
	if code != 200 {
		t.Fatalf("trace code=%d", code)
	}
	result := trace["result"].(map[string]any)
	if result["cal_value"].(float64) != 812 {
		t.Fatalf("cal=%v", result["cal_value"])
	}
	refs := trace["references"].(map[string]any)
	if _, ok := refs["rs"]; !ok {
		t.Fatal("trace must include reference point rs")
	}
	version := trace["version"].(map[string]any)
	if version["fingerprint"] != result["version_fingerprint"] {
		t.Fatal("trace version fingerprint must match result")
	}
}

func TestAPIVersionConflict(t *testing.T) {
	s, h := newTestAPI(t)
	from := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Versions["v2"].ValidTo = from
	s.Versions["v2"].Fingerprint = fingerprintVersion(s.Versions["v2"])
	seg := []map[string]any{{"type": "linear", "lower": 0, "lower_closed": true,
		"upper": 1, "upper_closed": true, "uncertainty": 1,
		"knots": []map[string]any{{"x": 0, "y": 0}, {"x": 1, "y": 10}}}}
	body := map[string]any{"device_id": "PT1", "valid_from": from.Format(time.RFC3339),
		"valid_to": from.Add(24 * time.Hour).Format(time.RFC3339), "segments": seg}
	code, created := doJSON(t, h, "POST", "/api/versions", body)
	if code != 201 {
		t.Fatalf("create=%d %v", code, created)
	}
	vid := created["id"].(string)
	// Publish twice: second call with stale revision must 409.
	code, pub := doJSON(t, h, "POST", "/api/versions/"+vid+"/publish", map[string]any{"expected_revision": 1})
	if code != 200 {
		t.Fatalf("publish code=%d body=%v", code, pub)
	}
	// Re-publish with the pre-publish revision: stale optimistic lock.
	code, body2 := doJSON(t, h, "POST", "/api/versions/"+vid+"/publish", map[string]any{"expected_revision": 1})
	if code != 409 {
		t.Fatalf("stale publish code=%d body=%v", code, body2)
	}
	// Editing a published version also 409.
	body["expected_revision"] = 1 // frozen, any edit refused regardless of rev
	code, _ = doJSON(t, h, "PUT", "/api/versions/"+vid, body)
	if code != 409 {
		t.Fatalf("edit published code=%d want 409", code)
	}
}

func TestAPIOverlapValidation(t *testing.T) {
	_, h := newTestAPI(t)
	from := time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	seg := []map[string]any{
		{"type": "linear", "label": "a", "lower": 0, "lower_closed": true, "upper": 6, "upper_closed": true,
			"uncertainty": 1, "knots": []map[string]any{{"x": 0, "y": 0}, {"x": 6, "y": 600}}},
		{"type": "linear", "label": "b", "lower": 5, "lower_closed": true, "upper": 10, "upper_closed": true,
			"uncertainty": 1, "knots": []map[string]any{{"x": 5, "y": 500}, {"x": 10, "y": 1000}}},
	}
	code, body := doJSON(t, h, "POST", "/api/versions", map[string]any{
		"device_id": "PT1", "valid_from": from.Format(time.RFC3339),
		"valid_to": from.Add(24 * time.Hour).Format(time.RFC3339), "segments": seg})
	if code != 422 {
		t.Fatalf("overlapping create code=%d body=%v", code, body)
	}
	issues := body["issues"].([]any)
	if !strings.Contains(issues[0].(map[string]any)["code"].(string), "overlap") {
		t.Fatalf("issues=%v", issues)
	}
}

func TestAPIIndexServesTitle(t *testing.T) {
	_, h := newTestAPI(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "校准追溯站") {
		t.Fatalf("index missing title, code=%d", rec.Code)
	}
}

func TestAPICompareNonDestructive(t *testing.T) {
	s, h := newTestAPI(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	importReading(t, s, "c1", 8, "V", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	before := s.Results["c1"].CalValue
	code, _ := doJSON(t, h, "POST", "/api/compare", map[string]any{
		"version_a": "v1", "version_b": "v2", "reading_ids": []string{"c1"}})
	if code != 200 {
		t.Fatalf("compare code=%d", code)
	}
	if s.Results["c1"].CalValue != before || s.Results["c1"].VersionID != "v1" {
		t.Fatal("compare rewrote historical result")
	}
}

func TestAPIExportRoundTrip(t *testing.T) {
	s, h := newTestAPI(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	importReading(t, s, "e1", 8, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	req := httptest.NewRequest("GET", "/api/export", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("export code=%d", rec.Code)
	}
	var bundle Bundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Results["e1"].Fingerprint != s.Results["e1"].Fingerprint {
		t.Fatal("exported fingerprint mismatch")
	}
	// Re-import the exported bundle into a fresh server via HTTP.
	s2 := &Store{Bundle: newBundle()}
	h2 := NewServer(s2).Handler()
	req2 := httptest.NewRequest("POST", "/api/import", bytes.NewReader(rec.Body.Bytes()))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("import code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if s2.Results["e1"].Uncertainty.Total != s.Results["e1"].Uncertainty.Total {
		t.Fatal("uncertainty not identical after export/import")
	}
}
