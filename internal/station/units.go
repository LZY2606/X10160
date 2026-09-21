package station

import (
	"fmt"
	"sort"
)

const (
	DimElectricVoltage = "electric_voltage"
	DimPressure        = "pressure"
	DimTemperature     = "temperature"
	DimCurrent         = "electric_current"
	DimResistance      = "electric_resistance"
	DimFrequency       = "frequency"
	DimDimensionless   = "dimensionless"
)

func builtinUnits() map[string]Unit {
	defs := []Unit{
		{Name: "V", Dimension: DimElectricVoltage, Symbol: "V", Factor: 1},
		{Name: "mV", Dimension: DimElectricVoltage, Symbol: "mV", Factor: 0.001},
		{Name: "Pa", Dimension: DimPressure, Symbol: "Pa", Factor: 1},
		{Name: "kPa", Dimension: DimPressure, Symbol: "kPa", Factor: 1000},
		{Name: "MPa", Dimension: DimPressure, Symbol: "MPa", Factor: 1e6},
		{Name: "bar", Dimension: DimPressure, Symbol: "bar", Factor: 1e5},
		{Name: "psi", Dimension: DimPressure, Symbol: "psi", Factor: 6894.757293168},
		// Affine temperature units: base is kelvin.
		{Name: "K", Dimension: DimTemperature, Symbol: "K", Factor: 1},
		{Name: "degC", Dimension: DimTemperature, Symbol: "°C", Factor: 1, Offset: 273.15},
		{Name: "degF", Dimension: DimTemperature, Symbol: "°F", Factor: 5.0 / 9.0, Offset: 459.67 * 5.0 / 9.0},
		{Name: "A", Dimension: DimCurrent, Symbol: "A", Factor: 1},
		{Name: "mA", Dimension: DimCurrent, Symbol: "mA", Factor: 0.001},
		{Name: "ohm", Dimension: DimResistance, Symbol: "Ω", Factor: 1},
		{Name: "Hz", Dimension: DimFrequency, Symbol: "Hz", Factor: 1},
		{Name: "kHz", Dimension: DimFrequency, Symbol: "kHz", Factor: 1000},
		{Name: "ratio", Dimension: DimDimensionless, Symbol: "1", Factor: 1},
	}
	m := make(map[string]Unit, len(defs))
	for _, u := range defs {
		u.Builtin = true
		m[u.Name] = u
	}
	return m
}

type DimMismatchError struct {
	From, To string
}

func (e *DimMismatchError) Error() string {
	return fmt.Sprintf("量纲不匹配: %s 与 %s", e.From, e.To)
}

type UnknownUnitError struct{ Name string }

func (e *UnknownUnitError) Error() string { return fmt.Sprintf("未知单位: %s", e.Name) }

// convertPoint converts a point value between units, checking dimensions
// first. It never mutates stored facts; callers own the derived value.
func (s *Store) convertPoint(value float64, from, to string) (float64, Unit, Unit, error) {
	f, ok := s.Units[from]
	if !ok {
		return 0, Unit{}, Unit{}, &UnknownUnitError{from}
	}
	t, ok := s.Units[to]
	if !ok {
		return 0, Unit{}, Unit{}, &UnknownUnitError{to}
	}
	if f.Dimension != t.Dimension {
		return 0, f, t, &DimMismatchError{From: f.Dimension, To: t.Dimension}
	}
	return t.fromBase(f.toBase(value)), f, t, nil
}

func (s *Store) unitDimension(name string) (string, bool) {
	u, ok := s.Units[name]
	return u.Dimension, ok
}

func (s *Store) sortedUnits() []Unit {
	out := make([]Unit, 0, len(s.Units))
	for _, u := range s.Units {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
