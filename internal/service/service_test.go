package service

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"calibration-trace/internal/model"
	"calibration-trace/internal/store"
)

func seedService(t *testing.T) *Service {
	t.Helper()
	st := store.NewMemory()
	s := New(st)
	if _, err := s.CreateUnit(model.Unit{ID: "C", Name: "Celsius", Dimension: "temperature", Factor: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUnit(model.Unit{ID: "F", Name: "Fahrenheit", Dimension: "temperature", Factor: 5.0 / 9.0, Offset: -160.0 / 9.0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUnit(model.Unit{ID: "Pa", Name: "Pascal", Dimension: "pressure", Factor: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDevice(model.Device{ID: "d1", Name: "Probe", InputUnitID: "C", OutputUnitID: "C"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReference(model.ReferencePoint{ID: "r1", DeviceID: "d1", Name: "zero", InputValue: 0, InputUnitID: "C", OutputValue: 0, OutputUnitID: "C", TakenAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func curveInput(id string, slope float64, validFrom string, refs []string, extrapolate bool) model.CurveVersion {
	return model.CurveVersion{ID: id, DeviceID: "d1", Name: id, InputUnitID: "C", OutputUnitID: "C", ValidFrom: validFrom,
		Segments: []model.Segment{{ID: "a", Kind: "linear", XMin: 0, XMax: 100, MinInclusive: true, MaxInclusive: true,
			Points: []model.LinearPoint{{X: 0, Y: 0}, {X: 100, Y: 100 * slope}}, Uncertainty: 0.5}},
		Extrapolation: extrapolate, ExtraUncertainty: map[bool]float64{true: 2.0}[extrapolate],
		ReferenceIDs: refs}
}

func publish(t *testing.T, s *Service, id, confirmed string, rev int) {
	t.Helper()
	if _, err := s.PublishCurve(id, PublishRequest{ExpectedRevision: rev, ConfirmedAt: confirmed}); err != nil {
		t.Fatal(err)
	}
}

func row(id string, raw float64, unit, sampled, received string) model.ReadingRow {
	return model.ReadingRow{DeviceID: "d1", SampleID: id, RawValue: raw, RawUnitID: unit, DeviceTime: sampled, ReceivedAt: received}
}

func TestClockRollbackSelectionAndHistoryFreeze(t *testing.T) {
	s := seedService(t)
	c1, err := s.CreateCurve(curveInput("c1", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, "c1", "2026-02-01T00:00:00Z", c1.Revision)
	c2, err := s.CreateCurve(curveInput("c2", 2, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, "c2", "2026-03-01T00:00:00Z", c2.Revision)
	batch, err := s.ImportReadings([]model.ReadingRow{
		row("later-sample", 10, "C", "2026-04-01T00:00:00Z", "2026-04-02T00:00:00Z"),
		row("clock-rolled-back", 10, "C", "2026-02-10T00:00:00Z", "2026-04-03T00:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range batch.Rows {
		if r.Status != "accepted" {
			t.Fatalf("row %s: %s", r.SampleID, r.Reason)
		}
	}
	d := s.Snapshot()
	var old, newer model.CalibratedResult
	for _, r := range d.Results {
		if r.SampleID == "clock-rolled-back" {
			old = r
		}
		if r.SampleID == "later-sample" {
			newer = r
		}
	}
	if old.SelectionReason.CurveID != "c2" || newer.SelectionReason.CurveID != "c2" {
		t.Fatalf("selection old=%s newer=%s", old.SelectionReason.CurveID, newer.SelectionReason.CurveID)
	}
	frozen := old.Fingerprint
	_, err = s.CompareCurves(CompareRequest{LeftCurveID: "c1", RightCurveID: "c2", DeviceID: "d1", SampleIDs: []string{"clock-rolled-back"}})
	if err != nil {
		t.Fatal(err)
	}
	d = s.Snapshot()
	for _, r := range d.Results {
		if r.SampleID == "clock-rolled-back" && r.Fingerprint != frozen {
			t.Fatal("historical result was rewritten by comparison")
		}
	}
}

func TestBatchPartialFailureIdempotenceAndConflict(t *testing.T) {
	s := seedService(t)
	c, _ := s.CreateCurve(curveInput("c1", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	publish(t, s, "c1", "2026-02-01T00:00:00Z", c.Revision)
	batch, err := s.ImportReadings([]model.ReadingRow{
		row("ok", 10, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
		row("bad-dimension", 10, "Pa", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
		row("outside", 200, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Accepted != 1 || batch.Failed != 2 || len(s.Snapshot().Readings) != 1 {
		t.Fatalf("batch=%+v readings=%d", batch, len(s.Snapshot().Readings))
	}
	again, _ := s.ImportReadings([]model.ReadingRow{row("ok", 10, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z")})
	if again.Rows[0].Status != "idempotent" {
		t.Fatalf("repeat=%+v", again)
	}
	conflict, _ := s.ImportReadings([]model.ReadingRow{row("ok", 11, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z")})
	if conflict.Rows[0].Status != "failed" || !conflict.Rows[0].Conflict {
		t.Fatalf("conflict=%+v", conflict)
	}
}

func TestNoCurveBeforeReceiptAndExplicitExtrapolation(t *testing.T) {
	s := seedService(t)
	c, _ := s.CreateCurve(curveInput("future", 1, "2026-01-01T00:00:00Z", []string{"r1"}, true))
	publish(t, s, "future", "2026-03-01T00:00:00Z", c.Revision)
	batch, _ := s.ImportReadings([]model.ReadingRow{row("early", 10, "C", "2026-02-01T00:00:00Z", "2026-02-02T00:00:00Z")})
	if batch.Rows[0].Status != "failed" || !strings.Contains(batch.Rows[0].Reason, "no curve") {
		t.Fatalf("early=%+v", batch.Rows[0])
	}
	batch, _ = s.ImportReadings([]model.ReadingRow{row("far", 110, "C", "2026-03-02T00:00:00Z", "2026-03-03T00:00:00Z")})
	if batch.Rows[0].Status != "accepted" || !batch.Rows[0].Result.Extrapolated || batch.Rows[0].Result.Uncertainty.Combined != 2.0615528128088303 {
		t.Fatalf("extrapolation=%+v", batch.Rows[0])
	}
}

func TestOptimisticLockAndImmutablePublish(t *testing.T) {
	s := seedService(t)
	c, err := s.CreateCurve(curveInput("c", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	if err != nil {
		t.Fatal(err)
	}
	update := curveInput("c", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false)
	if _, err := s.UpdateCurve("c", c.Revision, update); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCurve("c", c.Revision, update); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale update err=%v", err)
	}
	c = s.Snapshot().Curves[0]
	publish(t, s, "c", "2026-02-01T00:00:00Z", c.Revision)
	if _, err := s.UpdateCurve("c", c.Revision+1, update); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("immutable err=%v", err)
	}
}

func TestExportImportFingerprintsAndRawFactStability(t *testing.T) {
	s := seedService(t)
	c, _ := s.CreateCurve(curveInput("c", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	publish(t, s, "c", "2026-02-01T00:00:00Z", c.Revision)
	batch, _ := s.ImportReadings([]model.ReadingRow{row("s", 10, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z")})
	if batch.Accepted != 1 {
		t.Fatal(batch)
	}
	exported := s.Export()
	if err := s.ImportData(exported); err != nil {
		t.Fatalf("valid reimport: %v", err)
	}
	bad := exported
	bad.Readings[0].RawValue = 999
	if err := s.ImportData(bad); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestRestartKeepsFrozenFingerprints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	st, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(st)
	if _, err := s.CreateUnit(model.Unit{ID: "C", Name: "Celsius", Dimension: "temperature", Factor: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDevice(model.Device{ID: "d1", Name: "Probe", InputUnitID: "C", OutputUnitID: "C"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReference(model.ReferencePoint{ID: "r1", DeviceID: "d1", Name: "zero", InputValue: 0, InputUnitID: "C", OutputValue: 0, OutputUnitID: "C", TakenAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCurve(curveInput("c1", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, "c1", "2026-02-01T00:00:00Z", c.Revision)
	batch, err := s.ImportReadings([]model.ReadingRow{row("s1", 10, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	before := batch.Rows[0].Result.Fingerprint
	st2, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	s2 := New(st2)
	traced, result, refs, curve, err := s2.Trace("d1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if traced.Fingerprint == "" || result.Fingerprint != before || len(refs) != 1 || curve.Fingerprint == "" {
		t.Fatalf("restart trace raw=%q result=%q refs=%d curve=%q", traced.Fingerprint, result.Fingerprint, len(refs), curve.Fingerprint)
	}
}

func TestSameBatchDuplicateIsConflictAndLegalRowsContinue(t *testing.T) {
	s := seedService(t)
	c, _ := s.CreateCurve(curveInput("c1", 1, "2026-01-01T00:00:00Z", []string{"r1"}, false))
	publish(t, s, "c1", "2026-02-01T00:00:00Z", c.Revision)
	batch, err := s.ImportReadings([]model.ReadingRow{
		row("dup", 1, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
		row("dup", 2, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
		row("legal", 3, "C", "2026-02-02T00:00:00Z", "2026-02-02T00:01:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Accepted != 2 || batch.Failed != 1 || !batch.Rows[1].Conflict {
		t.Fatalf("batch=%+v", batch)
	}
	if len(s.Snapshot().Readings) != 2 {
		t.Fatalf("readings=%d", len(s.Snapshot().Readings))
	}
}
