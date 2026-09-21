package service

import (
	"fmt"
	"sort"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
	"calibration-trace/internal/store"
)

func rawFingerprint(r model.ReadingRow) (string, error) {
	return calib.Fingerprint("raw-reading", map[string]any{"device_id": r.DeviceID, "sample_id": r.SampleID, "raw_value": r.RawValue, "raw_unit_id": r.RawUnitID, "device_time": r.DeviceTime, "received_at": r.ReceivedAt})
}

func resultFingerprint(r model.CalibratedResult) (string, error) {
	orderedComponents := append([]model.UncertaintyComponent(nil), r.Uncertainty.Components...)
	sort.SliceStable(orderedComponents, func(i, j int) bool { return orderedComponents[i].Name < orderedComponents[j].Name })
	components := make([]map[string]float64, 0, len(orderedComponents))
	for _, c := range orderedComponents {
		components = append(components, map[string]float64{c.Name: c.Value})
	}
	refs := append([]string(nil), r.ReferenceIDs...)
	sort.Strings(refs)
	refFPS := append([]string(nil), r.ReferenceFingerprints...)
	sort.Strings(refFPS)
	eligible := append([]string(nil), r.SelectionReason.EligibleCurveIDs...)
	sort.Strings(eligible)
	return calib.Fingerprint("calibrated-result", map[string]any{
		"device_id": r.DeviceID, "sample_id": r.SampleID, "raw_fingerprint": r.RawFingerprint,
		"curve_fingerprint": r.CurveFingerprint, "input_value": r.InputValue, "input_unit_id": r.InputUnitID,
		"calibrated_value": r.CalibratedValue, "output_unit_id": r.OutputUnitID,
		"uncertainty": map[string]any{"combined": r.Uncertainty.Combined, "components": components},
		"segment_id":  r.SegmentID, "interval": r.Interval, "extrapolated": r.Extrapolated,
		"selection": map[string]any{"rule": r.SelectionReason.Rule, "curve_id": r.SelectionReason.CurveID,
			"confirmed_at": r.SelectionReason.ConfirmedAt, "sampled_at": r.SelectionReason.SampledAt,
			"received_at": r.SelectionReason.ReceivedAt, "eligible_curve_ids": eligible},
		"reference_ids": refs, "reference_fingerprints": refFPS,
	})
}

func evaluateReading(d *model.Data, row model.ReadingRow, now string) (*model.CalibratedResult, error) {
	dev, ok := device(d.Devices, row.DeviceID)
	if !ok {
		return nil, fmt.Errorf("device not found")
	}
	sampled, err := calib.ParseTime(row.DeviceTime, "device_time")
	if err != nil {
		return nil, err
	}
	received, err := calib.ParseTime(row.ReceivedAt, "received_at")
	if err != nil {
		return nil, err
	}
	ru, ok := unit(d.Units, row.RawUnitID)
	if !ok {
		return nil, fmt.Errorf("raw unit not found")
	}
	di, _ := unit(d.Units, dev.InputUnitID)
	if ru.Dimension != di.Dimension {
		return nil, fmt.Errorf("raw unit dimension %s does not match device input dimension %s", ru.Dimension, di.Dimension)
	}
	x, err := calib.ConvertValue(row.RawValue, ru, di)
	if err != nil {
		return nil, err
	}
	c, eligible := selectCurve(d.Curves, row.DeviceID, sampled, received)
	if c == nil {
		return nil, fmt.Errorf("no curve version confirmed by %s covering sample time %s", row.ReceivedAt, row.DeviceTime)
	}
	ev, err := calib.Evaluate(*c, x)
	if err != nil {
		return nil, err
	}
	combined := ev.Segment.Uncertainty
	components := []model.UncertaintyComponent{{Name: "segment", Value: ev.Segment.Uncertainty}}
	if ev.Extrapolated {
		combined = sqrtSumSquares(ev.Segment.Uncertainty, c.ExtraUncertainty)
		components = append(components, model.UncertaintyComponent{Name: "explicit_extrapolation", Value: c.ExtraUncertainty})
	}
	ids := append([]string(nil), c.ReferenceIDs...)
	sort.Strings(ids)
	fps := make([]string, 0, len(ids))
	for _, id := range ids {
		r, _ := referenceAt(d.References, id)
		fps = append(fps, r.Fingerprint)
	}
	sort.SliceStable(components, func(i, j int) bool { return components[i].Name < components[j].Name })
	res := &model.CalibratedResult{
		DeviceID: row.DeviceID, SampleID: row.SampleID, InputValue: x, InputUnitID: dev.InputUnitID,
		CalibratedValue: ev.Y, OutputUnitID: dev.OutputUnitID,
		Uncertainty: model.Uncertainty{Combined: combined, Components: components},
		SegmentID:   ev.Segment.ID, Interval: ev.Interval, Extrapolated: ev.Extrapolated,
		SelectionReason: model.SelectionReason{Rule: "latest confirmed-at on or before received-at whose validity contains device sample time",
			CurveID: c.ID, CurveRevision: c.Revision, CurveFingerprint: c.Fingerprint, ConfirmedAt: c.ConfirmedAt,
			SampledAt: calib.FormatTime(sampled), ReceivedAt: calib.FormatTime(received), EligibleCurveIDs: eligible},
		ReferenceIDs: ids, ReferenceFingerprints: fps, CreatedAt: now,
	}
	fp, err := resultFingerprint(*res)
	if err != nil {
		return nil, err
	}
	res.Fingerprint = fp
	return res, nil
}

