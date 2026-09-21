package service

import (
	"fmt"
	"math"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
)

type CompareRequest struct {
	LeftCurveID  string   `json:"left_curve_id"`
	RightCurveID string   `json:"right_curve_id"`
	DeviceID     string   `json:"device_id"`
	SampleIDs    []string `json:"sample_ids"`
}

type ComparePoint struct {
	SampleID string          `json:"sample_id"`
	Left     CalibrationView `json:"left"`
	Right    CalibrationView `json:"right"`
	Delta    float64         `json:"delta"`
}

type CalibrationView struct {
	CurveID      string            `json:"curve_id"`
	Value        float64           `json:"value"`
	Uncertainty  model.Uncertainty `json:"uncertainty"`
	SegmentID    string            `json:"segment_id"`
	Interval     string            `json:"interval"`
	Extrapolated bool              `json:"extrapolated"`
	Error        string            `json:"error,omitempty"`
}

type CompareResponse struct {
	Points []ComparePoint `json:"points"`
}

func hypothetical(d *model.Data, c *model.CurveVersion, dev model.Device, row model.RawReading) CalibrationView {
	di, _ := unit(d.Units, dev.InputUnitID)
	ru, _ := unit(d.Units, row.RawUnitID)
	x, err := calib.ConvertValue(row.RawValue, ru, di)
	if err != nil {
		return CalibrationView{CurveID: c.ID, Error: err.Error()}
	}
	ev, err := calib.Evaluate(*c, x)
	if err != nil {
		return CalibrationView{CurveID: c.ID, Error: err.Error()}
	}
	u := model.Uncertainty{Combined: ev.Segment.Uncertainty, Components: []model.UncertaintyComponent{{Name: "segment", Value: ev.Segment.Uncertainty}}}
	if ev.Extrapolated {
		u = combineUncertainty(ev.Segment.Uncertainty, c.ExtraUncertainty)
	}
	return CalibrationView{CurveID: c.ID, Value: ev.Y, Uncertainty: u, SegmentID: ev.Segment.ID, Interval: ev.Interval, Extrapolated: ev.Extrapolated}
}

func combineUncertainty(base, extra float64) model.Uncertainty {
	return model.Uncertainty{Combined: sqrtSumSquares(base, extra), Components: []model.UncertaintyComponent{{Name: "segment", Value: base}, {Name: "explicit_extrapolation", Value: extra}}}
}

func (s *Service) CompareCurves(req CompareRequest) (CompareResponse, error) {
	d := s.Snapshot()
	left := curveAt(d.Curves, req.LeftCurveID)
	right := curveAt(d.Curves, req.RightCurveID)
	if left == nil || right == nil {
		return CompareResponse{}, fmt.Errorf("both curve versions must exist")
	}
	dev, ok := device(d.Devices, req.DeviceID)
	if !ok {
		return CompareResponse{}, fmt.Errorf("device not found")
	}
	if left.DeviceID != req.DeviceID || right.DeviceID != req.DeviceID {
		return CompareResponse{}, fmt.Errorf("curves must belong to requested device")
	}
	ids := req.SampleIDs
	out := CompareResponse{Points: []ComparePoint{}}
	for _, id := range ids {
		r := readingAt(d.Readings, req.DeviceID, id)
		if r == nil {
			return CompareResponse{}, fmt.Errorf("sample %q not found", id)
		}
		lv, rv := hypothetical(&d, left, dev, *r), hypothetical(&d, right, dev, *r)
		out.Points = append(out.Points, ComparePoint{SampleID: id, Left: lv, Right: rv, Delta: rv.Value - lv.Value})
	}
	return out, nil
}

func (s *Service) Export() model.Data { return s.Snapshot() }

func (s *Service) ValidateImport(d model.Data) (model.Data, error) {
	out := cloneExport(d)
	for i := range out.Curves {
		normalizeCurve(&out.Curves[i])
	}
	if err := s.validateAll(out); err != nil {
		return model.Data{}, err
	}
	return out, nil
}

func (s *Service) ImportData(d model.Data) error {
	valid, err := s.ValidateImport(d)
	if err != nil {
		return err
	}
	return s.store.Replace(valid)
}

func cloneExport(d model.Data) model.Data {
	out := d
	out.Curves = append([]model.CurveVersion(nil), d.Curves...)
	out.Readings = append([]model.RawReading(nil), d.Readings...)
	out.Results = append([]model.CalibratedResult(nil), d.Results...)
	return out
}

func (s *Service) validateAll(d model.Data) error { return validateData(d) }

func sqrtSumSquares(a, b float64) float64 { return math.Sqrt(a*a + b*b) }
