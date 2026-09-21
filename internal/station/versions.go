package station

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type VersionInput struct {
	ID                    string    `json:"id"`
	DeviceID              string    `json:"device_id"`
	Note                  string    `json:"note"`
	Segments              []Segment `json:"segments"`
	ValidFrom             time.Time `json:"valid_from"`
	ValidTo               time.Time `json:"valid_to"`
	AllowExtrapolate      bool      `json:"allow_extrapolate"`
	ExtrapUncertaintyRate float64   `json:"extrap_uncertainty_rate"`
	ExpectedRevision      int64     `json:"expected_revision"`
}

func (s *Store) ListVersions(deviceID string) []*CurveVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*CurveVersion, 0)
	for _, v := range s.Versions {
		if deviceID != "" && v.DeviceID != deviceID {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ValidFrom.Before(out[j].ValidFrom) })
	return out
}

func (s *Store) GetVersion(id string) (*CurveVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.Versions[id]
	if !ok {
		return nil, &NotFoundError{"曲线版本 " + id}
	}
	return v, nil
}

func (s *Store) validateRefsLocked(deviceID string, segs []Segment) error {
	for _, seg := range segs {
		for _, rid := range seg.Refs {
			r, ok := s.ReferencePoints[rid]
			if !ok {
				return &NotFoundError{"参考点 " + rid}
			}
			if r.DeviceID != "" && r.DeviceID != deviceID {
				return fmt.Errorf("参考点 %s 不属于设备 %s", rid, deviceID)
			}
		}
	}
	return nil
}

func (s *Store) CreateVersion(in VersionInput) (*CurveVersion, []ValidationIssue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Devices[in.DeviceID]; !ok {
		return nil, nil, &NotFoundError{"设备 " + in.DeviceID}
	}
	if !in.ValidTo.IsZero() && !in.ValidFrom.Before(in.ValidTo) {
		return nil, nil, errors.New("有效期开始必须早于结束")
	}
	if err := s.validateRefsLocked(in.DeviceID, in.Segments); err != nil {
		return nil, nil, err
	}
	v := &CurveVersion{
		DeviceID: in.DeviceID, Note: in.Note, Segments: in.Segments,
		ValidFrom: normTime(in.ValidFrom), ValidTo: normTime(in.ValidTo),
		AllowExtrapolate:      in.AllowExtrapolate,
		ExtrapUncertaintyRate: in.ExtrapUncertaintyRate,
		State:                 StateDraft, Revision: 1, CreatedAt: normTime(time.Now()),
	}
	if issues := v.validateSegments(); len(issues) > 0 {
		return v, issues, nil
	}
	id := strings.TrimSpace(in.ID)
	if id != "" {
		if _, ok := s.Versions[id]; ok {
			return nil, nil, &ConflictError{"曲线版本 id 已存在: " + id}
		}
	} else {
		id = newID("ver", func(x string) bool { _, ok := s.Versions[x]; return ok })
	}
	v.ID = id
	v.Fingerprint = fingerprintVersion(v)
	s.Versions[id] = v
	if err := s.saveLocked(); err != nil {
		return nil, nil, err
	}
	return v, nil, nil
}

// UpdateVersion edits a draft (published versions are frozen forever).
func (s *Store) UpdateVersion(in VersionInput) (*CurveVersion, []ValidationIssue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Versions[in.ID]
	if !ok {
		return nil, nil, &NotFoundError{"曲线版本 " + in.ID}
	}
	if v.State == StatePublished {
		return nil, nil, &ConflictError{"曲线版本已发布，内容永久冻结；请新建版本"}
	}
	if v.Revision != in.ExpectedRevision {
		return nil, nil, &ConflictError{fmt.Sprintf("曲线版本冲突: 期望 revision=%d，当前=%d（有人先保存过，请刷新）", in.ExpectedRevision, v.Revision)}
	}
	if _, ok := s.Devices[in.DeviceID]; !ok {
		return nil, nil, &NotFoundError{"设备 " + in.DeviceID}
	}
	if !in.ValidTo.IsZero() && !in.ValidFrom.Before(in.ValidTo) {
		return nil, nil, errors.New("有效期开始必须早于结束")
	}
	if err := s.validateRefsLocked(in.DeviceID, in.Segments); err != nil {
		return nil, nil, err
	}
	updated := &CurveVersion{
		ID: v.ID, DeviceID: in.DeviceID, Revision: v.Revision + 1,
		State: StateDraft, Note: in.Note, Segments: in.Segments,
		ValidFrom: normTime(in.ValidFrom), ValidTo: normTime(in.ValidTo),
		AllowExtrapolate:      in.AllowExtrapolate,
		ExtrapUncertaintyRate: in.ExtrapUncertaintyRate,
		CreatedAt:             v.CreatedAt,
	}
	if issues := updated.validateSegments(); len(issues) > 0 {
		return updated, issues, nil
	}
	updated.Fingerprint = fingerprintVersion(updated)
	s.Versions[v.ID] = updated
	if err := s.saveLocked(); err != nil {
		return nil, nil, err
	}
	return updated, nil, nil
}

// PublishVersion freezes a draft and stamps it. Published validity windows
// for the same device must not overlap (touching endpoints allowed), since
// overlapping windows would make "the version confirmed at receive time"
// ambiguous.
func (s *Store) PublishVersion(id string, expectedRevision int64) (*CurveVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Versions[id]
	if !ok {
		return nil, &NotFoundError{"曲线版本 " + id}
	}
	if v.State == StatePublished {
		return nil, &ConflictError{"曲线版本已发布"}
	}
	if v.Revision != expectedRevision {
		return nil, &ConflictError{fmt.Sprintf("发布冲突: 期望 revision=%d，当前=%d", expectedRevision, v.Revision)}
	}
	if issues := v.validateSegments(); len(issues) > 0 {
		return nil, fmt.Errorf("曲线仍有 %d 个校验问题，不能发布", len(issues))
	}
	for _, other := range s.Versions {
		if other.ID == v.ID || other.DeviceID != v.DeviceID || other.State != StatePublished {
			continue
		}
		if windowsOverlap(v.ValidFrom, v.ValidTo, other.ValidFrom, other.ValidTo) {
			return nil, &ConflictError{fmt.Sprintf("有效期与已发布版本 %s 重叠（端点相接允许）", other.ID)}
		}
	}
	now := normTime(time.Now())
	v.State = StatePublished
	v.PublishedAt = now
	v.Revision++
	v.Fingerprint = fingerprintVersion(v)
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return v, nil
}

// windowsOverlap reports positive-length overlap of half-open windows
// [from,to). Zero ValidTo means "open ended". Touching endpoints do not
// overlap.
// windowsOverlap reports positive-length overlap of half-open windows
// [from,to). A zero ValidTo means "open ended". Touching endpoints do not
// overlap: [a,b) and [b,c) are disjoint.
func windowsOverlap(f1, t1, f2, t2 time.Time) bool {
	// [f1,t1) ends at/before [f2,t2) starts.
	if !t1.IsZero() && !t1.After(f2) {
		return false
	}
	// [f2,t2) ends at/before [f1,t1) starts.
	if !t2.IsZero() && !t2.After(f1) {
		return false
	}
	return true
}

func (s *Store) Coverage(id string) (CoverageView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.Versions[id]
	if !ok {
		return CoverageView{}, &NotFoundError{"曲线版本 " + id}
	}
	return v.Coverage(), nil
}
