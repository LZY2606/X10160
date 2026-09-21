package station

import "time"

type SeedReport struct {
	AlreadySeeded bool     `json:"already_seeded"`
	DeviceID      string   `json:"device_id"`
	Refs          []string `json:"reference_points"`
	Versions      []string `json:"versions"`
	Readings      []string `json:"readings"`
}

var seededKey = "PT-204"

// SeedDemo populates a small idempotent demonstration dataset: a pressure
// transmitter, two published curve versions (showing history freeze), a
// rolled-back device clock reading and an out-of-range reading.
func (s *Store) SeedDemo() (*SeedReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := &SeedReport{DeviceID: seededKey}
	if _, ok := s.Devices[seededKey]; ok {
		rep.AlreadySeeded = true
		return rep, nil
	}
	now := normTime(time.Now())
	dev := &Device{
		ID: seededKey, Name: "压力变送器 PT-204（演示）",
		RawUnit: "V", CalibratedUnit: "kPa",
		Description: "0–10 V 电压输入，输出 0–1000 kPa 压力",
		Revision:    1, CreatedAt: now, UpdatedAt: now,
	}
	s.Devices[dev.ID] = dev

	refs := []*ReferencePoint{
		{ID: "ref-zero-204", DeviceID: dev.ID, Name: "零点参考（大气压）", Dimension: DimPressure,
			Value: 0, Unit: "kPa", Uncertainty: 0.4, Source: "实验室零压基准 ZR-9", CreatedAt: now},
		{ID: "ref-span-204", DeviceID: dev.ID, Name: "满量程参考", Dimension: DimPressure,
			Value: 1000, Unit: "kPa", Uncertainty: 1.5, Source: "活塞式压力计 PG-120", CreatedAt: now},
	}
	for _, rp := range refs {
		s.ReferencePoints[rp.ID] = rp
		rep.Refs = append(rep.Refs, rp.ID)
	}

	v1 := &CurveVersion{
		ID: "ver-204-v1", DeviceID: dev.ID, Revision: 2, State: StatePublished,
		Note: "初版：出厂分段线性 0–5V / 5–10V",
		Segments: []Segment{
			{Type: SegLinear, Label: "低段", Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: false,
				Knots: []Knot{{0, 0}, {5, 500}}, Uncertainty: 1.2,
				Refs: []string{"ref-zero-204", "ref-span-204"}},
			{Type: SegLinear, Label: "高段", Lower: 5, LowerClosed: true, Upper: 10, UpperClosed: true,
				Knots: []Knot{{5, 500}, {10, 1000}}, Uncertainty: 1.8,
				Refs: []string{"ref-span-204"}},
		},
		ValidFrom: now.Add(-72 * time.Hour), ValidTo: now.Add(-24 * time.Hour),
		AllowExtrapolate: false,
		CreatedAt:        now.Add(-72 * time.Hour), PublishedAt: now.Add(-72 * time.Hour),
	}
	v1.Fingerprint = fingerprintVersion(v1)

	v2 := &CurveVersion{
		ID: "ver-204-v2", DeviceID: dev.ID, Revision: 1, State: StatePublished,
		Note: "再校准版：高段斜率 +2%，低段不变（用于历史对比）",
		Segments: []Segment{
			{Type: SegLinear, Label: "低段", Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: false,
				Knots: []Knot{{0, 0}, {5, 500}}, Uncertainty: 1.2,
				Refs: []string{"ref-zero-204", "ref-span-204"}},
			{Type: SegLinear, Label: "高段", Lower: 5, LowerClosed: true, Upper: 10, UpperClosed: true,
				Knots: []Knot{{5, 500}, {10, 1020}}, Uncertainty: 1.6,
				Refs: []string{"ref-span-204"}},
		},
		ValidFrom: now.Add(-24 * time.Hour), ValidTo: time.Time{},
		AllowExtrapolate: true, ExtrapUncertaintyRate: 3.0,
		CreatedAt: now.Add(-24 * time.Hour), PublishedAt: now.Add(-24 * time.Hour),
	}
	v2.Fingerprint = fingerprintVersion(v2)
	s.Versions[v1.ID], s.Versions[v2.ID] = v1, v2
	rep.Versions = []string{v1.ID, v2.ID}

	readings := []struct {
		id, why                 string
		sampledAgo, receivedAgo time.Duration
		val                     float64
		unit                    string
	}{
		{"rd-204-old", "旧版本期间采样并接收：冻结在 v1", -48 * time.Hour, -48 * time.Hour, 8, "V"},
		{"rd-204-new", "新版本期间：当前 v2 校准", -2 * time.Hour, -2 * time.Hour, 8, "V"},
		{"rd-204-rollback", "设备时钟回拨：v2 期接收但声明 v1 期采样", -48 * time.Hour, -1 * time.Hour, 4, "V"},
		{"rd-204-over", "超过满量程：v2 显式允许外推", -1 * time.Hour, -1 * time.Hour, 10.8, "V"},
	}
	for _, rc := range readings {
		r := &Reading{
			ID: rc.id, DeviceID: dev.ID, RawValue: rc.val, RawUnit: rc.unit,
			SampledAt: now.Add(rc.sampledAgo), ReceivedAt: now.Add(rc.receivedAgo),
			Source: rc.why, CreatedAt: now,
		}
		s.Readings[r.ID] = r
		s.Results[r.ID] = s.calibrateLocked(r, dev)
		rep.Readings = append(rep.Readings, r.ID)
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return rep, nil
}
