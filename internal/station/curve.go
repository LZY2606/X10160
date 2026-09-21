package station

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	StateDraft     = "draft"
	StatePublished = "published"
)

type CurveVersion struct {
	ID               string    `json:"id"`
	DeviceID         string    `json:"device_id"`
	Revision         int64     `json:"revision"`
	State            string    `json:"state"`
	Note             string    `json:"note"`
	Segments         []Segment `json:"segments"`
	ValidFrom        time.Time `json:"valid_from"`
	ValidTo          time.Time `json:"valid_to"`
	AllowExtrapolate bool      `json:"allow_extrapolate"`
	// Extra absolute uncertainty added per unit of distance outside coverage.
	ExtrapUncertaintyRate float64   `json:"extrap_uncertainty_rate"`
	CreatedAt             time.Time `json:"created_at"`
	PublishedAt           time.Time `json:"published_at,omitempty"`
	Fingerprint           string    `json:"fingerprint"`
}

type RangeError struct{ Kind string }

func (e *RangeError) Error() string { return e.Kind }

var (
	ErrNoSegment             = errors.New("no segment contains the point")
	ErrExtrapolationDisabled = errors.New("extrapolation is not enabled for this version")
)

type ValidationIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	SegmentA int    `json:"segment_a,omitempty"`
	SegmentB int    `json:"segment_b,omitempty"`
}

func (v *CurveVersion) validateSegments() []ValidationIssue {
	var issues []ValidationIssue
	if len(v.Segments) == 0 {
		issues = append(issues, ValidationIssue{Code: "empty", Message: "曲线至少需要一个区段"})
		return issues
	}
	for i := range v.Segments {
		s := &v.Segments[i]
		if s.Type != SegLinear && s.Type != SegPolynomial {
			issues = append(issues, ValidationIssue{Code: "bad_type", Message: fmt.Sprintf("区段 %d 类型无效: %q", i, s.Type), SegmentA: i})
			continue
		}
		if math.IsNaN(s.Lower) || math.IsNaN(s.Upper) || math.IsInf(s.Lower, 0) || math.IsInf(s.Upper, 0) {
			issues = append(issues, ValidationIssue{Code: "bad_endpoint", Message: fmt.Sprintf("区段 %d 端点不是有限数", i), SegmentA: i})
			continue
		}
		if s.Lower > s.Upper || (s.Lower == s.Upper && !(s.LowerClosed && s.UpperClosed)) {
			issues = append(issues, ValidationIssue{Code: "bad_interval", Message: fmt.Sprintf("区段 %d 区间端点无效", i), SegmentA: i})
		}
		switch s.Type {
		case SegLinear:
			if len(s.Knots) != 2 {
				issues = append(issues, ValidationIssue{Code: "bad_knots", Message: fmt.Sprintf("线性区段 %d 必须恰好有两个节点", i), SegmentA: i})
			} else if s.Knots[0].X != s.Lower || s.Knots[1].X != s.Upper {
				issues = append(issues, ValidationIssue{Code: "bad_knots", Message: fmt.Sprintf("线性区段 %d 节点 x 必须与区间端点一致", i), SegmentA: i})
			}
		case SegPolynomial:
			if len(s.Coeffs) < 2 || len(s.Coeffs) > 4 {
				issues = append(issues, ValidationIssue{Code: "bad_poly", Message: fmt.Sprintf("多项式区段 %d 阶数必须在 1..3 之间", i), SegmentA: i})
			}
			for _, c := range s.Coeffs {
				if math.IsNaN(c) || math.IsInf(c, 0) {
					issues = append(issues, ValidationIssue{Code: "bad_poly", Message: fmt.Sprintf("多项式区段 %d 系数无效", i), SegmentA: i})
					break
				}
			}
		}
		if s.Uncertainty < 0 {
			issues = append(issues, ValidationIssue{Code: "bad_uncertainty", Message: fmt.Sprintf("区段 %d 不确定度不能为负", i), SegmentA: i})
		}
	}
	for i := 0; i < len(v.Segments); i++ {
		for j := i + 1; j < len(v.Segments); j++ {
			if interiorOverlap(v.Segments[i], v.Segments[j]) && !declaredOverlap(v.Segments[i], v.Segments[j]) {
				issues = append(issues, ValidationIssue{
					Code: "undeclared_overlap", SegmentA: i, SegmentB: j,
					Message: fmt.Sprintf("区段 %d 与 %d 存在未声明的覆盖重叠", i, j),
				})
			}
		}
	}
	if v.AllowExtrapolate && v.ExtrapUncertaintyRate < 0 {
		issues = append(issues, ValidationIssue{Code: "bad_extrap_rate", Message: "外推附加不确定度比率不能为负"})
	}
	return issues
}

// interiorOverlap reports whether two intervals share interior points
// (identical single-point closed intervals count as overlap). Touching
// endpoints (left-closed/right-open boundary style) are allowed.
func interiorOverlap(a, b Segment) bool {
	lo := math.Max(a.Lower, b.Lower)
	hi := math.Min(a.Upper, b.Upper)
	if lo > hi {
		return false
	}
	if lo < hi {
		// There is a positive-length overlap. Exclude only the case where the
		// shared point is open on both sides at a zero-length intersection.
		return true
	}
	// Intersection is the single point lo == hi: it is an overlap only if
	// both intervals include it.
	return a.contains(lo) && b.contains(lo)
}

func declaredOverlap(a, b Segment) bool {
	return a.OverlapGroup != "" && a.OverlapGroup == b.OverlapGroup
}

