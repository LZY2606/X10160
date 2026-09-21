package station

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

func (s *Store) ListReadings(deviceID string) []*Reading {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Reading, 0)
	for _, r := range s.Readings {
		if deviceID != "" && r.DeviceID != deviceID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceID != out[j].DeviceID {
			return out[i].DeviceID < out[j].DeviceID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

type Trace struct {
	Reading    *Reading                  `json:"reading"`
	Result     *CalibrationResult        `json:"result"`
	Device     *Device                   `json:"device"`
	Version    *CurveVersion             `json:"version,omitempty"`
	Segment    *Segment                  `json:"segment,omitempty"`
	References map[string]ReferencePoint `json:"references"`
}

func (s *Store) Trace(readingID string) (*Trace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.Readings[readingID]
	if !ok {
		return nil, &NotFoundError{"样本 " + readingID}
	}
	res := s.Results[readingID]
	t := &Trace{Reading: r, Result: res, References: map[string]ReferencePoint{}}
	if d, ok := s.Devices[r.DeviceID]; ok {
		t.Device = d
	}
	if res != nil && res.VersionID != "" {
		if v, ok := s.Versions[res.VersionID]; ok {
			t.Version = v
			for i := range v.Segments {
				if v.Segments[i].Label == res.SegmentLabel && v.Segments[i].Type == res.SegmentType {
					seg := v.Segments[i]
					t.Segment = &seg
					break
				}
			}
		}
		for _, rid := range res.ReferenceIDs {
			if rp, ok := s.ReferencePoints[rid]; ok {
				t.References[rid] = *rp
			}
		}
	}
	return t, nil
}

// WhatIfValue is a non-destructive re-evaluation of one stored sample
// against an arbitrary version. No stored result is modified.
type WhatIfValue struct {
	ReadingID    string            `json:"reading_id"`
	DeviceID     string            `json:"device_id"`
	RawValue     float64           `json:"raw_value"`
	RawUnit      string            `json:"raw_unit"`
	DomainValue  float64           `json:"domain_value"`
	DomainUnit   string            `json:"domain_unit"`
	Status       string            `json:"status"`
	Why          string            `json:"why"`
	CalValue     float64           `json:"cal_value,omitempty"`
	SegmentLabel string            `json:"segment_label,omitempty"`
	SegmentType  string            `json:"segment_type,omitempty"`
	Extrapolated bool              `json:"extrapolated"`
	Distance     float64           `json:"distance,omitempty"`
	Uncertainty  UncertaintyDetail `json:"uncertainty"`
	ReferenceIDs []string          `json:"reference_ids"`
}

func (s *Store) whatIfLocked(v *CurveVersion, r *Reading, dev *Device) WhatIfValue {
	out := WhatIfValue{
		ReadingID: r.ID, DeviceID: dev.ID, RawValue: r.RawValue, RawUnit: r.RawUnit,
		DomainUnit: dev.RawUnit, Status: StatusCalibrated,
	}
	x, _, _, err := s.convertPoint(r.RawValue, r.RawUnit, dev.RawUnit)
	if err != nil {
		out.Status, out.Why = "unit_error", err.Error()
		return out
	}
	out.DomainValue = x
	eval, evalErr := v.eval(x)
	out.SegmentLabel, out.SegmentType = eval.Segment.Label, eval.Segment.Type
	out.ReferenceIDs = append([]string(nil), eval.ReferenceIDs...)
	if evalErr != nil {
		out.Status, out.Distance = StatusOutOfRange, eval.Distance
		out.Why = fmt.Sprintf("超出覆盖范围（%s 边界外 %g），外推=%v",
			side(eval.Below), eval.Distance, v.AllowExtrapolate)
		return out
	}
	out.CalValue = eval.Value
	if !eval.InCoverage {
		out.Extrapolated = true
		out.Distance = eval.Distance
		out.Why = fmt.Sprintf("版本显式授权外推，距离 %g", eval.Distance)
	}
	var refSq float64
	for _, rid := range eval.ReferenceIDs {
		if rp, ok := s.ReferencePoints[rid]; ok {
			if u, err := s.refUncertaintyIn(rp, dev.CalibratedUnit); err == nil {
				refSq += u * u
			}
		}
	}
	refTerm := math.Sqrt(refSq)
	extrapTerm := 0.0
	if out.Extrapolated {
		extrapTerm = v.ExtrapUncertaintyRate * eval.Distance
	}
	total := math.Sqrt(refTerm*refTerm + eval.RefUnc*eval.RefUnc + extrapTerm*extrapTerm)
	out.Uncertainty = UncertaintyDetail{
		Reference: refTerm, Interp: eval.RefUnc, Extrap: extrapTerm, Total: total,
		ExtrapRate: v.ExtrapUncertaintyRate,
	}
	return out
}

type CompareReport struct {
	VersionA string        `json:"version_a"`
	VersionB string        `json:"version_b"`
	Note     string        `json:"note"`
	Items    []CompareItem `json:"items"`
}

type CompareItem struct {
	ReadingID    string        `json:"reading_id"`
	Frozen       FrozenSummary `json:"frozen"`
	A            WhatIfValue   `json:"a"`
	B            WhatIfValue   `json:"b"`
	DeltaBMinusA float64       `json:"delta_b_minus_a"`
	DeltaBFrozen float64       `json:"delta_b_vs_frozen"`
}

type FrozenSummary struct {
	VersionID string  `json:"version_id"`
	Status    string  `json:"status"`
	CalValue  float64 `json:"cal_value,omitempty"`
}

// CompareVersions evaluates stored samples against two arbitrary versions
// (drafts allowed) for impact analysis. It never writes anything and never
// recomputes the frozen stored results; those are echoed for reference.
func (s *Store) CompareVersions(versionA, versionB, deviceID string, readingIDs []string) (*CompareReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	va, ok := s.Versions[versionA]
	if !ok {
		return nil, &NotFoundError{"曲线版本 " + versionA}
	}
	vb, ok := s.Versions[versionB]
	if !ok {
		return nil, &NotFoundError{"曲线版本 " + versionB}
	}
	ids := readingIDs
	if len(ids) == 0 {
		for id, r := range s.Readings {
			if deviceID == "" || r.DeviceID == deviceID {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
	}
	rep := &CompareReport{VersionA: versionA, VersionB: versionB,
		Note: "对比结果为即时计算，不写回、不改写任何历史结果"}
	for _, id := range ids {
		r, ok := s.Readings[id]
		if !ok {
			return nil, &NotFoundError{"样本 " + id}
		}
		dev := s.Devices[r.DeviceID]
		item := CompareItem{ReadingID: id}
		if frozen := s.Results[id]; frozen != nil {
			item.Frozen = FrozenSummary{VersionID: frozen.VersionID, Status: frozen.Status, CalValue: frozen.CalValue}
		}
		item.A = s.whatIfLocked(va, r, dev)
		item.B = s.whatIfLocked(vb, r, dev)
		if item.A.Status == StatusCalibrated && item.B.Status == StatusCalibrated {
			item.DeltaBMinusA = item.B.CalValue - item.A.CalValue
		}
		if item.B.Status == StatusCalibrated && item.Frozen.Status == StatusCalibrated {
			item.DeltaBFrozen = item.B.CalValue - item.Frozen.CalValue
		}
		rep.Items = append(rep.Items, item)
	}
	return rep, nil
}

// ConvertDisplay converts a value for display into a compatible unit. It
// returns the derived value only; the raw fact is never rewritten.
func (s *Store) ConvertDisplay(value float64, from, to string) (float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, _, _, err := s.convertPoint(value, from, to)
	return v, err
}

func (s *Store) Snapshot() Bundle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Bundle
}

// MergeImport merges an external bundle into the store. Fingerprints are
// verified before any merge; conflicting primary keys abort the whole import
// so partial foreign data can never silently overwrite local facts.
func (s *Store) MergeImport(b *Bundle) (int, error) {
	if b == nil {
		return 0, errors.New("导入内容为空")
	}
	if err := verifyBundle(b); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range b.Versions {
		if local, ok := s.Versions[id]; ok && local.Fingerprint != v.Fingerprint {
			return 0, &ConflictError{fmt.Sprintf("曲线版本 %s 已存在且指纹不同，拒绝合并", id)}
		}
	}
	for id, r := range b.Results {
		if local, ok := s.Results[id]; ok && local.Fingerprint != r.Fingerprint {
			return 0, &ConflictError{fmt.Sprintf("结果 %s 已存在且指纹不同（历史冲突），拒绝合并", id)}
		}
	}
	for id, r := range b.Readings {
		if local, ok := s.Readings[id]; ok && fingerprintReading(local) != fingerprintReading(r) {
			return 0, &ConflictError{fmt.Sprintf("样本 %s 已存在且原始事实不同，拒绝合并", id)}
		}
	}
	count := 0
	for name, u := range b.Units {
		if _, ok := s.Units[name]; !ok {
			s.Units[name] = u
			count++
		}
	}
	for id, d := range b.Devices {
		if _, ok := s.Devices[id]; !ok {
			s.Devices[id] = d
			count++
		}
	}
	for id, r := range b.ReferencePoints {
		if _, ok := s.ReferencePoints[id]; !ok {
			s.ReferencePoints[id] = r
			count++
		}
	}
	for id, v := range b.Versions {
		if _, ok := s.Versions[id]; !ok {
			s.Versions[id] = v
			count++
		}
	}
	for id, r := range b.Readings {
		if _, ok := s.Readings[id]; !ok {
			s.Readings[id] = r
			count++
		}
	}
	for id, r := range b.Results {
		if _, ok := s.Results[id]; !ok {
			s.Results[id] = r
			count++
		}
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return count, nil
}
