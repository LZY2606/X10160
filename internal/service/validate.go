package service

import (
	"fmt"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
)

func validateData(d model.Data) error {
	unitByID := map[string]model.Unit{}
	for _, u := range d.Units {
		if err := calib.ValidateUnit(u); err != nil {
			return err
		}
		if _, ok := unitByID[u.ID]; ok {
			return fmt.Errorf("duplicate unit %s", u.ID)
		}
		want, err := calib.Fingerprint("unit", map[string]any{"id": u.ID, "name": u.Name, "dimension": u.Dimension, "factor": u.Factor, "offset": u.Offset})
		if err != nil {
			return err
		}
		if u.Fingerprint != want {
			return fmt.Errorf("unit %s fingerprint mismatch", u.ID)
		}
		unitByID[u.ID] = u
	}
	devByID := map[string]model.Device{}
	for _, dev := range d.Devices {
		if dev.ID == "" {
			return fmt.Errorf("device id is required")
		}
		if _, ok := devByID[dev.ID]; ok {
			return fmt.Errorf("duplicate device %s", dev.ID)
		}
		in, ok := unitByID[dev.InputUnitID]
		if !ok {
			return fmt.Errorf("device unit missing")
		}
		out, ok2 := unitByID[dev.OutputUnitID]
		if !ok2 {
			return fmt.Errorf("device output unit missing")
		}
		if in.Dimension != out.Dimension {
			return fmt.Errorf("device dimension mismatch")
		}
		want, err := calib.Fingerprint("device", map[string]any{"id": dev.ID, "name": dev.Name, "input_unit_id": dev.InputUnitID, "output_unit_id": dev.OutputUnitID})
		if err != nil {
			return err
		}
		if dev.Fingerprint != want {
			return fmt.Errorf("device %s fingerprint mismatch", dev.ID)
		}
		devByID[dev.ID] = dev
	}
	refByID := map[string]model.ReferencePoint{}
	for _, r := range d.References {
		dev, ok := devByID[r.DeviceID]
		if !ok {
			return fmt.Errorf("reference device missing")
		}
		iu, iok := unitByID[r.InputUnitID]
		ou, ook := unitByID[r.OutputUnitID]
		if !iok || !ook || iu.Dimension != unitByID[dev.InputUnitID].Dimension || ou.Dimension != unitByID[dev.OutputUnitID].Dimension {
			return fmt.Errorf("reference dimension mismatch")
		}
		if _, err := calib.ParseTime(r.TakenAt, "taken_at"); err != nil {
			return err
		}
		if _, ok := refByID[r.ID]; ok {
			return fmt.Errorf("duplicate reference %s", r.ID)
		}
		want, err := calib.Fingerprint("reference", map[string]any{"id": r.ID, "device_id": r.DeviceID, "name": r.Name, "input_value": r.InputValue, "input_unit_id": r.InputUnitID, "output_value": r.OutputValue, "output_unit_id": r.OutputUnitID, "taken_at": r.TakenAt})
		if err != nil {
			return err
		}
		if r.Fingerprint != want {
			return fmt.Errorf("reference %s fingerprint mismatch", r.ID)
		}
		refByID[r.ID] = r
	}
	curveByID := map[string]model.CurveVersion{}
	for _, c := range d.Curves {
		svc := &Service{}
		if err := svc.validateCurveData(&d, c); err != nil {
			return err
		}
		if _, ok := curveByID[c.ID]; ok {
			return fmt.Errorf("duplicate curve %s", c.ID)
		}
		want, err := curveFingerprint(c)
		if err != nil {
			return err
		}
		if c.Fingerprint != want {
			return fmt.Errorf("curve %s fingerprint mismatch", c.ID)
		}
		curveByID[c.ID] = c
	}
	readingKey := map[string]bool{}
	for _, r := range d.Readings {
		key := r.DeviceID + "\x00" + r.SampleID
		if readingKey[key] {
			return fmt.Errorf("duplicate reading %q", key)
		}
		readingKey[key] = true
		if _, ok := devByID[r.DeviceID]; !ok {
			return fmt.Errorf("reading device missing")
		}
		row := model.ReadingRow{DeviceID: r.DeviceID, SampleID: r.SampleID, RawValue: r.RawValue, RawUnitID: r.RawUnitID, DeviceTime: r.DeviceTime, ReceivedAt: r.ReceivedAt}
		want, err := rawFingerprint(row)
		if err != nil {
			return err
		}
		if r.Fingerprint != want {
			return fmt.Errorf("reading %s/%s raw fingerprint mismatch", r.DeviceID, r.SampleID)
		}
	}
	resultKey := map[string]bool{}
	for _, r := range d.Results {
		key := r.DeviceID + "\x00" + r.SampleID
		if resultKey[key] {
			return fmt.Errorf("duplicate result %q", key)
		}
		resultKey[key] = true
		if !readingKey[key] {
			return fmt.Errorf("result has no raw reading")
		}
		if _, ok := curveByID[r.SelectionReason.CurveID]; !ok {
			return fmt.Errorf("result curve missing")
		}
		want, err := resultFingerprint(r)
		if err != nil {
			return err
		}
		if r.Fingerprint != want {
			return fmt.Errorf("result %q fingerprint mismatch", key)
		}
	}
	return nil
}
