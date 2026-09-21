package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
	"calibration-trace/internal/store"
)

func (s *Service) validateCurveData(d *model.Data, c model.CurveVersion) error {
	dev, ok := device(d.Devices, c.DeviceID)
	if !ok {
		return fmt.Errorf("device %q not found", c.DeviceID)
	}
	in, ok := unit(d.Units, c.InputUnitID)
	if !ok {
		return fmt.Errorf("curve input unit %q not found", c.InputUnitID)
	}
	out, ok := unit(d.Units, c.OutputUnitID)
	if !ok {
		return fmt.Errorf("curve output unit %q not found", c.OutputUnitID)
	}
	di, _ := unit(d.Units, dev.InputUnitID)
	do, _ := unit(d.Units, dev.OutputUnitID)
	if in.Dimension != di.Dimension {
		return fmt.Errorf("curve input dimension must match device input dimension")
	}
	if out.Dimension != do.Dimension {
		return fmt.Errorf("curve output dimension must match device output dimension")
	}
	seenRef := map[string]bool{}
	for _, id := range c.ReferenceIDs {
		r, ok := referenceAt(d.References, id)
		if !ok {
			return fmt.Errorf("reference %q not found", id)
		}
		if r.DeviceID != c.DeviceID {
			return fmt.Errorf("reference %q belongs to another device", id)
		}
		if seenRef[id] {
			return fmt.Errorf("duplicate reference %q", id)
		}
		seenRef[id] = true
	}
	return calib.ValidateCurve(c)
}

func curveFingerprint(c model.CurveVersion) (string, error) {
	refs := append([]string(nil), c.ReferenceIDs...)
	sort.Strings(refs)
	segments := make([]map[string]any, 0, len(c.Segments))
	for _, seg := range c.Segments {
		item := map[string]any{"id": seg.ID, "kind": seg.Kind, "x_min": seg.XMin, "x_max": seg.XMax, "min_inclusive": seg.MinInclusive, "max_inclusive": seg.MaxInclusive, "uncertainty": seg.Uncertainty}
		if seg.Kind == "linear" {
			points := make([][]float64, 0, len(seg.Points))
			for _, p := range seg.Points {
				points = append(points, []float64{p.X, p.Y})
			}
			item["points"] = points
		} else {
			item["coefficients"] = append([]float64(nil), seg.Coefficients...)
		}
		segments = append(segments, item)
	}
	return calib.Fingerprint("curve-version", map[string]any{
		"device_id": c.DeviceID, "name": c.Name, "input_unit_id": c.InputUnitID, "output_unit_id": c.OutputUnitID,
		"valid_from": c.ValidFrom, "valid_until": c.ValidUntil, "confirmed_at": c.ConfirmedAt,
		"extrapolation": c.Extrapolation, "extra_uncertainty": c.ExtraUncertainty,
		"segments": segments, "reference_ids": refs,
	})
}

func (s *Service) CreateCurve(in model.CurveVersion) (model.CurveVersion, error) {
	if strings.TrimSpace(in.DeviceID) == "" {
		return model.CurveVersion{}, fmt.Errorf("device id is required")
	}
	in.Status = "draft"
	in.Revision = 0
	in.ConfirmedAt = ""
	now := calib.FormatTime(s.now())
	in.CreatedAt, in.UpdatedAt = now, now
	out := model.CurveVersion{}
	_, err := s.store.Mutate(func(d *model.Data) error {
		exists := func(id string) bool { return curveAt(d.Curves, id) != nil }
		if in.ID == "" {
			in.ID = newID("curve", exists)
		}
		if exists(in.ID) {
			return fmt.Errorf("%w: curve %q", store.ErrConflict, in.ID)
		}
		if err := s.validateCurveData(d, in); err != nil {
			return err
		}
		normalizeCurve(&in)
		fp, err := curveFingerprint(in)
		if err != nil {
			return err
		}
		in.Fingerprint = fp
		d.Curves = append(d.Curves, in)
		out = in
		return nil
	})
	return out, err
}

func (s *Service) UpdateCurve(id string, expectedRevision int, in model.CurveVersion) (model.CurveVersion, error) {
	in.ID, in.Status = id, ""
	out := model.CurveVersion{}
	_, err := s.store.Mutate(func(d *model.Data) error {
		c := curveAt(d.Curves, id)
		if c == nil {
			return fmt.Errorf("%w: curve %q", store.ErrNotFound, id)
		}
		if c.Revision != expectedRevision {
			return fmt.Errorf("%w: expected revision %d, found %d", store.ErrConflict, expectedRevision, c.Revision)
		}
		if c.Status == "published" {
			return fmt.Errorf("%w: published curve %q is immutable", store.ErrConflict, id)
		}
		c.Name = in.Name
		c.InputUnitID = in.InputUnitID
		c.OutputUnitID = in.OutputUnitID
		c.Segments = in.Segments
		c.Extrapolation = in.Extrapolation
		c.ExtraUncertainty = in.ExtraUncertainty
		c.ValidFrom = in.ValidFrom
		c.ValidUntil = in.ValidUntil
		c.ReferenceIDs = in.ReferenceIDs
		if err := s.validateCurveData(d, *c); err != nil {
			return err
		}
		normalizeCurve(c)
		c.Revision++
		c.UpdatedAt = calib.FormatTime(s.now())
		fp, err := curveFingerprint(*c)
		if err != nil {
			return err
		}
		c.Fingerprint = fp
		out = *c
		return nil
	})
	return out, err
}

type PublishRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	ConfirmedAt      string `json:"confirmed_at"`
}

func (s *Service) PublishCurve(id string, req PublishRequest) (model.CurveVersion, error) {
	out := model.CurveVersion{}
	_, err := s.store.Mutate(func(d *model.Data) error {
		c := curveAt(d.Curves, id)
		if c == nil {
			return fmt.Errorf("%w: curve %q", store.ErrNotFound, id)
		}
		if c.Revision != req.ExpectedRevision {
			return fmt.Errorf("%w: expected revision %d, found %d", store.ErrConflict, req.ExpectedRevision, c.Revision)
		}
		if c.Status == "published" {
			return fmt.Errorf("%w: curve is already published", store.ErrConflict)
		}
		confirmed := s.now()
		if req.ConfirmedAt != "" {
			t, err := calib.ParseTime(req.ConfirmedAt, "confirmed_at")
			if err != nil {
				return err
			}
			confirmed = t
		}
		from, _ := calib.ParseTime(c.ValidFrom, "valid_from")
		if confirmed.Before(from) {
			return fmt.Errorf("confirmed_at cannot be before valid_from")
		}
		c.Status = "published"
		c.Revision++
		c.ConfirmedAt = calib.FormatTime(confirmed)
		c.UpdatedAt = calib.FormatTime(s.now())
		fp, err := curveFingerprint(*c)
		if err != nil {
			return err
		}
		c.Fingerprint = fp
		out = *c
		return nil
	})
	return out, err
}

func (s *Service) CloneCurve(id string, requestedID string) (model.CurveVersion, error) {
	out := model.CurveVersion{}
	_, err := s.store.Mutate(func(d *model.Data) error {
		src := curveAt(d.Curves, id)
		if src == nil {
			return fmt.Errorf("%w: curve %q", store.ErrNotFound, id)
		}
		exists := func(candidate string) bool { return curveAt(d.Curves, candidate) != nil }
		if requestedID == "" {
			requestedID = newID("curve", exists)
		}
		if exists(requestedID) {
			return fmt.Errorf("%w: curve %q", store.ErrConflict, requestedID)
		}
		c := *src
		c.ID = requestedID
		normalizeCurve(&c)
		c.Status = "draft"
		c.Revision = 0
		c.ConfirmedAt = ""
		now := calib.FormatTime(s.now())
		c.CreatedAt, c.UpdatedAt = now, now
		fp, err := curveFingerprint(c)
		if err != nil {
			return err
		}
		c.Fingerprint = fp
		d.Curves = append(d.Curves, c)
		out = c
		return nil
	})
	return out, err
}

func validityContains(c model.CurveVersion, sampled time.Time) bool {
	from, err := calib.ParseTime(c.ValidFrom, "valid_from")
	if err != nil {
		return false
	}
	if sampled.Before(from) {
		return false
	}
	if c.ValidUntil == "" {
		return true
	}
	until, err := calib.ParseTime(c.ValidUntil, "valid_until")
	if err != nil {
		return false
	}
	return sampled.Before(until)
}

func selectCurve(curves []model.CurveVersion, deviceID string, sampled, received time.Time) (*model.CurveVersion, []string) {
	eligible := make([]model.CurveVersion, 0)
	for i := range curves {
		c := curves[i]
		if c.DeviceID == deviceID && c.Status == "published" && validityContains(c, sampled) {
			confirmed, err := calib.ParseTime(c.ConfirmedAt, "confirmed_at")
			if err == nil && !confirmed.After(received) {
				eligible = append(eligible, c)
			}
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, eligible[i].ConfirmedAt)
		tj, _ := time.Parse(time.RFC3339, eligible[j].ConfirmedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		if eligible[i].ValidFrom != eligible[j].ValidFrom {
			return eligible[i].ValidFrom > eligible[j].ValidFrom
		}
		return eligible[i].ID < eligible[j].ID
	})
	ids := make([]string, 0, len(eligible))
	for _, c := range eligible {
		ids = append(ids, c.ID)
	}
	if len(eligible) == 0 {
		return nil, ids
	}
	for i := range curves {
		if curves[i].ID == eligible[0].ID {
			return &curves[i], ids
		}
	}
	return nil, ids
}