func (s *Service) ImportReadings(rows []model.ReadingRow) (model.BatchResponse, error) {
	type planned struct {
		result *model.CalibratedResult
		raw    model.RawReading
	}
	resp := model.BatchResponse{Rows: make([]model.RowStatus, 0, len(rows))}
	var additions []planned
	_, mutateErr := s.store.Mutate(func(d *model.Data) error {
		batchSeen := map[string]bool{}
		for i, row := range rows {
			status := model.RowStatus{Index: i, SampleID: row.SampleID, Status: "failed"}
			resp.Processed++
			if row.DeviceID == "" || row.SampleID == "" {
				status.Reason = "device_id and sample_id are required"
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			batchKey := row.DeviceID + "\u0000" + row.SampleID
			if batchSeen[batchKey] {
				status.Reason = "same device_id/sample_id appears more than once in one batch"
				status.Conflict = true
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			if existing := readingAt(d.Readings, row.DeviceID, row.SampleID); existing != nil {
				if existing.RawValue == row.RawValue && existing.RawUnitID == row.RawUnitID && existing.DeviceTime == row.DeviceTime && existing.ReceivedAt == row.ReceivedAt {
					status.Status = "idempotent"
					status.Idempotent = true
					batchSeen[batchKey] = true
					if frozen := resultAt(d.Results, row.DeviceID, row.SampleID); frozen != nil {
						r := *frozen
						status.Result = &r
					}
					resp.Rows = append(resp.Rows, status)
					continue
				}
				status.Reason = "same device_id/sample_id already exists with a different immutable raw fact"
				status.Conflict = true
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			rfp, err := rawFingerprint(row)
			if err != nil {
				status.Reason = err.Error()
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			res, err := evaluateReading(d, row, calib.FormatTime(s.now()))
			if err != nil {
				status.Reason = err.Error()
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			raw := model.RawReading{DeviceID: row.DeviceID, SampleID: row.SampleID, RawValue: row.RawValue, RawUnitID: row.RawUnitID, DeviceTime: row.DeviceTime, ReceivedAt: row.ReceivedAt, Fingerprint: rfp}
			res.RawFingerprint = rfp
			res.CurveFingerprint = res.SelectionReason.CurveFingerprint
			fp, err := resultFingerprint(*res)
			if err != nil {
				status.Reason = err.Error()
				resp.Failed++
				resp.Rows = append(resp.Rows, status)
				continue
			}
			res.Fingerprint = fp
			additions = append(additions, planned{result: res, raw: raw})
			batchSeen[batchKey] = true
			status.Status = "accepted"
			status.Result = res
			resp.Accepted++
			resp.Rows = append(resp.Rows, status)
		}
		for _, p := range additions {
			d.Readings = append(d.Readings, p.raw)
			d.Results = append(d.Results, *p.result)
		}
		return nil
	})
	sort.SliceStable(resp.Rows, func(i, j int) bool { return resp.Rows[i].Index < resp.Rows[j].Index })
	return resp, mutateErr
}

func (s *Service) Trace(deviceID, sampleID string) (model.RawReading, *model.CalibratedResult, []model.ReferencePoint, model.CurveVersion, error) {
	d := s.Snapshot()
	r := readingAt(d.Readings, deviceID, sampleID)
	if r == nil {
		return model.RawReading{}, nil, nil, model.CurveVersion{}, fmt.Errorf("%w: reading", store.ErrNotFound)
	}
	res := resultAt(d.Results, deviceID, sampleID)
	var refs []model.ReferencePoint
	if res != nil {
		for _, id := range res.ReferenceIDs {
			if rp, ok := referenceAt(d.References, id); ok {
				refs = append(refs, rp)
			}
		}
	}
	var c model.CurveVersion
	if res != nil {
		if pc := curveAt(d.Curves, res.SelectionReason.CurveID); pc != nil {
			c = *pc
		}
	}
	return *r, res, refs, c, nil
}
