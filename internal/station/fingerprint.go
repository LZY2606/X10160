package station

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"
)

// canonFloat rounds to 12 significant digits so fingerprints only depend on
// values that can survive JSON round trips.
func canonFloat(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	digits := 12.0
	scale := math.Pow(10, digits-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*scale) / scale
}

func canonTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type canonKnot struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type canonSegment struct {
	Type         string      `json:"type"`
	Lower        float64     `json:"lower"`
	LowerClosed  bool        `json:"lower_closed"`
	Upper        float64     `json:"upper"`
	UpperClosed  bool        `json:"upper_closed"`
	Knots        []canonKnot `json:"knots"`
	Coeffs       []float64   `json:"coeffs"`
	Center       float64     `json:"center"`
	Uncertainty  float64     `json:"uncertainty"`
	OverlapGroup string      `json:"overlap_group"`
	Priority     int         `json:"priority"`
	Refs         []string    `json:"refs"`
	Label        string      `json:"label"`
}

type canonVersion struct {
	Kind                  string         `json:"kind"`
	ID                    string         `json:"id"`
	DeviceID              string         `json:"device_id"`
	State                 string         `json:"state"`
	Note                  string         `json:"note"`
	Segments              []canonSegment `json:"segments"`
	ValidFrom             string         `json:"valid_from"`
	ValidTo               string         `json:"valid_to"`
	AllowExtrapolate      bool           `json:"allow_extrapolate"`
	ExtrapUncertaintyRate float64        `json:"extrap_uncertainty_rate"`
	PublishedAt           string         `json:"published_at"`
}

func segmentCanon(s Segment) canonSegment {
	ks := make([]canonKnot, 0, len(s.Knots))
	for _, k := range s.Knots {
		ks = append(ks, canonKnot{X: canonFloat(k.X), Y: canonFloat(k.Y)})
	}
	cs := make([]float64, 0, len(s.Coeffs))
	for _, c := range s.Coeffs {
		cs = append(cs, canonFloat(c))
	}
	refs := append([]string(nil), s.Refs...)
	sort.Strings(refs)
	return canonSegment{
		Type: s.Type, Lower: canonFloat(s.Lower), LowerClosed: s.LowerClosed,
		Upper: canonFloat(s.Upper), UpperClosed: s.UpperClosed,
		Knots: ks, Coeffs: cs, Center: canonFloat(s.Center),
		Uncertainty: canonFloat(s.Uncertainty), OverlapGroup: s.OverlapGroup,
		Priority: s.Priority, Refs: refs, Label: s.Label,
	}
}

func versionCanon(v *CurveVersion) canonVersion {
	segs := make([]canonSegment, 0, len(v.Segments))
	for _, s := range v.Segments {
		segs = append(segs, segmentCanon(s))
	}
	return canonVersion{
		Kind: "curve_version", ID: v.ID, DeviceID: v.DeviceID, State: v.State, Note: v.Note,
		Segments: segs, ValidFrom: canonTime(v.ValidFrom), ValidTo: canonTime(v.ValidTo),
		AllowExtrapolate:      v.AllowExtrapolate,
		ExtrapUncertaintyRate: canonFloat(v.ExtrapUncertaintyRate),
		PublishedAt:           canonTime(v.PublishedAt),
	}
}

type canonUncertainty struct {
	Reference  float64 `json:"reference"`
	Interp     float64 `json:"interpolation"`
	Extrap     float64 `json:"extrapolation"`
	Total      float64 `json:"total"`
	ExtrapRate float64 `json:"extrap_rate"`
}

type canonSelection struct {
	VersionID       string   `json:"version_id"`
	Status          string   `json:"status"`
	Why             string   `json:"why"`
	Candidates      []string `json:"candidates"`
	WindowValidFrom string   `json:"window_valid_from"`
	WindowValidTo   string   `json:"window_valid_to"`
	SegmentLabel    string   `json:"segment_label"`
	SegmentType     string   `json:"segment_type"`
	DeclaredOverlap bool     `json:"declared_overlap"`
	ReceivedAt      string   `json:"received_at"`
	SampledAt       string   `json:"sampled_at"`
}

type canonResult struct {
	Kind               string           `json:"kind"`
	ReadingID          string           `json:"reading_id"`
	DeviceID           string           `json:"device_id"`
	VersionID          string           `json:"version_id"`
	VersionFingerprint string           `json:"version_fingerprint"`
	RawValue           float64          `json:"raw_value"`
	RawUnit            string           `json:"raw_unit"`
	CalValue           float64          `json:"cal_value"`
	CalUnit            string           `json:"cal_unit"`
	Uncertainty        canonUncertainty `json:"uncertainty"`
	SegmentLabel       string           `json:"segment_label"`
	SegmentType        string           `json:"segment_type"`
	ReferenceIDs       []string         `json:"reference_ids"`
	Extrapolated       bool             `json:"extrapolated"`
	Status             string           `json:"status"`
	Selection          canonSelection   `json:"selection"`
	CreatedAt          string           `json:"created_at"`
}

