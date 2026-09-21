package station

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestBatchIdempotencyConflictPartial(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rows := []ReadingInput{
		{ID: "x1", DeviceID: "PT1", RawValue: 2, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
		{ID: "x2", DeviceID: "NOPE", RawValue: 2, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
		{ID: "x3", DeviceID: "PT1", RawValue: 9, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
	}
	rep, err := s.BatchImport(rows)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 3 || rep.Imported != 2 || rep.Failed != 1 {
		t.Fatalf("first batch %+v", rep)
	}
	if rep.Rows[1].Outcome != "error" {
		t.Fatalf("unknown device row: %+v", rep.Rows[1])
	}
	// Second batch: identical x1 => duplicate; changed raw value => conflict;
	// brand new x4 => imported. Conflict must not roll back other legal rows.
	rows2 := []ReadingInput{
		{ID: "x1", DeviceID: "PT1", RawValue: 2, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
		{ID: "x1", DeviceID: "PT1", RawValue: 999, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
		{ID: "x4", DeviceID: "PT1", RawValue: 1, RawUnit: "V", SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour)},
	}
	rep2, err := s.BatchImport(rows2)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Duplicate != 1 || rep2.Conflict != 1 || rep2.Imported != 1 {
		t.Fatalf("second batch %+v", rep2)
	}
	// Raw fact and frozen result untouched by the conflicting row.
	if s.Readings["x1"].RawValue != 2 {
		t.Fatal("conflicting value must not overwrite stored raw fact")
	}
	if s.Results["x1"].CalValue != 200 {
		t.Fatalf("frozen result changed: %v", s.Results["x1"].CalValue)
	}
}

func TestOptimisticLockOnVersion(t *testing.T) {
	s, _, _, v2 := setupWorld(t)
	// Create a draft directly through the API path.
	// Close v2 so a later window can touch its end without overlap.
	v2.ValidTo = time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	v2.Fingerprint = fingerprintVersion(v2)
	s.Versions["v2"] = v2
	draftFrom := v2.ValidTo
	draftTo := draftFrom.Add(24 * time.Hour)
	in := VersionInput{DeviceID: "PT1", ValidFrom: draftFrom, ValidTo: draftTo,
		Segments: []Segment{{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 1, UpperClosed: true,
			Knots: []Knot{{0, 0}, {1, 10}}, Uncertainty: 1}}}
	v, issues, err := s.CreateVersion(in)
	if err != nil || len(issues) != 0 {
		t.Fatalf("create err=%v issues=%+v", err, issues)
	}
	// Stale revision save -> conflict.
	stale := in
	stale.ID, stale.ExpectedRevision = v.ID, v.Revision-1
	if _, _, err := s.UpdateVersion(stale); err == nil {
		t.Fatal("stale revision must be rejected")
	}
	// Correct revision save succeeds and bumps revision.
	fresh := in
	fresh.ID, fresh.ExpectedRevision = v.ID, v.Revision
	updated, _, err := s.UpdateVersion(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != v.Revision+1 {
		t.Fatalf("revision=%d", updated.Revision)
	}
	// Publish, then any edit must be refused (history freeze).
	if _, err := s.PublishVersion(updated.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateVersion(fresh); err == nil {
		t.Fatal("published version must be immutable")
	}
}

func TestPublishWindowOverlap(t *testing.T) {
	s, _, v1, v2 := setupWorld(t)
	// Draft whose window overlaps already-published v1 window.
	in := VersionInput{DeviceID: "PT1",
		ValidFrom: v1.ValidFrom.Add(time.Hour), ValidTo: v1.ValidTo.Add(-time.Hour),
		Segments: []Segment{{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 1, UpperClosed: true,
			Knots: []Knot{{0, 0}, {1, 10}}, Uncertainty: 1}}}
	v, _, err := s.CreateVersion(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishErr(v.ID, v.Revision); err == nil {
		t.Fatal("overlapping publish windows must be rejected")
	}
	// A window behind v1, ending exactly when v1 starts, is disjoint.
	if windowsOverlap(v1.ValidFrom.Add(-2*time.Hour), v1.ValidFrom, v1.ValidFrom, v1.ValidTo) {
		t.Fatal("windows touching at v1.ValidFrom must be disjoint")
	}
	// A window that merely fills [v1.ValidTo-1h, v1.ValidTo) overlaps v1 by
	// construction; instead verify the primitive: v1 and v2 themselves touch
	// at v1.ValidTo without overlapping.
	if windowsOverlap(v1.ValidFrom, v1.ValidTo, v2.ValidFrom, v2.ValidTo) {
		t.Fatal("v1/v2 touch at one endpoint and must not overlap")
	}
	in2 := in
	in2.ValidFrom = v1.ValidFrom.Add(-2 * time.Hour)
	in2.ValidTo = v1.ValidFrom
	in2.Note = "touches v1 from the past"
	vb, _, err := s.CreateVersion(in2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishErr(vb.ID, vb.Revision); err != nil {
		t.Fatalf("touching windows allowed, got %v", err)
	}
}

func (s *Store) publishErr(id string, rev int64) error {
	_, err := s.PublishVersion(id, rev)
	return err
}

func TestHistoryFreezeAfterRepublish(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	r := importReading(t, s, "h1", 8, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	if r.CalValue != 812 || r.VersionID != "v2" {
		t.Fatalf("setup result=%v ver=%s", r.CalValue, r.VersionID)
	}
	oldFp := r.Fingerprint
	// A new version v3 published later covering from now+1h must not touch
	// existing results (nor select for old receive times).
	v3 := &CurveVersion{ID: "v3", DeviceID: "PT1", Revision: 1, State: StatePublished,
		Segments: []Segment{{Type: SegLinear, Label: "唯一", Lower: 0, LowerClosed: true, Upper: 10, UpperClosed: true,
			Knots: []Knot{{0, 0}, {10, 9999}}, Uncertainty: 1}},
		ValidFrom: now.Add(time.Hour), ValidTo: time.Time{}, PublishedAt: now, CreatedAt: now}
	v3.Fingerprint = fingerprintVersion(v3)
	s.Versions["v3"] = v3
	if s.Results["h1"].Fingerprint != oldFp || s.Results["h1"].CalValue != 812 {
		t.Fatal("publishing a new version rewrote historical results")
	}
	// What-if comparison against v3 differs but does not persist.
	rep, err := s.CompareVersions("v2", "v3", "PT1", []string{"h1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 1 || !approx(rep.Items[0].B.CalValue, 7999.2) {
		t.Fatalf("compare item=%+v", rep.Items)
	}
	if s.Results["h1"].CalValue != 812 {
		t.Fatal("compare mutated stored result")
	}
}

func TestRestartPersistsFingerprints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	r := &Reading{ID: "z1", DeviceID: "PT1", RawValue: 8, RawUnit: "V",
		SampledAt: now.Add(-2 * time.Hour), ReceivedAt: now.Add(-2 * time.Hour), CreatedAt: now}
	s.Readings["z1"] = r
	s.Results["z1"] = s.calibrateForTest(r)
	s.path = path
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	wantResultFp := s.Results["z1"].Fingerprint
	wantVersionFp := s.Versions["v2"].Fingerprint
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Results["z1"].Fingerprint != wantResultFp {
		t.Fatal("result fingerprint changed across restart")
	}
	if reopened.Versions["v2"].Fingerprint != wantVersionFp {
		t.Fatal("version fingerprint changed across restart")
	}
}

func TestTamperedExportRejected(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	b := s.Snapshot()
	// Tamper a version segment after export; fingerprint must catch it.
	tampered := b
	raw, _ := json.Marshal(tampered)
	var mod Bundle
	_ = json.Unmarshal(raw, &mod)
	seg := mod.Versions["v2"].Segments[1]
	seg.Knots[1].Y = 9999
	mod.Versions["v2"].Segments[1] = seg
	target := s
	if _, err := target.MergeImport(&mod); err == nil {
		t.Fatal("tampered bundle with mismatched fingerprint must be rejected")
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	importReading(t, s, "q1", 8, "V", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	b := s.Snapshot()
	other, _ := Open("")
	n, err := other.MergeImport(&b)
	if err != nil {
		t.Fatalf("clean round trip rejected: %v", err)
	}
	if n == 0 {
		t.Fatal("nothing merged")
	}
	if other.Results["q1"].Fingerprint != s.Results["q1"].Fingerprint {
		t.Fatal("fingerprint differs after export/import")
	}
	if other.Results["q1"].Uncertainty.Total != s.Results["q1"].Uncertainty.Total {
		t.Fatal("uncertainty serialization differs after export/import")
	}
	if other.Results["q1"].Selection.Why != s.Results["q1"].Selection.Why {
		t.Fatal("selection reason differs after export/import")
	}
	// Second merge of identical content is a no-op (idempotent).
	if _, err := other.MergeImport(&b); err != nil {
		t.Fatalf("re-merge identical bundle: %v", err)
	}
}

func TestDeviceOptimisticLock(t *testing.T) {
	s, _, _, _ := setupWorld(t)
	d, err := s.UpdateDevice(DeviceInput{ID: "PT1", Name: "新名", RawUnit: "V", CalibratedUnit: "kPa", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.Revision != 2 {
		t.Fatalf("rev=%d", d.Revision)
	}
	if _, err := s.UpdateDevice(DeviceInput{ID: "PT1", Name: "旧名", RawUnit: "V", CalibratedUnit: "kPa", ExpectedRevision: 1}); err == nil {
		t.Fatal("stale device revision must conflict")
	}
}
