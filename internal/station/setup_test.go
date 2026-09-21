package station

import (
	"testing"
	"time"
)

// setupWorld builds a device with three reference points and two published
// versions: v1 covering [-72h,-24h) without extrapolation, v2 covering
// [-24h,open) with explicit linear extrapolation.
func setupWorld(t *testing.T) (*Store, *Device, *CurveVersion, *CurveVersion) {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	dev := &Device{ID: "PT1", Name: "压力计", RawUnit: "V", CalibratedUnit: "kPa",
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	s.Devices[dev.ID] = dev
	refs := []*ReferencePoint{
		{ID: "r0", DeviceID: "PT1", Name: "零点", Dimension: DimPressure, Value: 0, Unit: "kPa", Uncertainty: 0.4, CreatedAt: now},
		{ID: "rm", DeviceID: "PT1", Name: "中点", Dimension: DimPressure, Value: 500, Unit: "kPa", Uncertainty: 1.0, CreatedAt: now},
		{ID: "rs", DeviceID: "PT1", Name: "满点", Dimension: DimPressure, Value: 1000, Unit: "kPa", Uncertainty: 1.5, CreatedAt: now},
	}
	for _, r := range refs {
		s.ReferencePoints[r.ID] = r
	}
	segs := func(spanY float64, uLow, uHigh float64) []Segment {
		return []Segment{
			{Type: SegLinear, Label: "低", Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: false,
				Knots: []Knot{{0, 0}, {5, 500}}, Uncertainty: uLow, Refs: []string{"r0", "rm"}},
			{Type: SegLinear, Label: "高", Lower: 5, LowerClosed: true, Upper: 10, UpperClosed: true,
				Knots: []Knot{{5, 500}, {10, spanY}}, Uncertainty: uHigh, Refs: []string{"rs"}},
		}
	}
	v1 := &CurveVersion{ID: "v1", DeviceID: "PT1", Revision: 2, State: StatePublished,
		Segments:  segs(1000, 1.2, 1.8),
		ValidFrom: now.Add(-72 * time.Hour), ValidTo: now.Add(-24 * time.Hour),
		CreatedAt: now.Add(-72 * time.Hour), PublishedAt: now.Add(-72 * time.Hour)}
	v1.Fingerprint = fingerprintVersion(v1)
	v2 := &CurveVersion{ID: "v2", DeviceID: "PT1", Revision: 1, State: StatePublished,
		Segments:  segs(1020, 1.1, 1.6),
		ValidFrom: now.Add(-24 * time.Hour), ValidTo: time.Time{},
		AllowExtrapolate: true, ExtrapUncertaintyRate: 3.0,
		CreatedAt: now.Add(-24 * time.Hour), PublishedAt: now.Add(-24 * time.Hour)}
	v2.Fingerprint = fingerprintVersion(v2)
	s.Versions["v1"], s.Versions["v2"] = v1, v2
	return s, dev, v1, v2
}

func importReading(t *testing.T, s *Store, id string, val float64, unit string, sampled, received time.Time) *CalibrationResult {
	t.Helper()
	r := &Reading{ID: id, DeviceID: "PT1", RawValue: val, RawUnit: unit,
		SampledAt: sampled, ReceivedAt: received, CreatedAt: received}
	res := s.calibrateForTest(r)
	s.Readings[id] = r
	s.Results[id] = res
	return res
}
