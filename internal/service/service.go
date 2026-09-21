package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"calibration-trace/internal/calib"
	"calibration-trace/internal/model"
	"calibration-trace/internal/store"
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(st *store.Store) *Service {
	return &Service{store: st, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Snapshot() model.Data { return s.store.Snapshot() }

func unit(units []model.Unit, id string) (model.Unit, bool) {
	for _, u := range units {
		if u.ID == id {
			return u, true
		}
	}
	return model.Unit{}, false
}

func device(devices []model.Device, id string) (model.Device, bool) {
	for _, d := range devices {
		if d.ID == id {
			return d, true
		}
	}
	return model.Device{}, false
}

func curveAt(curves []model.CurveVersion, id string) *model.CurveVersion {
	for i := range curves {
		if curves[i].ID == id {
			return &curves[i]
		}
	}
	return nil
}

func readingAt(rs []model.RawReading, deviceID, sampleID string) *model.RawReading {
	for i := range rs {
		if rs[i].DeviceID == deviceID && rs[i].SampleID == sampleID {
			return &rs[i]
		}
	}
	return nil
}

func resultAt(rs []model.CalibratedResult, deviceID, sampleID string) *model.CalibratedResult {
	for i := range rs {
		if rs[i].DeviceID == deviceID && rs[i].SampleID == sampleID {
			return &rs[i]
		}
	}
	return nil
}

func newID(prefix string, exists func(string) bool) string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("%s-%d", prefix, i)
		if !exists(id) {
			return id
		}
	}
}

func (s *Service) CreateUnit(in model.Unit) (model.Unit, error) {
	if err := calib.ValidateUnit(in); err != nil {
		return model.Unit{}, err
	}
	_, err := s.store.Mutate(func(d *model.Data) error {
		if _, ok := unit(d.Units, in.ID); ok {
			return fmt.Errorf("%w: unit %q", store.ErrConflict, in.ID)
		}
		fp, err := calib.Fingerprint("unit", map[string]any{"id": in.ID, "name": in.Name, "dimension": in.Dimension, "factor": in.Factor, "offset": in.Offset})
		if err != nil {
			return err
		}
		in.Fingerprint = fp
		d.Units = append(d.Units, in)
		return nil
	})
	return in, err
}

func (s *Service) CreateDevice(in model.Device) (model.Device, error) {
	if strings.TrimSpace(in.ID) == "" {
		return model.Device{}, fmt.Errorf("device id is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return model.Device{}, fmt.Errorf("device name is required")
	}
	_, err := s.store.Mutate(func(d *model.Data) error {
		if _, ok := device(d.Devices, in.ID); ok {
			return fmt.Errorf("%w: device %q", store.ErrConflict, in.ID)
		}
		input, ok := unit(d.Units, in.InputUnitID)
		if !ok {
			return fmt.Errorf("input unit %q not found", in.InputUnitID)
		}
		output, ok := unit(d.Units, in.OutputUnitID)
		if !ok {
			return fmt.Errorf("output unit %q not found", in.OutputUnitID)
		}
		if input.Dimension != output.Dimension {
			return fmt.Errorf("device input and output dimensions must agree")
		}
		fp, err := calib.Fingerprint("device", map[string]any{"id": in.ID, "name": in.Name, "input_unit_id": in.InputUnitID, "output_unit_id": in.OutputUnitID})
		if err != nil {
			return err
		}
		in.Fingerprint = fp
		d.Devices = append(d.Devices, in)
		return nil
	})
	return in, err
}

func referenceAt(rs []model.ReferencePoint, id string) (model.ReferencePoint, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return model.ReferencePoint{}, false
}

func (s *Service) CreateReference(in model.ReferencePoint) (model.ReferencePoint, error) {
	if strings.TrimSpace(in.ID) == "" {
		return model.ReferencePoint{}, fmt.Errorf("reference id is required")
	}
	if strings.TrimSpace(in.DeviceID) == "" {
		return model.ReferencePoint{}, fmt.Errorf("device id is required")
	}
	if _, err := calib.ParseTime(in.TakenAt, "taken_at"); err != nil {
		return model.ReferencePoint{}, err
	}
	_, err := s.store.Mutate(func(d *model.Data) error {
		dev, ok := device(d.Devices, in.DeviceID)
		if !ok {
			return fmt.Errorf("device %q not found", in.DeviceID)
		}
		if _, ok := referenceAt(d.References, in.ID); ok {
			return fmt.Errorf("%w: reference %q", store.ErrConflict, in.ID)
		}
		iu, ok := unit(d.Units, in.InputUnitID)
		if !ok {
			return fmt.Errorf("reference input unit not found")
		}
		ou, ok := unit(d.Units, in.OutputUnitID)
		if !ok {
			return fmt.Errorf("reference output unit not found")
		}
		di, _ := unit(d.Units, dev.InputUnitID)
		do, _ := unit(d.Units, dev.OutputUnitID)
		if iu.Dimension != di.Dimension {
			return fmt.Errorf("reference input dimension does not match device")
		}
		if ou.Dimension != do.Dimension {
			return fmt.Errorf("reference output dimension does not match device")
		}
		fp, err := calib.Fingerprint("reference", map[string]any{"id": in.ID, "device_id": in.DeviceID, "name": in.Name, "input_value": in.InputValue, "input_unit_id": in.InputUnitID, "output_value": in.OutputValue, "output_unit_id": in.OutputUnitID, "taken_at": in.TakenAt})
		if err != nil {
			return err
		}
		in.Fingerprint = fp
		d.References = append(d.References, in)
		return nil
	})
	return in, err
}

var _ = math.NaN
var _ = sort.Strings
