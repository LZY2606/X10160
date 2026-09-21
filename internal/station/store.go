package station

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const schemaVersion = 1

type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

type NotFoundError struct{ What string }

func (e *NotFoundError) Error() string { return e.What + " 不存在" }

type Bundle struct {
	Schema          int                           `json:"schema"`
	ExportedAt      time.Time                     `json:"exported_at"`
	Units           map[string]Unit               `json:"units"`
	Devices         map[string]*Device            `json:"devices"`
	ReferencePoints map[string]*ReferencePoint    `json:"reference_points"`
	Versions        map[string]*CurveVersion      `json:"curve_versions"`
	Readings        map[string]*Reading           `json:"readings"`
	Results         map[string]*CalibrationResult `json:"results"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	Bundle
}

func newBundle() Bundle {
	return Bundle{
		Schema:          schemaVersion,
		Units:           builtinUnits(),
		Devices:         map[string]*Device{},
		ReferencePoints: map[string]*ReferencePoint{},
		Versions:        map[string]*CurveVersion{},
		Readings:        map[string]*Reading{},
		Results:         map[string]*CalibrationResult{},
	}
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, Bundle: newBundle()}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var loaded Bundle
	if err := json.Unmarshal(b, &loaded); err != nil {
		return nil, fmt.Errorf("数据文件解析失败: %w", err)
	}
	if err := verifyBundle(&loaded); err != nil {
		return nil, err
	}
	if loaded.Schema == 0 {
		loaded.Schema = schemaVersion
	}
	if loaded.Units == nil {
		loaded.Units = builtinUnits()
	} else {
		for name, u := range builtinUnits() {
			if _, ok := loaded.Units[name]; !ok {
				loaded.Units[name] = u
			}
		}
	}
	s.Bundle = loaded
	return s, nil
}

// verifyBundle re-checks every fingerprint so tampering or divergent
// serialization after export/import is rejected instead of silently trusted.
func verifyBundle(b *Bundle) error {
	for _, d := range b.Devices {
		if d.ID != "" && fingerprintDevice(d) == "" {
			return errors.New("device fingerprint error")
		}
	}
	for _, v := range b.Versions {
		if fp := fingerprintVersion(v); fp != v.Fingerprint {
			return fmt.Errorf("曲线版本 %s 指纹不一致（数据已被改动或序列化不一致）", v.ID)
		}
	}
	for id, r := range b.Results {
		if fp := fingerprintResult(r); fp != r.Fingerprint {
			return fmt.Errorf("结果 %s 指纹不一致（历史已被改动）", id)
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.Bundle, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newID(prefix string, taken func(string) bool) string {
	for range 8 {
		var raw [6]byte
		_, _ = rand.Read(raw[:])
		id := prefix + "_" + hex.EncodeToString(raw[:])
		if !taken(id) {
			return id
		}
	}
	return prefix + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
}

func normTime(t time.Time) time.Time { return t.UTC() }

// ---------------- Units ----------------

func (s *Store) ListUnits() []Unit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sortedUnits()
}

func (s *Store) RegisterUnit(u Unit) error {
	u.Name = strings.TrimSpace(u.Name)
	if u.Name == "" {
		return errors.New("单位名称不能为空")
	}
	if u.Dimension == "" {
		return errors.New("量纲不能为空")
	}
	if u.Factor <= 0 {
		return errors.New("换算系数必须为正数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Units[u.Name]; ok {
		return &ConflictError{Msg: "单位已存在: " + u.Name}
	}
	u.Builtin = false
	s.Units[u.Name] = u
	return s.saveLocked()
}

// ---------------- Devices ----------------

type DeviceInput struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	RawUnit          string `json:"raw_unit"`
	CalibratedUnit   string `json:"calibrated_unit"`
	Description      string `json:"description"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (s *Store) ListDevices() []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Device, 0, len(s.Devices))
	for _, d := range s.Devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) GetDevice(id string) (*Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.Devices[id]
	if !ok {
		return nil, &NotFoundError{"设备 " + id}
	}
	return d, nil
}

func (s *Store) RegisterDevice(in DeviceInput) (*Device, error) {
	if in.Name == "" {
		return nil, errors.New("设备名称不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Units[in.RawUnit]; !ok {
		return nil, &UnknownUnitError{in.RawUnit}
	}
	if _, ok := s.Units[in.CalibratedUnit]; !ok {
		return nil, &UnknownUnitError{in.CalibratedUnit}
	}
	id := strings.TrimSpace(in.ID)
	if id != "" {
		if _, ok := s.Devices[id]; ok {
			return nil, &ConflictError{"设备 id 已存在: " + id}
		}
	} else {
		id = newID("dev", func(x string) bool { _, ok := s.Devices[x]; return ok })
	}
	now := normTime(time.Now())
	d := &Device{
		ID: id, Name: in.Name, RawUnit: in.RawUnit, CalibratedUnit: in.CalibratedUnit,
		Description: in.Description, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	s.Devices[id] = d
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) UpdateDevice(in DeviceInput) (*Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.Devices[in.ID]
	if !ok {
		return nil, &NotFoundError{"设备 " + in.ID}
	}
	if d.Revision != in.ExpectedRevision {
		return nil, &ConflictError{fmt.Sprintf("设备版本冲突: 期望 revision=%d，当前=%d（请刷新后重试）", in.ExpectedRevision, d.Revision)}
	}
	if _, ok := s.Units[in.RawUnit]; !ok {
		return nil, &UnknownUnitError{in.RawUnit}
	}
	if _, ok := s.Units[in.CalibratedUnit]; !ok {
		return nil, &UnknownUnitError{in.CalibratedUnit}
	}
	d.Name, d.Description = in.Name, in.Description
	d.RawUnit, d.CalibratedUnit = in.RawUnit, in.CalibratedUnit
	d.Revision++
	d.UpdatedAt = normTime(time.Now())
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return d, nil
}

// ---------------- Reference points ----------------

type RefInput struct {
	ID          string  `json:"id"`
	DeviceID    string  `json:"device_id"`
	Name        string  `json:"name"`
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	Uncertainty float64 `json:"uncertainty"`
	Source      string  `json:"source"`
}

func (s *Store) ListReferencePoints(deviceID string) []*ReferencePoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ReferencePoint, 0)
	for _, r := range s.ReferencePoints {
		if deviceID != "" && r.DeviceID != deviceID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) AddReferencePoint(in RefInput) (*ReferencePoint, error) {
	if in.Name == "" {
		return nil, errors.New("参考点名称不能为空")
	}
	if in.Uncertainty < 0 {
		return nil, errors.New("参考点不确定度不能为负")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.Units[in.Unit]
	if !ok {
		return nil, &UnknownUnitError{in.Unit}
	}
	if in.DeviceID != "" {
		if _, ok := s.Devices[in.DeviceID]; !ok {
			return nil, &NotFoundError{"设备 " + in.DeviceID}
		}
	}
	id := strings.TrimSpace(in.ID)
	if id != "" {
		if _, ok := s.ReferencePoints[id]; ok {
			return nil, &ConflictError{"参考点 id 已存在: " + id}
		}
	} else {
		id = newID("ref", func(x string) bool { _, ok := s.ReferencePoints[x]; return ok })
	}
	r := &ReferencePoint{
		ID: id, DeviceID: in.DeviceID, Name: in.Name, Dimension: u.Dimension,
		Value: in.Value, Unit: in.Unit, Uncertainty: in.Uncertainty, Source: in.Source,
		CreatedAt: normTime(time.Now()),
	}
	s.ReferencePoints[id] = r
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) Result(id string) *CalibrationResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Results[id]
}
