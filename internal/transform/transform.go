package transform

import (
	"github.com/erenalidal/existora2pg/internal/schema"
)

// TransformRowInline applies Oracle→PG value transformations to a row in-place.
func TransformRowInline(row []any, columns []schema.Column) {
	for i, val := range row {
		if val == nil {
			continue
		}
		row[i] = TransformValue(val, columns[i])
	}
}

// TransformValue handles Oracle-specific type quirks.
func TransformValue(val any, col schema.Column) any {
	switch v := val.(type) {
	case string:
		// Oracle treats empty string as NULL — preserve this behavior
		if v == "" {
			return nil
		}
		return v

	case []byte:
		// RAW/BLOB data passes through as-is (pgx handles BYTEA)
		if len(v) == 0 {
			return nil
		}
		return v

	default:
		// godror handles most type conversions (time.Time, int64, float64, etc.)
		return val
	}
}
