package calib

import (
	"fmt"
	"math"
	"strings"

	"calibration-trace/internal/model"
)

func ValidateUnit(u model.Unit) error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("unit id is required")
	}
	if strings.TrimSpace(u.Name) == "" {
		return fmt.Errorf("unit name is required")
	}
	if strings.TrimSpace(u.Dimension) == "" {
		return fmt.Errorf("dimension is required")
	}
	if u.Factor <= 0 || math.IsNaN(u.Factor) || math.IsInf(u.Factor, 0) {
		return fmt.Errorf("unit factor must be a positive finite number")
	}
	if math.IsNaN(u.Offset) || math.IsInf(u.Offset, 0) {
		return fmt.Errorf("unit offset must be finite")
	}
	return nil
}

func ConvertValue(value float64, from, to model.Unit) (float64, error) {
	if from.Dimension != to.Dimension {
		return 0, fmt.Errorf("dimension mismatch: %s (%s) cannot convert to %s (%s)", from.ID, from.Dimension, to.ID, to.Dimension)
	}
	return (value*from.Factor + from.Offset - to.Offset) / to.Factor, nil
}
