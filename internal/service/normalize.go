package service

import "sort"

import "calibration-trace/internal/model"

func sortCurveSegments(c *model.CurveVersion) {
	sort.SliceStable(c.Segments, func(i, j int) bool {
		if c.Segments[i].XMin != c.Segments[j].XMin {
			return c.Segments[i].XMin < c.Segments[j].XMin
		}
		if c.Segments[i].XMax != c.Segments[j].XMax {
			return c.Segments[i].XMax < c.Segments[j].XMax
		}
		return c.Segments[i].ID < c.Segments[j].ID
	})
}

func normalizeCurve(c *model.CurveVersion) {
	sortCurveSegments(c)
	sort.Strings(c.ReferenceIDs)
}
