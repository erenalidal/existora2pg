package schema

import (
	"fmt"
	"strings"
)

// MapOracleToPG converts an Oracle column type to the equivalent PostgreSQL type.
// It uses the column's OracleType, Precision, Scale, and Length fields.
func MapOracleToPG(col Column) string {
	oType := strings.ToUpper(strings.TrimSpace(col.OracleType))

	switch oType {
	case "NUMBER":
		return mapNumber(col.Precision, col.Scale)
	case "FLOAT":
		return "DOUBLE PRECISION"
	case "BINARY_FLOAT":
		return "REAL"
	case "BINARY_DOUBLE":
		return "DOUBLE PRECISION"

	case "VARCHAR2", "NVARCHAR2":
		if col.Length != nil && *col.Length > 0 {
			return fmt.Sprintf("VARCHAR(%d)", *col.Length)
		}
		return "TEXT"
	case "CHAR", "NCHAR":
		if col.Length != nil && *col.Length > 0 {
			return fmt.Sprintf("CHAR(%d)", *col.Length)
		}
		return "CHAR(1)"

	case "CLOB", "NCLOB", "LONG":
		return "TEXT"
	case "BLOB", "LONG RAW":
		return "BYTEA"
	case "RAW":
		return "BYTEA"

	case "DATE":
		return "TIMESTAMP(0)"
	case "TIMESTAMP":
		p := 6
		if col.Scale != nil {
			p = *col.Scale
		}
		return fmt.Sprintf("TIMESTAMP(%d)", p)
	case "TIMESTAMP WITH TIME ZONE", "TIMESTAMP(6) WITH TIME ZONE":
		return "TIMESTAMPTZ"
	case "TIMESTAMP WITH LOCAL TIME ZONE", "TIMESTAMP(6) WITH LOCAL TIME ZONE":
		return "TIMESTAMPTZ"

	case "INTERVAL YEAR TO MONTH":
		return "INTERVAL"
	case "INTERVAL DAY TO SECOND":
		return "INTERVAL"

	case "XMLTYPE":
		return "XML"

	case "ROWID", "UROWID":
		return "VARCHAR(18)"

	default:
		// Handle TIMESTAMP(p) variants that come as e.g. "TIMESTAMP(3)"
		if strings.HasPrefix(oType, "TIMESTAMP") {
			if strings.Contains(oType, "TIME ZONE") || strings.Contains(oType, "LOCAL") {
				return "TIMESTAMPTZ"
			}
			return "TIMESTAMP(6)"
		}
		// Unknown type: pass through as-is (will likely cause DDL error, which is intentional)
		return oType
	}
}

// mapNumber maps Oracle NUMBER(precision, scale) to PostgreSQL numeric types.
func mapNumber(precision, scale *int) string {
	// NUMBER without precision/scale -> NUMERIC
	if precision == nil || *precision == 0 {
		return "NUMERIC"
	}

	p := *precision
	s := 0
	if scale != nil {
		s = *scale
	}

	// Integer types (scale = 0)
	// Oracle NUMBER(p,0) max value = 10^p - 1
	// SMALLINT max = 32,767 → safe only for p ≤ 4 (max 9,999)
	// INTEGER max = 2,147,483,647 → safe for p ≤ 9 (max 999,999,999)
	// BIGINT max = 9.2×10^18 → safe for p ≤ 18
	if s == 0 {
		switch {
		case p <= 4:
			return "SMALLINT"
		case p <= 9:
			return "INTEGER"
		case p <= 18:
			return "BIGINT"
		default:
			return fmt.Sprintf("NUMERIC(%d)", p)
		}
	}

	// Decimal types
	return fmt.Sprintf("NUMERIC(%d,%d)", p, s)
}

// ApplyTypeMappings fills PGType for all columns in all tables.
func ApplyTypeMappings(s *Schema) {
	for i := range s.Tables {
		for j := range s.Tables[i].Columns {
			s.Tables[i].Columns[j].PGType = MapOracleToPG(s.Tables[i].Columns[j])
		}
	}
}
