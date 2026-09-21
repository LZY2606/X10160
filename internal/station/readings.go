package station

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

type ReadingInput struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"device_id"`
	RawValue   float64   `json:"raw_value"`
	RawUnit    string    `json:"raw_unit"`
	SampledAt  time.Time `json:"sampled_at"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
}

type RowOutcome struct {
	Row     int                `json:"row"`
	ID      string             `json:"id"`
	Outcome string             `json:"outcome"` // imported | duplicate | conflict | error
	Message string             `json:"message,omitempty"`
	Reading *Reading           `json:"reading,omitempty"`
	Result  *CalibrationResult `json:"result,omitempty"`
}

type BatchReport struct {
	Total     int          `json:"total"`
	Imported  int          `json:"imported"`
	Duplicate int          `json:"duplicate"`
	Conflict  int          `json:"conflict"`
	Failed    int          `json:"failed"`
	Rows      []RowOutcome `json:"rows"`
}

func (s *Store) BatchImport(rows []ReadingInput) (*BatchReport, error) {
	if len(rows) == 0 {
		return nil, errors.New("没有可导入的行")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	report := &BatchReport{Total: len(rows)}
	for i, in := range rows {
		out := s.importOneLocked(i, in)
		switch out.Outcome {
		case "imported":
			report.Imported++
		case "duplicate":
			report.Duplicate++
		case "conflict":
			report.Conflict++
		default:
			report.Failed++
		}
		report.Rows = append(report.Rows, out)
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return report, nil
}

func sameFact(a, b float64) bool { return canonFloat(a) == canonFloat(b) }

func (s *Store) importOneLocked(row int, in ReadingInput) RowOutcome {
	out := RowOutcome{Row: row, ID: in.ID}
	if in.ID == "" {
		out.Outcome, out.Message = "error", "样本 id 不能为空"
		return out
	}
	if in.DeviceID == "" {
		out.Outcome, out.Message = "error", "设备 id 不能为空"
		return out
	}
	dev, ok := s.Devices[in.DeviceID]
	if !ok {
		out.Outcome, out.Message = "error", (&NotFoundError{"设备 " + in.DeviceID}).Error()
		return out
	}
	if _, ok := s.Units[in.RawUnit]; !ok {
		out.Outcome, out.Message = "error", (&UnknownUnitError{in.RawUnit}).Error()
		return out
	}
	if dim, _ := s.unitDimension(in.RawUnit); dim != s.Units[dev.RawUnit].Dimension {
		out.Outcome = "error"
		out.Message = fmt.Sprintf("量纲不匹配: 读数单位 %s(%s) 不能用于设备原始单位 %s(%s)",
			in.RawUnit, dim, dev.RawUnit, s.Units[dev.RawUnit].Dimension)
		return out
	}
	if in.SampledAt.IsZero() {
		out.Outcome, out.Message = "error", "采样时刻不能为空"
		return out
	}
	receivedAt := in.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	receivedAt, in.SampledAt = normTime(receivedAt), normTime(in.SampledAt)
	if existing, ok := s.Readings[in.ID]; ok {
		switch {
		case existing.DeviceID != in.DeviceID:
			out.Outcome, out.Message = "conflict",
				fmt.Sprintf("同 id 样本已属于设备 %s，不能改绑设备", existing.DeviceID)
		case !sameFact(existing.RawValue, in.RawValue):
			out.Outcome = "conflict"
			out.Message = fmt.Sprintf("同 id 不同原始值: 已有 %g %s，新行 %g %s（原始事实不可改写）",
				existing.RawValue, existing.RawUnit, in.RawValue, in.RawUnit)
		case existing.RawUnit != in.RawUnit:
			out.Outcome, out.Message = "conflict", "同 id 不同原始单位: "+existing.RawUnit+" vs "+in.RawUnit
		case !existing.SampledAt.Equal(in.SampledAt):
			out.Outcome, out.Message = "conflict", "同 id 不同采样时刻"
		default:
			out.Outcome = "duplicate"
			out.Message = "样本已存在且原始事实一致；历史结果保持冻结"
			out.Reading = existing
			out.Result = s.Results[in.ID]
		}
		return out
	}
	reading := &Reading{
		ID: in.ID, DeviceID: in.DeviceID, RawValue: in.RawValue, RawUnit: in.RawUnit,
		SampledAt: in.SampledAt, ReceivedAt: receivedAt, Source: in.Source,
		CreatedAt: normTime(time.Now()),
	}
	result := s.calibrateLocked(reading, dev)
	s.Readings[in.ID] = reading
	s.Results[in.ID] = result
	out.Outcome = "imported"
	out.Reading, out.Result = reading, result
	return out
}

func inWindow(from, to, t time.Time) bool {
	if t.Before(from) {
		return false
	}
	if !to.IsZero() && !t.Before(to) {
		return false // half-open [from,to)
	}
	return true
}

// calibrateLocked chooses the version confirmed at receive time and verifies
// it also covers the declared sampling instant. The decision and the curve
// fingerprint are frozen into the result so later curve publication can never
// rewrite history.
func (s *Store) calibrateLocked(r *Reading, dev *Device) *CalibrationResult {
	now := normTime(time.Now())
	res := &CalibrationResult{
		ReadingID: r.ID, DeviceID: r.DeviceID, RawValue: r.RawValue, RawUnit: r.RawUnit,
		CalUnit: dev.CalibratedUnit, Status: StatusNoVersion, CreatedAt: now,
		Selection: SelectionReason{ReceivedAt: r.ReceivedAt, SampledAt: r.SampledAt},
	}
	// Convert the declared raw fact into the device's curve domain unit. The
	// stored reading keeps the declared value/unit untouched.
	x, _, _, err := s.convertPoint(r.RawValue, r.RawUnit, dev.RawUnit)
	if err != nil {
		res.Status = "unit_error"
		res.Selection.Status, res.Selection.Why = res.Status, err.Error()
		res.Fingerprint = fingerprintResult(res)
		return res
	}
	var confirmed []*CurveVersion
	for _, v := range s.Versions {
		if v.DeviceID != dev.ID || v.State != StatePublished {
			continue
		}
		if v.PublishedAt.After(r.ReceivedAt) {
			continue
		}
		if inWindow(v.ValidFrom, v.ValidTo, r.ReceivedAt) {
			confirmed = append(confirmed, v)
		}
	}
	sort.Slice(confirmed, func(i, j int) bool { return confirmed[i].ID < confirmed[j].ID })
	if len(confirmed) == 0 {
		res.Selection.Status = StatusNoVersion
		res.Selection.Why = "接收时刻没有已发布且处于有效期内的曲线版本；不默认使用其他版本"
		res.Fingerprint = fingerprintResult(res)
		return res
	}
	if len(confirmed) > 1 {
		ids := make([]string, len(confirmed))
		for i, v := range confirmed {
			ids[i] = v.ID
		}
		res.Status, res.Selection.Status = StatusAmbiguous, StatusAmbiguous
		res.Selection.Candidates, res.Selection.Why = ids, "接收时刻有多个生效版本，选择不唯一，拒绝猜测"
		res.Fingerprint = fingerprintResult(res)
		return res
	}
	v := confirmed[0]
	res.VersionID, res.VersionFingerprint = v.ID, v.Fingerprint
	res.Selection.VersionID = v.ID
	res.Selection.WindowValidFrom, res.Selection.WindowValidTo = v.ValidFrom, v.ValidTo
	if !inWindow(v.ValidFrom, v.ValidTo, r.SampledAt) {
		res.Status = StatusDeviceTimeBack
		res.Selection.Status = StatusDeviceTimeBack
		res.Selection.Why = "设备时间疑似回拨：接收时确认的版本不覆盖声明的采样时刻，不回退到旧曲线"
		res.Fingerprint = fingerprintResult(res)
		return res
	}
	eval, evalErr := v.eval(x)
	res.Selection.SegmentLabel = eval.Segment.Label
	res.Selection.SegmentType = eval.Segment.Type
	if chosenSegmentIsDeclaredOverlap(v.Segments, eval.SegmentIndex, x) {
		res.Selection.DeclaredOverlap = true
	}
	if evalErr != nil {
		rerr := &RangeError{}
		if errors.As(evalErr, &rerr) {
			res.Status = StatusOutOfRange
			res.Selection.Status = StatusOutOfRange
			switch {
			case eval.Distance > 0 && v.AllowExtrapolate:
				res.Selection.Why = fmt.Sprintf("读数 %g 在%s边界外 %g；边界区段不可用于线性外推，拒绝求值",
					x, side(eval.Below), eval.Distance)
			case eval.Distance > 0:
				res.Selection.Why = fmt.Sprintf("读数 %g 超出曲线覆盖范围（%s 边界外 %g）；该版本未启用外推，拒绝默认外推",
					x, side(eval.Below), eval.Distance)
			default:
				res.Selection.Why = "读数落在曲线覆盖的内部间隙内；间隙不存在可依据的曲线区段，拒绝猜测/外推"
			}
			res.Fingerprint = fingerprintResult(res)
			return res
		}
	}
	res.CalValue = eval.Value
	res.SegmentLabel, res.SegmentType = eval.Segment.Label, eval.Segment.Type
	res.ReferenceIDs = eval.ReferenceIDs
	res.Status, res.Selection.Status = StatusCalibrated, StatusCalibrated
	res.Selection.Why = "按接收时确认且覆盖采样时刻的唯一已发布版本求值"
	// Uncertainty assembly (all terms standard uncertainties, calibrated unit).
	var refSq, interp float64
	for _, rid := range eval.ReferenceIDs {
		if rp, ok := s.ReferencePoints[rid]; ok {
			res.RefTraceSet(*rp)
			if u, err := s.refUncertaintyIn(rp, dev.CalibratedUnit); err == nil {
				refSq += u * u
			}
		}
	}
	refTerm := math.Sqrt(refSq)
	interp = eval.RefUnc
	extrapTerm := 0.0
	if !eval.InCoverage {
		res.Extrapolated = true
		extrapTerm = v.ExtrapUncertaintyRate * eval.Distance
		res.Selection.Why = fmt.Sprintf("读数在覆盖范围外，按版本显式授权做线性外推，距离 %g，附加不确定度 %g",
			eval.Distance, extrapTerm)
	}
	total := math.Sqrt(refTerm*refTerm + interp*interp + extrapTerm*extrapTerm)
	res.Uncertainty = UncertaintyDetail{
		Reference: refTerm, Interp: interp, Extrap: extrapTerm, Total: total,
		ExtrapRate: v.ExtrapUncertaintyRate,
	}
	res.Fingerprint = fingerprintResult(res)
	return res
}

func (s *Store) refUncertaintyIn(rp *ReferencePoint, unit string) (float64, error) {
	v, _, _, err := s.convertPoint(rp.Uncertainty, rp.Unit, unit)
	return v, err
}

func side(below bool) string {
	if below {
		return "下限"
	}
	return "上限"
}

// chosenSegmentIsDeclaredOverlap reports whether the selected point is also
// covered by another segment of the same declared overlap group.
func chosenSegmentIsDeclaredOverlap(segs []Segment, chosen int, x float64) bool {
	cs := segs[chosen]
	if cs.OverlapGroup == "" {
		return false
	}
	for i := range segs {
		if i == chosen || segs[i].OverlapGroup != cs.OverlapGroup {
			continue
		}
		if segs[i].contains(x) {
			return true
		}
	}
	return false
}