type Span struct {
	Lower       float64 `json:"lower"`
	Upper       float64 `json:"upper"`
	LowerClosed bool    `json:"lower_closed"`
	UpperClosed bool    `json:"upper_closed"`
}

// CoverageView describes merged coverage and interior gaps for a version.
type CoverageView struct {
	Segments []Segment `json:"segments"`
	Covered  []Span    `json:"covered"`
	Gaps     []Span    `json:"gaps"`
	Min      float64   `json:"min"`
	Max      float64   `json:"max"`
}

func (v *CurveVersion) Coverage() CoverageView {
	segs := make([]Segment, len(v.Segments))
	copy(segs, v.Segments)
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].Lower != segs[j].Lower {
			return segs[i].Lower < segs[j].Lower
		}
		return segs[i].Upper < segs[j].Upper
	})
	view := CoverageView{Segments: segs}
	if len(segs) == 0 {
		return view
	}
	// Merge intervals ignoring overlap-group dimension for the overall span.
	type acc struct{ sp Span }
	cur := Span{Lower: segs[0].Lower, Upper: segs[0].Upper, LowerClosed: segs[0].LowerClosed, UpperClosed: segs[0].UpperClosed}
	for i := 1; i < len(segs); i++ {
		s := segs[i]
		if s.Lower < cur.Upper || (s.Lower == cur.Upper && (s.LowerClosed || cur.UpperClosed)) {
			// overlap or touching inclusive -> merge
			if s.Upper > cur.Upper {
				cur.Upper, cur.UpperClosed = s.Upper, s.UpperClosed
			} else if s.Upper == cur.Upper {
				cur.UpperClosed = cur.UpperClosed || s.UpperClosed
			}
			continue
		}
		// gap between cur.Upper and s.Lower (half-open semantics)
		gap := Span{Lower: cur.Upper, Upper: s.Lower}
		view.Gaps = append(view.Gaps, gap)
		view.Covered = append(view.Covered, cur)
		cur = Span{Lower: s.Lower, Upper: s.Upper, LowerClosed: s.LowerClosed, UpperClosed: s.UpperClosed}
	}
	view.Covered = append(view.Covered, cur)
	view.Min = view.Covered[0].Lower
	view.Max = view.Covered[len(view.Covered)-1].Upper
	return view
}

// EvalOutcome is the version-level evaluation of one domain point.
type EvalOutcome struct {
	Value        float64
	Segment      Segment
	SegmentIndex int
	InCoverage   bool
	Below        bool
	Distance     float64
	ReferenceIDs []string
	RefUnc       float64
}

func (v *CurveVersion) eval(x float64) (EvalOutcome, error) {
	var hits []int
	for i := range v.Segments {
		if v.Segments[i].contains(x) {
			hits = append(hits, i)
		}
	}
	out := EvalOutcome{}
	if len(hits) > 0 {
		idx := selectHit(v.Segments, hits)
		s := v.Segments[idx]
		out.Segment, out.SegmentIndex, out.InCoverage = s, idx, true
		out.ReferenceIDs = append([]string(nil), s.Refs...)
		out.RefUnc = s.Uncertainty
		switch s.Type {
		case SegLinear:
			k0, k1 := s.Knots[0], s.Knots[1]
			t := (x - k0.X) / (k1.X - k0.X)
			out.Value = k0.Y + t*(k1.Y-k0.Y)
		case SegPolynomial:
			z := x - s.Center
			pow := 1.0
			for _, c := range s.Coeffs {
				out.Value += c * pow
				pow *= z
			}
		}
		return out, nil
	}
	cov := v.Coverage()
	if x < cov.Min {
		out.Below, out.Distance = true, cov.Min-x
	} else {
		out.Below, out.Distance = false, x-cov.Max
	}
	if !v.AllowExtrapolate {
		return out, &RangeError{Kind: StatusOutOfRange}
	}
	// Extrapolation uses the slope of the boundary linear segment; polynomial
	// boundary segments are not extrapolated (curves would diverge).
	var idx int
	if out.Below {
		idx = boundarySegment(v.Segments, true)
	} else {
		idx = boundarySegment(v.Segments, false)
	}
	if idx < 0 || v.Segments[idx].Type != SegLinear {
		return out, &RangeError{Kind: StatusOutOfRange}
	}
	s := v.Segments[idx]
	out.Segment, out.SegmentIndex = s, idx
	k0, k1 := s.Knots[0], s.Knots[1]
	slope := (k1.Y - k0.Y) / (k1.X - k0.X)
	if out.Below {
		out.Value = k0.Y - slope*out.Distance
	} else {
		out.Value = k1.Y + slope*out.Distance
	}
	out.ReferenceIDs = append([]string(nil), s.Refs...)
	out.RefUnc = s.Uncertainty
	return out, nil
}

func selectHit(segs []Segment, hits []int) int {
	best := hits[0]
	for _, idx := range hits[1:] {
		if segs[idx].Priority < segs[best].Priority ||
			(segs[idx].Priority == segs[best].Priority && idx < best) {
			best = idx
		}
	}
	return best
}

func boundarySegment(segs []Segment, lower bool) int {
	idx := -1
	bound := 0.0
	for i := range segs {
		if segs[i].Type != SegLinear {
			continue
		}
		if lower {
			if idx == -1 || segs[i].Lower < bound || (segs[i].Lower == bound && i < idx) {
				idx, bound = i, segs[i].Lower
			}
		} else {
			if idx == -1 || segs[i].Upper > bound || (segs[i].Upper == bound && i < idx) {
				idx, bound = i, segs[i].Upper
			}
		}
	}
	return idx
}
