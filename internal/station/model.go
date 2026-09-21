package station

import "time"

// Unit describes a display/raw unit belonging to exactly one dimension.
// valueInBase = value*factor + offset converts into the dimension base unit.
// Temperature units (offset != 0) are affine; conversion between two affine
// units is therefore only well defined for point values, never for deltas.
type Unit struct {
	Name      string  `json:"name"`
	Dimension string  `json:"dimension"`
	Symbol    string  `json:"symbol"`
	Factor    float64 `json:"factor"`
	Offset    float64 `json:"offset"`
	Builtin   bool    `json:"builtin"`
}

func (u Unit) toBase(v float64) float64 { return v*u.Factor + u.Offset }

func (u Unit) fromBase(v float64) float64 { return (v - u.Offset) / u.Factor }

// Device carries its fixed fact units. Curves are defined in RawUnit domain
// values and produce CalibratedUnit values. Display conversions never rewrite
// either stored fact.
type Device struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	RawUnit        string    `json:"raw_unit"`
	CalibratedUnit string    `json:"calibrated_unit"`
	Description    string    `json:"description"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ReferencePoint is an independently known calibration reference cited by
// curve segments so every result can trace back to physical references.
type ReferencePoint struct {
	ID          string    `json:"id"`
	DeviceID    string    `json:"device_id,omitempty"`
	Name        string    `json:"name"`
	Dimension   string    `json:"dimension"`
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Uncertainty float64   `json:"uncertainty"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

type Knot struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

const (
	SegLinear     = "linear"
	SegPolynomial = "polynomial"
)

// Segment is either a piecewise-linear pair of knots or a low-order
// polynomial. Interval endpoints are declared explicitly so endpoint
// semantics never have to be guessed: each endpoint is open (exclusive) or
// closed (inclusive).
type Segment struct {
	Type        string  `json:"type"`
	Lower       float64 `json:"lower"`
	LowerClosed bool    `json:"lower_closed"`
	Upper       float64 `json:"upper"`
	UpperClosed bool    `json:"upper_closed"`
	Knots       []Knot  `json:"knots,omitempty"`
	// Coefficients are ascending powers of (x-Center): y=sum Coeffs[i]*(x-Center)^i
	Coeffs []float64 `json:"coeffs,omitempty"`
	Center float64   `json:"center,omitempty"`
	// Calibration standard uncertainty (in calibrated units) at this segment.
	Uncertainty float64 `json:"uncertainty"`
	// Declared overlap membership. Empty segment sets must be disjoint except
	// for touching endpoints. Non-empty groups may overlap on purpose.
	OverlapGroup string `json:"overlap_group,omitempty"`
	// Priority disambiguates points covered by several segments of the same
	// declared overlap group. Smaller number wins; index breaks ties.
	Priority int      `json:"priority,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	Label    string   `json:"label,omitempty"`
}

func (s Segment) lowerClosedB() bool { return s.LowerClosed }
func (s Segment) upperClosedB() bool { return s.UpperClosed }

func (s Segment) contains(x float64) bool {
	if x < s.Lower || x > s.Upper {
		return false
	}
	if x == s.Lower && !s.LowerClosed {
		return false
	}
	if x == s.Upper && !s.UpperClosed {
		return false
	}
	return true
}

const (
	StatusCalibrated     = "calibrated"
	StatusOutOfRange     = "out_of_range"
	StatusNoVersion      = "no_version"
	StatusAmbiguous      = "ambiguous_versions"
	StatusDeviceTimeBack = "sample_outside_received_version"
)

// UncertaintyDetail records how total uncertainty was assembled. All terms
// are standard uncertainties in the calibrated unit.
type UncertaintyDetail struct {
	Reference  float64 `json:"reference"`
	Interp     float64 `json:"interpolation"`
	Extrap     float64 `json:"extrapolation"`
	Total      float64 `json:"total"`
	ExtrapRate float64 `json:"extrap_rate,omitempty"`
}

// SelectionReason is the frozen explanation of how the curve version and
// segment were chosen for one sample.
type SelectionReason struct {
	VersionID       string    `json:"version_id"`
	Status          string    `json:"status"`
	Why             string    `json:"why"`
	Candidates      []string  `json:"candidates,omitempty"`
	WindowValidFrom time.Time `json:"window_valid_from"`
	WindowValidTo   time.Time `json:"window_valid_to"`
	SegmentLabel    string    `json:"segment_label,omitempty"`
	SegmentType     string    `json:"segment_type,omitempty"`
	DeclaredOverlap bool      `json:"declared_overlap,omitempty"`
	ReceivedAt      time.Time `json:"received_at"`
	SampledAt       time.Time `json:"sampled_at"`
}

type CalibrationResult struct {
	ReadingID          string                    `json:"reading_id"`
	DeviceID           string                    `json:"device_id"`
	VersionID          string                    `json:"version_id"`
	VersionFingerprint string                    `json:"version_fingerprint"`
	RawValue           float64                   `json:"raw_value"`
	RawUnit            string                    `json:"raw_unit"`
	CalValue           float64                   `json:"cal_value,omitempty"`
	CalUnit            string                    `json:"cal_unit"`
	Uncertainty        UncertaintyDetail         `json:"uncertainty"`
	SegmentLabel       string                    `json:"segment_label,omitempty"`
	SegmentType        string                    `json:"segment_type,omitempty"`
	ReferenceIDs       []string                  `json:"reference_ids"`
	Extrapolated       bool                      `json:"extrapolated"`
	Status             string                    `json:"status"`
	Selection          SelectionReason           `json:"selection"`
	RefTrace           map[string]ReferencePoint `json:"-"`
	Fingerprint        string                    `json:"fingerprint"`
	CreatedAt          time.Time                 `json:"created_at"`
}

func (r *CalibrationResult) RefTraceSet(rp ReferencePoint) {
	if r.RefTrace == nil {
		r.RefTrace = map[string]ReferencePoint{}
	}
	r.RefTrace[rp.ID] = rp
}

type Reading struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"device_id"`
	RawValue   float64   `json:"raw_value"`
	RawUnit    string    `json:"raw_unit"`
	SampledAt  time.Time `json:"sampled_at"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
