package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"calibration-trace/internal/model"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Store struct {
	path string
	mu   sync.RWMutex
	data model.Data
}

func NewMemory() *Store { return &Store{data: model.Data{}} }

func New(path string) (*Store, error) {
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.data = model.Data{}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse data file: %w", err)
	}
	return s, nil
}

func (s *Store) Snapshot() model.Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneData(s.data)
}

func (s *Store) Mutate(fn func(*model.Data) error) (model.Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := cloneData(s.data)
	if err := fn(&d); err != nil {
		return model.Data{}, err
	}
	if err := s.replaceLocked(d); err != nil {
		return model.Data{}, err
	}
	return cloneData(s.data), nil
}

func (s *Store) Replace(d model.Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaceLocked(cloneData(d))
}

func (s *Store) replaceLocked(d model.Data) error {
	if s.path == "" {
		s.data = d
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".trace-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	s.data = d
	return nil
}

func cloneData(d model.Data) model.Data {
	out := d
	out.Units = append([]model.Unit(nil), d.Units...)
	out.Devices = append([]model.Device(nil), d.Devices...)
	out.References = append([]model.ReferencePoint(nil), d.References...)
	out.Curves = append([]model.CurveVersion(nil), d.Curves...)
	out.Readings = append([]model.RawReading(nil), d.Readings...)
	out.Results = append([]model.CalibratedResult(nil), d.Results...)
	for i := range d.Curves {
		out.Curves[i].Segments = cloneSegments(d.Curves[i].Segments)
		out.Curves[i].ReferenceIDs = append([]string(nil), d.Curves[i].ReferenceIDs...)
	}
	return out
}

func cloneSegments(in []model.Segment) []model.Segment {
	out := append([]model.Segment(nil), in...)
	for i := range out {
		out[i].Points = append([]model.LinearPoint(nil), in[i].Points...)
		out[i].Coefficients = append([]float64(nil), in[i].Coefficients...)
	}
	return out
}
