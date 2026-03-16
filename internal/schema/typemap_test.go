package schema

import "testing"

func intPtr(v int) *int { return &v }

func TestMapOracleToPG(t *testing.T) {
	tests := []struct {
		name     string
		col      Column
		expected string
	}{
		// NUMBER integer mappings
		{
			name:     "NUMBER(3,0) -> SMALLINT",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(3), Scale: intPtr(0)},
			expected: "SMALLINT",
		},
		{
			name:     "NUMBER(4,0) -> SMALLINT",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(4), Scale: intPtr(0)},
			expected: "SMALLINT",
		},
		{
			name:     "NUMBER(9,0) -> INTEGER",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(9), Scale: intPtr(0)},
			expected: "INTEGER",
		},
		{
			name:     "NUMBER(10,0) -> BIGINT",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(10), Scale: intPtr(0)},
			expected: "BIGINT",
		},
		{
			name:     "NUMBER(18,0) -> BIGINT",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(18), Scale: intPtr(0)},
			expected: "BIGINT",
		},
		{
			name:     "NUMBER(19,0) -> BIGINT",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(19), Scale: intPtr(0)},
			expected: "NUMERIC(19)",
		},
		{
			name:     "NUMBER(38,0) -> NUMERIC(38)",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(38), Scale: intPtr(0)},
			expected: "NUMERIC(38)",
		},

		// NUMBER decimal mappings
		{
			name:     "NUMBER(15,2) -> NUMERIC(15,2)",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(15), Scale: intPtr(2)},
			expected: "NUMERIC(15,2)",
		},
		{
			name:     "NUMBER(20,5) -> NUMERIC(20,5)",
			col:      Column{OracleType: "NUMBER", Precision: intPtr(20), Scale: intPtr(5)},
			expected: "NUMERIC(20,5)",
		},

		// NUMBER without precision
		{
			name:     "NUMBER no precision -> NUMERIC",
			col:      Column{OracleType: "NUMBER"},
			expected: "NUMERIC",
		},

		// String types
		{
			name:     "VARCHAR2(200) -> VARCHAR(200)",
			col:      Column{OracleType: "VARCHAR2", Length: intPtr(200)},
			expected: "VARCHAR(200)",
		},
		{
			name:     "NVARCHAR2(500) -> VARCHAR(500)",
			col:      Column{OracleType: "NVARCHAR2", Length: intPtr(500)},
			expected: "VARCHAR(500)",
		},
		{
			name:     "CHAR(10) -> CHAR(10)",
			col:      Column{OracleType: "CHAR", Length: intPtr(10)},
			expected: "CHAR(10)",
		},

		// LOB types
		{
			name:     "CLOB -> TEXT",
			col:      Column{OracleType: "CLOB"},
			expected: "TEXT",
		},
		{
			name:     "NCLOB -> TEXT",
			col:      Column{OracleType: "NCLOB"},
			expected: "TEXT",
		},
		{
			name:     "BLOB -> BYTEA",
			col:      Column{OracleType: "BLOB"},
			expected: "BYTEA",
		},
		{
			name:     "RAW -> BYTEA",
			col:      Column{OracleType: "RAW"},
			expected: "BYTEA",
		},
		{
			name:     "LONG -> TEXT",
			col:      Column{OracleType: "LONG"},
			expected: "TEXT",
		},
		{
			name:     "LONG RAW -> BYTEA",
			col:      Column{OracleType: "LONG RAW"},
			expected: "BYTEA",
		},

		// Date/time types
		{
			name:     "DATE -> TIMESTAMP(0)",
			col:      Column{OracleType: "DATE"},
			expected: "TIMESTAMP(0)",
		},
		{
			name:     "TIMESTAMP -> TIMESTAMP(6)",
			col:      Column{OracleType: "TIMESTAMP", Scale: intPtr(6)},
			expected: "TIMESTAMP(6)",
		},
		{
			name:     "TIMESTAMP(3) -> TIMESTAMP(3)",
			col:      Column{OracleType: "TIMESTAMP", Scale: intPtr(3)},
			expected: "TIMESTAMP(3)",
		},
		{
			name:     "TIMESTAMP WITH TIME ZONE -> TIMESTAMPTZ",
			col:      Column{OracleType: "TIMESTAMP WITH TIME ZONE"},
			expected: "TIMESTAMPTZ",
		},
		{
			name:     "TIMESTAMP WITH LOCAL TIME ZONE -> TIMESTAMPTZ",
			col:      Column{OracleType: "TIMESTAMP WITH LOCAL TIME ZONE"},
			expected: "TIMESTAMPTZ",
		},

		// Float types
		{
			name:     "FLOAT -> DOUBLE PRECISION",
			col:      Column{OracleType: "FLOAT"},
			expected: "DOUBLE PRECISION",
		},
		{
			name:     "BINARY_FLOAT -> REAL",
			col:      Column{OracleType: "BINARY_FLOAT"},
			expected: "REAL",
		},
		{
			name:     "BINARY_DOUBLE -> DOUBLE PRECISION",
			col:      Column{OracleType: "BINARY_DOUBLE"},
			expected: "DOUBLE PRECISION",
		},

		// Special types
		{
			name:     "XMLTYPE -> XML",
			col:      Column{OracleType: "XMLTYPE"},
			expected: "XML",
		},
		{
			name:     "ROWID -> VARCHAR(18)",
			col:      Column{OracleType: "ROWID"},
			expected: "VARCHAR(18)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapOracleToPG(tt.col)
			if result != tt.expected {
				t.Errorf("MapOracleToPG(%s) = %q, want %q", tt.col.OracleType, result, tt.expected)
			}
		})
	}
}

func TestApplyTypeMappings(t *testing.T) {
	s := &Schema{
		Tables: []Table{
			{
				Name: "TEST",
				Columns: []Column{
					{Name: "ID", OracleType: "NUMBER", Precision: intPtr(10), Scale: intPtr(0)},
					{Name: "NAME", OracleType: "VARCHAR2", Length: intPtr(100)},
					{Name: "CREATED", OracleType: "DATE"},
				},
			},
		},
	}

	ApplyTypeMappings(s)

	expected := []string{"BIGINT", "VARCHAR(100)", "TIMESTAMP(0)"}
	for i, col := range s.Tables[0].Columns {
		if col.PGType != expected[i] {
			t.Errorf("column %s: PGType = %q, want %q", col.Name, col.PGType, expected[i])
		}
	}
}
