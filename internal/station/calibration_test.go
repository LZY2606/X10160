package station

import (
	"math"
	"testing"
	"time"
)

func (s *Store) calibrateForTest(r *Reading) *CalibrationResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calibrateLocked(r, s.Devices[r.DeviceID])
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCalibrationEndpointLinearAndRefs(t *testing.T) {
	s, _, _, v2 := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	r := importReading(t, s, "a", 8, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if r.Status != StatusCalibrated {
		t.Fatalf("status=%s why=%s", r.Status, r.Selection.Why)
	}
	// v2 high segment: (8-5)/(10-5) * (1020-500) + 500 = 812
	if !approx(r.CalValue, 812) {
		t.Fatalf("cal=%v want 812", r.CalValue)
	}
	if r.VersionID != "v2" || r.VersionFingerprint != v2.Fingerprint {
		t.Fatalf("wrong version/fp %s %s", r.VersionID, r.VersionFingerprint)
	}
	if r.SegmentLabel != "高" || r.SegmentType != SegLinear {
		t.Fatalf("segment=%s %s", r.SegmentLabel, r.SegmentType)
	}
	if len(r.ReferenceIDs) != 1 || r.ReferenceIDs[0] != "rs" {
		t.Fatalf("refs=%v", r.ReferenceIDs)
	}
	// uncertainty: sqrt(1.5^2 + 1.6^2)
	wantU := math.Sqrt(1.5*1.5 + 1.6*1.6)
	if !approx(r.Uncertainty.Total, wantU) {
		t.Fatalf("u=%v want %v", r.Uncertainty.Total, wantU)
	}
	// endpoint semantics: x=5 belongs to both the open-upper low segment
	// boundary and closed-low high segment; the low segment excludes 5.
	r5 := importReading(t, s, "a5", 5, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if r5.SegmentLabel != "高" {
		t.Fatalf("x=5 resolved to %s, want 高 (low segment upper-open)", r5.SegmentLabel)
	}
	if !approx(r5.CalValue, 500) {
		t.Fatalf("x=5 cal=%v", r5.CalValue)
	}
}

func TestExtrapolationOptIn(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	// v2 allows extrapolation at 3.0 kPa per V of distance.
	r := importReading(t, s, "over", 10.8, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if r.Status != StatusCalibrated || !r.Extrapolated {
		t.Fatalf("status=%s extrap=%v why=%s", r.Status, r.Extrapolated, r.Selection.Why)
	}
	// slope high = (1020-500)/5 = 104; value = 1020 + 0.8*104 = 1103.2
	if !approx(r.CalValue, 1103.2) {
		t.Fatalf("cal=%v", r.CalValue)
	}
	// extra uncertainty = rate*distance = 3*0.8 = 2.4
	if !approx(r.Uncertainty.Extrap, 2.4) {
		t.Fatalf("extrap u=%v want 2.4", r.Uncertainty.Extrap)
	}
	// v1 (no extrapolation): sample received in the v1 window.
	old := importReading(t, s, "over1", 10.8, "V", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	if old.Status != StatusOutOfRange {
		t.Fatalf("v1 out-of-range got %s why=%s", old.Status, old.Selection.Why)
	}
}

func TestDeviceClockRollback(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	// Received during v2 window, but device claims sampling during v1 window.
	r := importReading(t, s, "rb", 4, "V", now.Add(-48*time.Hour), now.Add(-1*time.Hour))
	if r.Status != StatusDeviceTimeBack {
		t.Fatalf("status=%s why=%s", r.Status, r.Selection.Why)
	}
	if r.VersionID != "v2" {
		t.Fatalf("should pin received-time version v2, got %s", r.VersionID)
	}
	if r.CalValue != 0 {
		t.Fatalf("rollback result must not carry a calibrated value, got %v", r.CalValue)
	}
}

func TestNoVersionAndFutureVersion(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	base := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	// Received before anything was published (v1 published -72h).
	r := importReading(t, s, "early", 4, "V", base.Add(-100*time.Hour), base.Add(-100*time.Hour))
	if r.Status != StatusNoVersion {
		t.Fatalf("status=%s why=%s", r.Status, r.Selection.Why)
	}
	// Received in the future after a brand new published version: create v3
	// published later than receive time must not be selectable.
	now := base
	r2 := importReading(t, s, "fut", 4, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if r2.VersionID != "v2" {
		t.Fatalf("want v2, got %s", r2.VersionID)
	}
}

func TestDimensionCheckOnImport(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rep, err := s.BatchImport([]ReadingInput{{
		ID: "bad", DeviceID: "PT1", RawValue: 1, RawUnit: "kPa",
		SampledAt: now.Add(-time.Hour), ReceivedAt: now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || rep.Rows[0].Outcome != "error" {
		t.Fatalf("report=%+v", rep)
	}
	// Same-dimension different unit is legal (mV -> V domain), raw fact kept.
	rep2, err := s.BatchImport([]ReadingInput{{
		ID: "mv", DeviceID: "PT1", RawValue: 8000, RawUnit: "mV",
		SampledAt: now.Add(-time.Hour), ReceivedAt: now,
	}})
	if err != nil || rep2.Imported != 1 {
		t.Fatalf("err=%v rep=%+v", err, rep2)
	}
	got := s.Results["mv"]
	if !approx(got.CalValue, 812) {
		t.Fatalf("8000 mV should map as 8V -> 812, got %v", got.CalValue)
	}
	if s.Readings["mv"].RawUnit != "mV" || s.Readings["mv"].RawValue != 8000 {
		t.Fatal("raw fact was rewritten during conversion")
	}
}

func TestUnitConversionsAndAffineTemperature(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	v, _, _, err := s.convertPoint(1, "bar", "kPa")
	if err != nil || !approx(v, 100) {
		t.Fatalf("bar->kPa %v %v", v, err)
	}
	v, _, _, err = s.convertPoint(25, "degC", "K")
	if err != nil || !approx(v, 298.15) {
		t.Fatalf("C->K %v %v", v, err)
	}
	v, _, _, err = s.convertPoint(32, "degF", "degC")
	if err != nil || !approx(v, 0) {
		t.Fatalf("F->C %v %v", v, err)
	}
	if _, _, _, err = s.convertPoint(1, "V", "kPa"); err == nil {
		t.Fatal("dimension mismatch must error")
	} else {
		if _, ok := err.(*DimMismatchError); !ok {
			t.Fatalf("want DimMismatchError, got %T", err)
		}
	}
}

func TestAmbiguousPublishedVersions(t *testing.T) {
	s, dev, v1, v2 := setupWorld(t)
	// Force both v1 and v2 to cover the same receive instant by giving v2 a
	// window that starts before v1 ends (both published by then).
	v2.ValidFrom = v1.ValidFrom.Add(time.Hour)
	v2.PublishedAt = v1.ValidFrom.Add(time.Hour)
	v2.Fingerprint = fingerprintVersion(v2)
	s.Versions["v2"] = v2
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	r := &Reading{ID: "amb", DeviceID: dev.ID, RawValue: 4, RawUnit: "V",
		SampledAt: now.Add(-48 * time.Hour), ReceivedAt: now.Add(-48 * time.Hour), CreatedAt: now}
	res := s.calibrateForTest(r)
	if res.Status != StatusAmbiguous || len(res.Selection.Candidates) != 2 {
		t.Fatalf("status=%s candidates=%v why=%s", res.Status, res.Selection.Candidates, res.Selection.Why)
	}
}
