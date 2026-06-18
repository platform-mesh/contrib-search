package search

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func queryHash(q string) string {
	h := sha256.Sum256([]byte(q))
	return hex.EncodeToString(h[:])
}

func filtersHash(filters map[string][]string) string {
	if len(filters) == 0 {
		return ""
	}

	keys := make([]string, 0, len(filters))
	for key := range filters {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, key := range keys {
		rawValues := filters[key]
		values := make([]string, 0, len(rawValues))
		for _, value := range rawValues {
			trimmed := strings.TrimSpace(value)
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
		sort.Strings(values)
		if len(values) == 0 {
			continue
		}
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(strings.Join(values, ","))
		b.WriteString(";")
	}

	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
}
