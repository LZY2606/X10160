package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"calibration-trace/internal/model"
	"calibration-trace/internal/service"
	"calibration-trace/internal/store"
)

func setupAPI(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(service.New(st)))
	t.Cleanup(ts.Close)
	return ts, path
}

func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	res, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, out
}

func TestHTTPEndpointsAndTitle(t *testing.T) {
	ts, _ := setupAPI(t)
	must := func(status int, want int) {
		if status != want {
			t.Fatalf("status=%d", status)
		}
	}
	st, _ := postJSON(t, ts, "/api/units", map[string]any{"id": "C", "name": "C", "dimension": "temperature", "factor": 1, "offset": 0})
	must(st, 201)
	st, _ = postJSON(t, ts, "/api/devices", map[string]any{"id": "d1", "name": "Probe", "input_unit_id": "C", "output_unit_id": "C"})
	must(st, 201)
	st, _ = postJSON(t, ts, "/api/references", map[string]any{"id": "r1", "device_id": "d1", "name": "zero", "input_value": 0, "input_unit_id": "C", "output_value": 0, "output_unit_id": "C", "taken_at": "2026-01-01T00:00:00Z"})
	must(st, 201)
	curve := map[string]any{"id": "c1", "device_id": "d1", "name": "v1", "input_unit_id": "C", "output_unit_id": "C", "valid_from": "2026-01-01T00:00:00Z", "reference_ids": []string{"r1"}, "segments": []map[string]any{{"id": "a", "kind": "linear", "x_min": 0, "x_max": 100, "min_inclusive": true, "max_inclusive": true, "points": []map[string]any{{"x": 0, "y": 0}, {"x": 100, "y": 100}}, "uncertainty": 0.5}}}
	st, _ = postJSON(t, ts, "/api/curves", curve)
	must(st, 201)
	st, _ = postJSON(t, ts, "/api/curves/c1/publish", map[string]any{"expected_revision": 0, "confirmed_at": "2026-02-01T00:00:00Z"})
	must(st, 200)
	st, _ = postJSON(t, ts, "/api/readings/import", map[string]any{"rows": []map[string]any{{"device_id": "d1", "sample_id": "s1", "raw_value": 10, "raw_unit_id": "C", "device_time": "2026-02-02T00:00:00Z", "received_at": "2026-02-02T00:01:00Z"}}})
	must(st, 200)
	res, err := http.Get(ts.URL + "/api/trace?device_id=d1&sample_id=s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("trace status %d", res.StatusCode)
	}
	res.Body.Close()
	res, err = http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "校准追溯站") {
		t.Fatal("page title missing")
	}
}

func TestHTTPVersionConflictStatus(t *testing.T) {
	ts, _ := setupAPI(t)
	postJSON(t, ts, "/api/units", map[string]any{"id": "C", "name": "C", "dimension": "temperature", "factor": 1, "offset": 0})
	postJSON(t, ts, "/api/devices", map[string]any{"id": "d1", "name": "Probe", "input_unit_id": "C", "output_unit_id": "C"})
	postJSON(t, ts, "/api/references", map[string]any{"id": "r1", "device_id": "d1", "name": "zero", "input_value": 0, "input_unit_id": "C", "output_value": 0, "output_unit_id": "C", "taken_at": "2026-01-01T00:00:00Z"})
	curve := model.CurveVersion{ID: "c1", DeviceID: "d1", Name: "v", InputUnitID: "C", OutputUnitID: "C", ValidFrom: "2026-01-01T00:00:00Z", ReferenceIDs: []string{"r1"}, Segments: []model.Segment{{ID: "a", Kind: "linear", XMin: 0, XMax: 100, MinInclusive: true, MaxInclusive: true, Points: []model.LinearPoint{{X: 0, Y: 0}, {X: 100, Y: 100}}, Uncertainty: 1}}}
	if status, _ := postJSON(t, ts, "/api/curves", curve); status != 201 {
		t.Fatalf("create status=%d", status)
	}
	raw, _ := json.Marshal(map[string]any{"expected_revision": 99, "curve": curve})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/curves/c1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", res.StatusCode)
	}
	res.Body.Close()
}
