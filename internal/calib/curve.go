package calib

import (
	"fmt"
	"math"
	"sort"

	"calibration-trace/internal/model"
)

const eps = 1e-12

func ValidateCurve(c model.CurveVersion) error {
	if c.DeviceID == "" {
		return fmt.Errorf("device id is required")
	}
	if len(c.Segments) == 0 {
		return fmt.Errorf("curve must contain at least one segment")
	}
	if c.InputUnitID == "" || c.OutputUnitID == "" {
		return fmt.Errorf("curve input and output units are required")
	}
	if _, err := ParseTime(c.ValidFrom, "valid_from"); err != nil {
		return err
	}
	if c.ValidUntil != "" {
		until, err := ParseTime(c.ValidUntil, "valid_until")
		if err != nil {
			return err
		}
		from, _ := ParseTime(c.ValidFrom, "valid_from")
		if !until.After(from) {
			return fmt.Errorf("valid_until must be after valid_from")
		}
	}
	if c.Extrapolation && c.ExtraUncertainty < 0 {
		return fmt.Errorf("extra uncertainty must be non-negative")
	}
	if !c.Extrapolation && c.ExtraUncertainty != 0 {
		return fmt.Errorf("extra uncertainty requires extrapolation to be enabled")
	}
	seen := map[string]bool{}
	for i := range c.Segments {
		if err := validateSegment(c.Segments[i]); err != nil {
			return fmt.Errorf("segment %d: %w", i+1, err)
		}
		if seen[c.Segments[i].ID] {
			return fmt.Errorf("duplicate segment id %q", c.Segments[i].ID)
		}
		seen[c.Segments[i].ID] = true
	}
	segs := append([]model.Segment(nil), c.Segments...)
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].XMin != segs[j].XMin {
			return segs[i].XMin < segs[j].XMin
		}
		return segs[i].XMax < segs[j].XMax
	})
	for i := 1; i < len(segs); i++ {
		prev, cur := segs[i-1], segs[i]
		if math.Abs(cur.XMin-prev.XMin) <= eps {
			return fmt.Errorf("segments %q and %q share an x_min; declared spans must not overlap", prev.ID, cur.ID)
		}
		if cur.XMin < prev.XMax-eps {
			return fmt.Errorf("segments %q and %q overlap", prev.ID, cur.ID)
		}
		if math.Abs(cur.XMin-prev.XMax) <= eps && prev.MaxInclusive == cur.MinInclusive {
			if prev.MaxInclusive {
				return fmt.Errorf("segments %q and %q both own endpoint %g", prev.ID, cur.ID, prev.XMax)
			}
			return fmt.Errorf("segments %q and %q both leave endpoint %g unowned", prev.ID, cur.ID, prev.XMax)
		}
	}
	return nil
}

func validateSegment(s model.Segment) error {
	if s.ID == "" {
		return fmt.Errorf("id is required")
	}
	if !(s.XMin < s.XMax) {
		return fmt.Errorf("x_min must be strictly less than x_max")
	}
	if s.Uncertainty < 0 {
		return fmt.Errorf("uncertainty must be non-negative")
	}
	switch s.Kind {
	case "linear":
		if len(s.Points) < 2 {
			return fmt.Errorf("linear segment needs at least two points")
		}
		for i, p := range s.Points {
			if i > 0 && p.X <= s.Points[i-1].X {
				return fmt.Errorf("linear points must have strictly increasing x")
			}
			if i == 0 && math.Abs(p.X-s.XMin) > eps {
				return fmt.Errorf("first linear point must equal x_min")
			}
			if i == len(s.Points)-1 && math.Abs(p.X-s.XMax) > eps {
				return fmt.Errorf("last linear point must equal x_max")
			}
			if math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
				return fmt.Errorf("linear point y must be finite")
			}
		}
	case "polynomial":
		if len(s.Coefficients) < 2 || len(s.Coefficients) > 3 {
			return fmt.Errorf("polynomial degree must be 1 or 2")
		}
		for _, a := range s.Coefficients {
			if math.IsNaN(a) || math.IsInf(a, 0) {
				return fmt.Errorf("polynomial coefficient must be finite")
			}
		}
	default:
		return fmt.Errorf("unknown segment kind %q", s.Kind)
	}
	return nil
}

