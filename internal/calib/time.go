package calib

import (
	"fmt"
	"time"
)

func ParseTime(value, field string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339 UTC/offset time", field)
	}
	return t.UTC(), nil
}

func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
