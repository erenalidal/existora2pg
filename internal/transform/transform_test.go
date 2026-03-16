package transform

import (
	"testing"

	"github.com/erenalidal/existora2pg/internal/schema"
)

func TestTransformValue_EmptyStringToNil(t *testing.T) {
	col := schema.Column{OracleType: "VARCHAR2", PGType: "VARCHAR(100)"}
	result := TransformValue("", col)
	if result != nil {
		t.Errorf("empty string should be nil, got %v", result)
	}
}

func TestTransformValue_NonEmptyString(t *testing.T) {
	col := schema.Column{OracleType: "VARCHAR2", PGType: "VARCHAR(100)"}
	result := TransformValue("hello", col)
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
}

func TestTransformValue_NilPassthrough(t *testing.T) {
	col := schema.Column{OracleType: "NUMBER", PGType: "INTEGER"}
	result := TransformValue(nil, col)
	if result != nil {
		t.Errorf("nil should stay nil, got %v", result)
	}
}

func TestTransformValue_EmptyBytesToNil(t *testing.T) {
	col := schema.Column{OracleType: "RAW", PGType: "BYTEA"}
	result := TransformValue([]byte{}, col)
	if result != nil {
		t.Errorf("empty bytes should be nil, got %v", result)
	}
}

func TestTransformRowInline(t *testing.T) {
	cols := []schema.Column{
		{Name: "ID", OracleType: "NUMBER", PGType: "INTEGER"},
		{Name: "NAME", OracleType: "VARCHAR2", PGType: "VARCHAR(100)"},
	}

	rows := [][]any{
		{1, "hello"},
		{2, ""},
		{3, nil},
	}

	for _, row := range rows {
		TransformRowInline(row, cols)
	}

	// Non-empty string stays
	if rows[0][1] != "hello" {
		t.Errorf("expected 'hello', got %v", rows[0][1])
	}
	// Empty string transformed to nil
	if rows[1][1] != nil {
		t.Errorf("empty string should be nil, got %v", rows[1][1])
	}
	// nil stays nil
	if rows[2][1] != nil {
		t.Errorf("nil should stay nil, got %v", rows[2][1])
	}
}