func CurveCoverage(c model.CurveVersion) model.Coverage {
	segs := append([]model.Segment(nil), c.Segments...)
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].XMin == segs[j].XMin {
			return segs[i].XMax < segs[j].XMax
		}
		return segs[i].XMin < segs[j].XMin
	})
	cov := model.Coverage{XMin: segs[0].XMin, XMax: segs[len(segs)-1].XMax}
	for i := 1; i < len(segs); i++ {
		prev, cur := segs[i-1], segs[i]
		if cur.XMin > prev.XMax+eps {
			cov.Gaps = append(cov.Gaps, model.Gap{From: prev.XMax, To: cur.XMin, FromInclusive: !prev.MaxInclusive, ToInclusive: !cur.MinInclusive})
			continue
		}
		if math.Abs(cur.XMin-prev.XMax) <= eps && !prev.MaxInclusive && !cur.MinInclusive {
			cov.Gaps = append(cov.Gaps, model.Gap{From: prev.XMax, To: cur.XMin, FromInclusive: true, ToInclusive: true})
		}
	}
	return cov
}

func Contains(s model.Segment, x float64) bool {
	leftOK := x > s.XMin+eps || (math.Abs(x-s.XMin) <= eps && s.MinInclusive)
	rightOK := x < s.XMax-eps || (math.Abs(x-s.XMax) <= eps && s.MaxInclusive)
	return leftOK && rightOK
}

type EvalResult struct {
	Y            float64
	Segment      model.Segment
	Interval     string
	Extrapolated bool
}

func Evaluate(c model.CurveVersion, x float64) (EvalResult, error) {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return EvalResult{}, fmt.Errorf("input must be finite")
	}
	for _, s := range c.Segments {
		if Contains(s, x) {
			y, err := evaluateSegment(s, x)
			return EvalResult{Y: y, Segment: s, Interval: intervalString(s), Extrapolated: false}, err
		}
	}
	cov := CurveCoverage(c)
	if (x < cov.XMin || x > cov.XMax) && c.Extrapolation {
		segs := append([]model.Segment(nil), c.Segments...)
		sort.Slice(segs, func(i, j int) bool {
			if segs[i].XMin == segs[j].XMin {
				return segs[i].XMax < segs[j].XMax
			}
			return segs[i].XMin < segs[j].XMin
		})
		var s model.Segment
		if x < cov.XMin {
			s = segs[0]
		} else {
			s = segs[len(segs)-1]
		}
		y, err := extrapolateSegment(s, x, x < cov.XMin)
		if err != nil {
			return EvalResult{}, err
		}
		return EvalResult{Y: y, Segment: s, Interval: intervalString(s) + " external extrapolation", Extrapolated: true}, nil
	}
	if x < cov.XMin || x > cov.XMax {
		return EvalResult{}, fmt.Errorf("value %.12g is outside declared coverage [%.12g, %.12g] and extrapolation is disabled", x, cov.XMin, cov.XMax)
	}
	return EvalResult{}, fmt.Errorf("value %.12g falls in a declared coverage gap", x)
}

func intervalString(s model.Segment) string {
	l, r := "(", ")"
	if s.MinInclusive {
		l = "["
	}
	if s.MaxInclusive {
		r = "]"
	}
	return fmt.Sprintf("%s%g,%g%s", l, s.XMin, s.XMax, r)
}

func evaluateSegment(s model.Segment, x float64) (float64, error) {
	switch s.Kind {
	case "linear":
		return interpolate(s.Points, x), nil
	case "polynomial":
		y := 0.0
		for i := len(s.Coefficients) - 1; i >= 0; i-- {
			y = y*x + s.Coefficients[i]
		}
		return y, nil
	default:
		return 0, fmt.Errorf("unknown segment kind")
	}
}

func extrapolateSegment(s model.Segment, x float64, below bool) (float64, error) {
	if s.Kind == "polynomial" {
		return evaluateSegment(s, x)
	}
	if below {
		p0, p1 := s.Points[0], s.Points[1]
		return p0.Y + (x-p0.X)*(p1.Y-p0.Y)/(p1.X-p0.X), nil
	}
	p0, p1 := s.Points[len(s.Points)-2], s.Points[len(s.Points)-1]
	return p1.Y + (x-p1.X)*(p1.Y-p0.Y)/(p1.X-p0.X), nil
}

func interpolate(points []model.LinearPoint, x float64) float64 {
	if x <= points[0].X {
		return points[0].Y
	}
	last := points[len(points)-1]
	if x >= last.X {
		return last.Y
	}
	for i := 1; i < len(points); i++ {
		if x <= points[i].X {
			a, b := points[i-1], points[i]
			return a.Y + (x-a.X)*(b.Y-a.Y)/(b.X-a.X)
		}
	}
	return last.Y
}
