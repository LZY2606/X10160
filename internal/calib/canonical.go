package calib

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

func CanonicalJSON(value any) ([]byte, error) {
	normalized, err := normalize(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func Fingerprint(kind string, value any) (string, error) {
	payload, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(kind+":"), payload...))
	return hex.EncodeToString(sum[:]), nil
}

func normalize(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		return v, nil
	case bool:
		return v, nil
	case int:
		return normalize(float64(v))
	case int64:
		return normalize(float64(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("non-finite number cannot be fingerprinted")
		}
		return json.Number(strconv.FormatFloat(v, 'g', -1, 64)), nil
	case float32:
		return normalize(float64(v))
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, err
		}
		return normalize(f)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out, nil
	case []float64:
		out := make([]any, len(v))
		for i, item := range v {
			n, err := normalize(item)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([][2]any, 0, len(v))
		for _, key := range keys {
			n, err := normalize(v[key])
			if err != nil {
				return nil, err
			}
			out = append(out, [2]any{key, n})
		}
		return out, nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var generic any
		dec := json.NewDecoder(bytesReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&generic); err != nil {
			return nil, err
		}
		return normalize(generic)
	}
}
