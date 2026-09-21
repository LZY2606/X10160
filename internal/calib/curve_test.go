package calib

import (
	"strings"
	"testing"

	"calibration-trace/internal/model"
)

func linearCurve(extra bool) model.CurveVersion {
	return model.CurveVersion{
		DeviceID: "d", InputUnitID: "x", OutputUnitID: "y", ValidFrom: "2026-01-01T00:00:00Z",
		Segments: []model.Segment{{
			ID: "s1", Kind: "linear", XMin: 0, XMax: 50, MinInclusive: true, MaxInclusive: false,
			Points: []model.LinearPoint{{X: 0, Y: 0}, {X: 50, Y: 100}}, Uncertainty: 1,
		}, {
			ID: "s2", Kind: "linear", XMin: 50, XMax: 100, MinInclusive: true, MaxInclusive: true,
			Points: []model.LinearPoint{{X: 50, Y: 100}, {X: 100, Y: 150}}, Uncertainty: 2,
		}},
		Extrapolation: extra, ExtraUncertainty: map[bool]float64{true: 3}[extra],
	}
}

func TestLinearEndpointsOverlapAndGaps(t *testing.T) {
	c := linearCurve(false)
	if err := ValidateCurve(c); err != nil {
		t.Fatal(err)
	}
	v, err := Evaluate(c, 49.999999999)
	if err != nil || v.Segment.ID != "s1" {
		t.Fatalf("left side = %+v, %v", v, err)
	}
	v, err = Evaluate(c, 50)
	if err != nil || v.Segment.ID != "s2" || v.Y != 100 {
		t.Fatalf("endpoint = %+v, %v", v, err)
	}
	c.Segments[0].MaxInclusive = true
	if err := ValidateCurve(c); err == nil || !strings.Contains(err.Error(), "both own endpoint") {
		t.Fatalf("overlap error = %v", err)
	}

	gapped := c
	gapped.Segments[0].MaxInclusive = false
	gapped.Segments[1].XMin, gapped.Segments[1].Points[0].X = 55, 55
	cov := CurveCoverage(gapped)
	if len(cov.Gaps) != 1 || cov.Gaps[0].From != 50 || cov.Gaps[0].To != 55 {
		t.Fatalf("coverage = %+v", cov)
	}
	if _, err := Evaluate(gapped, 52); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("gap error = %v", err)
	}
}

func TestExtrapolationRequiresExplicitOptIn(t *testing.T) {
	c := linearCurve(false)
	if _, err := Evaluate(c, -1); err == nil || !strings.Contains(err.Error(), "extrapolation is disabled") {
		t.Fatalf("default extrapolation error = %v", err)
	}
	c = linearCurve(true)
	v, err := Evaluate(c, -1)
	if err != nil || !v.Extrapolated || v.Y != -2 {
		t.Fatalf("below = %+v, %v", v, err)
	}
	v, err = Evaluate(c, 101)
	if err != nil || !v.Extrapolated || v.Y != 151 {
		t.Fatalf("above = %+v, %v", v, err)
	}
}

func TestPolynomialDegreeAndEvaluation(t *testing.T) {
	c := model.CurveVersion{DeviceID: "d", InputUnitID: "x", OutputUnitID: "y", ValidFrom: "2026-01-01T00:00:00Z", Segments: []model.Segment{{
		ID: "p", Kind: "polynomial", XMin: 0, XMax: 10, MinInclusive: true, MaxInclusive: true,
		Coefficients: []float64{1, 2, 3}, Uncertainty: 0.1,
	}}}
	if err := ValidateCurve(c); err != nil {
		t.Fatal(err)
	}
	v, err := Evaluate(c, 2)
	if err != nil || v.Y != 17 {
		t.Fatalf("poly = %+v %v", v, err)
	}
	c.Segments[0].Coefficients = []float64{1, 2, 3, 4}
	if err := ValidateCurve(c); err == nil || !strings.Contains(err.Error(), "degree") {
		t.Fatalf("degree error = %v", err)
	}
}

func TestUnitDimensionAndAffineConversion(t *testing.T) {
	celsius := model.Unit{ID: "C", Dimension: "temperature", Factor: 1}
	fahrenheit := model.Unit{ID: "F", Dimension: "temperature", Factor: 5.0 / 9.0, Offset: -160.0 / 9.0}
	pressure := model.Unit{ID: "Pa", Dimension: "pressure", Factor: 1}
	v, err := ConvertValue(32, fahrenheit, celsius)
	if err != nil || v != 0 {
		t.Fatalf("32F = %v, %v", v, err)
	}
	if _, err := ConvertValue(1, celsius, pressure); err == nil {
		t.Fatal("expected dimension mismatch")
	}
}

func TestNestedSpansAreOverlaps(t *testing.T) {
	c := linearCurve(false)
	c.Segments = []model.Segment{
		{ID: "outer", Kind: "linear", XMin: 0, XMax: 100, MinInclusive: true, MaxInclusive: true, Points: []model.LinearPoint{{X: 0, Y: 0}, {X: 100, Y: 1}}, Uncertainty: 0},
		{ID: "inner", Kind: "linear", XMin: 10, XMax: 20, MinInclusive: true, MaxInclusive: true, Points: []model.LinearPoint{{X: 10, Y: 0}, {X: 20, Y: 1}}, Uncertainty: 0},
	}
	if err := ValidateCurve(c); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("nested overlap error = %v", err)
	}
}