func resultCanon(r *CalibrationResult) canonResult {
	refs := append([]string(nil), r.ReferenceIDs...)
	sort.Strings(refs)
	cands := append([]string(nil), r.Selection.Candidates...)
	sort.Strings(cands)
	return canonResult{
		Kind: "calibration_result", ReadingID: r.ReadingID, DeviceID: r.DeviceID,
		VersionID: r.VersionID, VersionFingerprint: r.VersionFingerprint,
		RawValue: canonFloat(r.RawValue), RawUnit: r.RawUnit,
		CalValue: canonFloat(r.CalValue), CalUnit: r.CalUnit,
		Uncertainty: canonUncertainty{
			Reference:  canonFloat(r.Uncertainty.Reference),
			Interp:     canonFloat(r.Uncertainty.Interp),
			Extrap:     canonFloat(r.Uncertainty.Extrap),
			Total:      canonFloat(r.Uncertainty.Total),
			ExtrapRate: canonFloat(r.Uncertainty.ExtrapRate),
		},
		SegmentLabel: r.SegmentLabel, SegmentType: r.SegmentType,
		ReferenceIDs: refs, Extrapolated: r.Extrapolated, Status: r.Status,
		Selection: canonSelection{
			VersionID: r.Selection.VersionID, Status: r.Selection.Status,
			Why: r.Selection.Why, Candidates: cands,
			WindowValidFrom: canonTime(r.Selection.WindowValidFrom),
			WindowValidTo:   canonTime(r.Selection.WindowValidTo),
			SegmentLabel:    r.Selection.SegmentLabel, SegmentType: r.Selection.SegmentType,
			DeclaredOverlap: r.Selection.DeclaredOverlap,
			ReceivedAt:      canonTime(r.Selection.ReceivedAt), SampledAt: canonTime(r.Selection.SampledAt),
		},
		CreatedAt: canonTime(r.CreatedAt),
	}
}

func hashCanon(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fingerprintVersion(v *CurveVersion) string     { return hashCanon(versionCanon(v)) }
func fingerprintResult(r *CalibrationResult) string { return hashCanon(resultCanon(r)) }

type canonReading struct {
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	DeviceID   string  `json:"device_id"`
	RawValue   float64 `json:"raw_value"`
	RawUnit    string  `json:"raw_unit"`
	SampledAt  string  `json:"sampled_at"`
	ReceivedAt string  `json:"received_at"`
	Source     string  `json:"source"`
	CreatedAt  string  `json:"created_at"`
}

func fingerprintReading(r *Reading) string {
	return hashCanon(canonReading{
		Kind: "reading", ID: r.ID, DeviceID: r.DeviceID, RawValue: canonFloat(r.RawValue),
		RawUnit: r.RawUnit, SampledAt: canonTime(r.SampledAt),
		ReceivedAt: canonTime(r.ReceivedAt), Source: r.Source, CreatedAt: canonTime(r.CreatedAt),
	})
}

type canonDevice struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	RawUnit        string `json:"raw_unit"`
	CalibratedUnit string `json:"calibrated_unit"`
	Description    string `json:"description"`
	Revision       int64  `json:"revision"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func fingerprintDevice(d *Device) string {
	return hashCanon(canonDevice{
		Kind: "device", ID: d.ID, Name: d.Name, RawUnit: d.RawUnit,
		CalibratedUnit: d.CalibratedUnit, Description: d.Description, Revision: d.Revision,
		CreatedAt: canonTime(d.CreatedAt), UpdatedAt: canonTime(d.UpdatedAt),
	})
}

type canonRef struct {
	Kind        string  `json:"kind"`
	ID          string  `json:"id"`
	DeviceID    string  `json:"device_id"`
	Name        string  `json:"name"`
	Dimension   string  `json:"dimension"`
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	Uncertainty float64 `json:"uncertainty"`
	Source      string  `json:"source"`
	CreatedAt   string  `json:"created_at"`
}

func fingerprintRef(r *ReferencePoint) string {
	return hashCanon(canonRef{
		Kind: "reference_point", ID: r.ID, DeviceID: r.DeviceID, Name: r.Name,
		Dimension: r.Dimension, Value: canonFloat(r.Value), Unit: r.Unit,
		Uncertainty: canonFloat(r.Uncertainty), Source: r.Source, CreatedAt: canonTime(r.CreatedAt),
	})
}
