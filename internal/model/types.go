package model

type Unit struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Dimension   string  `json:"dimension"`
	Factor      float64 `json:"factor"`
	Offset      float64 `json:"offset"`
	Fingerprint string  `json:"fingerprint"`
}

type Device struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	InputUnitID  string `json:"input_unit_id"`
	OutputUnitID string `json:"output_unit_id"`
	Fingerprint  string `json:"fingerprint"`
}

type ReferencePoint struct {
	ID           string  `json:"id"`
	DeviceID     string  `json:"device_id"`
	Name         string  `json:"name"`
	InputValue   float64 `json:"input_value"`
	InputUnitID  string  `json:"input_unit_id"`
	OutputValue  float64 `json:"output_value"`
	OutputUnitID string  `json:"output_unit_id"`
	TakenAt      string  `json:"taken_at"`
	Fingerprint  string  `json:"fingerprint"`
}

type LinearPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Segment struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	XMin         float64       `json:"x_min"`
	XMax         float64       `json:"x_max"`
	MinInclusive bool          `json:"min_inclusive"`
	MaxInclusive bool          `json:"max_inclusive"`
	Points       []LinearPoint `json:"points,omitempty"`
	Coefficients []float64     `json:"coefficients,omitempty"`
	Uncertainty  float64       `json:"uncertainty"`
}

type CurveVersion struct {
	ID               string    `json:"id"`
	DeviceID         string    `json:"device_id"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	Revision         int       `json:"revision"`
	InputUnitID      string    `json:"input_unit_id"`
	OutputUnitID     string    `json:"output_unit_id"`
	Segments         []Segment `json:"segments"`
	Extrapolation    bool      `json:"extrapolation"`
	ExtraUncertainty float64   `json:"extra_uncertainty"`
	ValidFrom        string    `json:"valid_from"`
	ValidUntil       string    `json:"valid_until"`
	ConfirmedAt      string    `json:"confirmed_at"`
	ReferenceIDs     []string  `json:"reference_ids"`
	Fingerprint      string    `json:"fingerprint"`
	CreatedAt        string    `json:"created_at"`
	UpdatedAt        string    `json:"updated_at"`
}

type RawReading struct {
	DeviceID    string  `json:"device_id"`
	SampleID    string  `json:"sample_id"`
	RawValue    float64 `json:"raw_value"`
	RawUnitID   string  `json:"raw_unit_id"`
	DeviceTime  string  `json:"device_time"`
	ReceivedAt  string  `json:"received_at"`
	Fingerprint string  `json:"fingerprint"`
}

type UncertaintyComponent struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type Uncertainty struct {
	Combined   float64                `json:"combined"`
	Components []UncertaintyComponent `json:"components"`
}

type SelectionReason struct {
	Rule             string   `json:"rule"`
	CurveID          string   `json:"curve_id"`
	CurveRevision    int      `json:"curve_revision"`
	CurveFingerprint string   `json:"curve_fingerprint"`
	ConfirmedAt      string   `json:"confirmed_at"`
	SampledAt        string   `json:"sampled_at"`
	ReceivedAt       string   `json:"received_at"`
	EligibleCurveIDs []string `json:"eligible_curve_ids"`
}

type CalibratedResult struct {
	DeviceID              string          `json:"device_id"`
	SampleID              string          `json:"sample_id"`
	RawFingerprint        string          `json:"raw_fingerprint"`
	CurveFingerprint      string          `json:"curve_fingerprint"`
	InputValue            float64         `json:"input_value"`
	InputUnitID           string          `json:"input_unit_id"`
	CalibratedValue       float64         `json:"calibrated_value"`
	OutputUnitID          string          `json:"output_unit_id"`
	Uncertainty           Uncertainty     `json:"uncertainty"`
	SegmentID             string          `json:"segment_id"`
	Interval              string          `json:"interval"`
	Extrapolated          bool            `json:"extrapolated"`
	SelectionReason       SelectionReason `json:"selection_reason"`
	ReferenceIDs          []string        `json:"reference_ids"`
	ReferenceFingerprints []string        `json:"reference_fingerprints"`
	Fingerprint           string          `json:"fingerprint"`
	CreatedAt             string          `json:"created_at"`
}

type ReadingRow struct {
	DeviceID   string  `json:"device_id"`
	SampleID   string  `json:"sample_id"`
	RawValue   float64 `json:"raw_value"`
	RawUnitID  string  `json:"raw_unit_id"`
	DeviceTime string  `json:"device_time"`
	ReceivedAt string  `json:"received_at"`
}

type RowStatus struct {
	Index      int               `json:"index"`
	SampleID   string            `json:"sample_id,omitempty"`
	Status     string            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	Conflict   bool              `json:"conflict,omitempty"`
	Idempotent bool              `json:"idempotent,omitempty"`
	Result     *CalibratedResult `json:"result,omitempty"`
}

type BatchResponse struct {
	Processed int         `json:"processed"`
	Accepted  int         `json:"accepted"`
	Failed    int         `json:"failed"`
	Rows      []RowStatus `json:"rows"`
}

type Data struct {
	Units      []Unit             `json:"units"`
	Devices    []Device           `json:"devices"`
	References []ReferencePoint   `json:"references"`
	Curves     []CurveVersion     `json:"curves"`
	Readings   []RawReading       `json:"readings"`
	Results    []CalibratedResult `json:"results"`
}

type Gap struct {
	From          float64 `json:"from"`
	To            float64 `json:"to"`
	FromInclusive bool    `json:"from_inclusive"`
	ToInclusive   bool    `json:"to_inclusive"`
}

type Coverage struct {
	XMin float64 `json:"x_min"`
	XMax float64 `json:"x_max"`
	Gaps []Gap   `json:"gaps"`
}
