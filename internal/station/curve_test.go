package station

import "testing"

func TestEndpointSemantics(t *testing.T) {
	closed := Segment{Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: true}
	half := Segment{Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: false}
	if !closed.contains(0) || !closed.contains(5) {
		t.Fatal("closed interval must include both endpoints")
	}
	if half.contains(5) || !half.contains(0) {
		t.Fatal("upper-open segment must exclude upper and include lower")
	}
	open0 := Segment{Lower: 0, LowerClosed: false, Upper: 5, UpperClosed: false}
	if open0.contains(0) {
		t.Fatal("lower-open must exclude lower")
	}
}

func TestUndeclaredOverlapRejected(t *testing.T) {
	v := &CurveVersion{Segments: []Segment{
		{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 6, UpperClosed: true,
			Knots: []Knot{{0, 0}, {6, 600}}, Uncertainty: 1},
		{Type: SegLinear, Lower: 5, LowerClosed: true, Upper: 10, UpperClosed: true,
			Knots: []Knot{{5, 500}, {10, 1000}}, Uncertainty: 1},
	}}
	issues := v.validateSegments()
	found := false
	for _, is := range issues {
		if is.Code == "undeclared_overlap" {
			found = true
		}
	}
	if !found {
		t.Fatalf("interior overlap must be flagged: %+v", issues)
	}
}

func TestTouchingEndpointsAllowed(t *testing.T) {
	v := &CurveVersion{Segments: []Segment{
		{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: false,
			Knots: []Knot{{0, 0}, {5, 500}}, Uncertainty: 1},
		{Type: SegLinear, Lower: 5, LowerClosed: true, Upper: 10, UpperClosed: true,
			Knots: []Knot{{5, 500}, {10, 1000}}, Uncertainty: 1},
	}}
	for _, is := range v.validateSegments() {
		t.Fatalf("touching half-open endpoints must be valid, got %+v", is)
	}
	// Double-closed touching at the same point IS an undeclared overlap.
	v.Segments[0].UpperClosed = true
	hasOverlap := false
	for _, is := range v.validateSegments() {
		if is.Code == "undeclared_overlap" {
			hasOverlap = true
		}
	}
	if !hasOverlap {
		t.Fatal("a point covered by two closed endpoints needs a declared overlap group")
	}
}

func TestDeclaredOverlapPriority(t *testing.T) {
	v := &CurveVersion{Segments: []Segment{
		{Type: SegLinear, Label: "主", Lower: 0, LowerClosed: true, Upper: 10, UpperClosed: true, Priority: 1,
			OverlapGroup: "g1", Knots: []Knot{{0, 0}, {10, 1000}}, Uncertainty: 2},
		{Type: SegLinear, Label: "复核", Lower: 4, LowerClosed: true, Upper: 6, UpperClosed: true, Priority: 0,
			OverlapGroup: "g1", Knots: []Knot{{4, 395}, {6, 610}}, Uncertainty: 1},
	}}
	if issues := v.validateSegments(); len(issues) != 0 {
		t.Fatalf("declared overlap must validate: %+v", issues)
	}
	out, err := v.eval(5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Segment.Label != "复核" {
		t.Fatalf("priority=0 segment must win, got %s", out.Segment.Label)
	}
	if out.Value != 502.5 {
		t.Fatalf("复核 midpoint value=%v want 502.5", out.Value)
	}
	out, _ = v.eval(7)
	if out.Segment.Label != "主" {
		t.Fatalf("outside nested segment should use 主, got %s", out.Segment.Label)
	}
}

func TestCoverageGaps(t *testing.T) {
	v := &CurveVersion{Segments: []Segment{
		{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 4, UpperClosed: true,
			Knots: []Knot{{0, 0}, {4, 400}}, Uncertainty: 1},
		{Type: SegLinear, Lower: 6, LowerClosed: true, Upper: 10, UpperClosed: true,
			Knots: []Knot{{6, 600}, {10, 1000}}, Uncertainty: 1},
	}}
	cov := v.Coverage()
	if len(cov.Gaps) != 1 {
		t.Fatalf("want exactly one gap, got %+v", cov.Gaps)
	}
	g := cov.Gaps[0]
	if g.Lower != 4 || g.Upper != 6 {
		t.Fatalf("gap = %v..%v", g.Lower, g.Upper)
	}
	// A point inside the gap: without extrapolation it is out of range; with
	// extrapolation enabled the gap is treated as interior (no fallback).
	v.AllowExtrapolate = false
	if _, err := v.eval(5); err == nil {
		t.Fatal("point in coverage gap must be out of range")
	}
}

func TestPolynomialSegment(t *testing.T) {
	// y = 100 + 2*(x-5) + (x-5)^2, center 5, on [0,10]
	v := &CurveVersion{Segments: []Segment{
		{Type: SegPolynomial, Label: "p2", Lower: 0, LowerClosed: true, Upper: 10, UpperClosed: true,
			Center: 5, Coeffs: []float64{100, 2, 1}, Uncertainty: 0.5},
	}}
	out, err := v.eval(5)
	if err != nil {
		t.Fatal(err)
	}
	if out.Value != 100 {
		t.Fatalf("at center value=%v want 100", out.Value)
	}
	out, _ = v.eval(7)
	if out.Value != 100+4+4 {
		t.Fatalf("x=7 value=%v want 108", out.Value)
	}
	// Order must be low: >3 rejected.
	bad := &CurveVersion{Segments: []Segment{
		{Type: SegPolynomial, Lower: 0, LowerClosed: true, Upper: 10, UpperClosed: true, Coeffs: []float64{1, 2, 3, 4, 5}},
	}}
	if len(bad.validateSegments()) == 0 {
		t.Fatal("4th-order polynomial must be rejected (low-order only)")
	}
}

func TestBadSegmentsRejected(t *testing.T) {
	cases := []*CurveVersion{
		{Segments: nil},
		{Segments: []Segment{{Type: SegLinear, Lower: 5, LowerClosed: true, Upper: 4, UpperClosed: true,
			Knots: []Knot{{5, 1}, {4, 2}}}}},
		{Segments: []Segment{{Type: SegLinear, Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: true,
			Knots: []Knot{{0, 0}}}}},
		{Segments: []Segment{{Type: "cubic-spline", Lower: 0, LowerClosed: true, Upper: 5, UpperClosed: true}}},
	}
	for i, v := range cases {
		if len(v.validateSegments()) == 0 {
			t.Fatalf("case %d should produce issues", i)
		}
	}
}

func TestPolynomialBoundaryNoExtrapolation(t *testing.T) {
	v := &CurveVersion{
		AllowExtrapolate: true, ExtrapUncertaintyRate: 2,
		Segments: []Segment{
			{Type: SegPolynomial, Label: "p", Lower: 0, LowerClosed: true, Upper: 10, UpperClosed: true,
				Center: 5, Coeffs: []float64{100, 1}, Uncertainty: 0.5},
		},
	}
	if _, err := v.eval(11); err == nil {
		t.Fatal("polynomial boundary segments must not be extrapolated even when opt-in")
	}
}
