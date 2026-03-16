package validate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/erenalidal/existora2pg/internal/schema"
)

// OracleCounter provides row counts from Oracle.
type OracleCounter interface {
	Count(ctx context.Context, owner, tableName string, partition *schema.Partition, where string) (int64, error)
}

// PGCounter provides row counts from PostgreSQL.
type PGCounter interface {
	Count(ctx context.Context, tableName string) (int64, error)
}

// Result holds the validation result for a single table.
type Result struct {
	TableName   string
	OracleCount int64
	PGCount     int64
	Match       bool
	Error       string
}

// Validator compares Oracle and PostgreSQL data.
type Validator struct {
	oracle OracleCounter
	pg     PGCounter
	logger *slog.Logger
}

// NewValidator creates a data validator.
func NewValidator(oracle OracleCounter, pg PGCounter, logger *slog.Logger) *Validator {
	return &Validator{oracle: oracle, pg: pg, logger: logger}
}

// ValidateRowCount compares row counts between Oracle and PostgreSQL.
// whereClause limits the Oracle count to match what was actually migrated.
func (v *Validator) ValidateRowCount(ctx context.Context, owner, tableName, whereClause string) (*Result, error) {
	r := &Result{TableName: tableName}

	oraCount, err := v.oracle.Count(ctx, owner, tableName, nil, whereClause)
	if err != nil {
		r.Error = fmt.Sprintf("oracle count: %v", err)
		return r, err
	}
	r.OracleCount = oraCount

	pgCount, err := v.pg.Count(ctx, tableName)
	if err != nil {
		r.Error = fmt.Sprintf("pg count: %v", err)
		return r, err
	}
	r.PGCount = pgCount

	r.Match = oraCount == pgCount

	if r.Match {
		v.logger.Info("validation passed", "table", tableName, "rows", oraCount)
	} else {
		v.logger.Warn("validation failed", "table", tableName,
			"oracle_count", oraCount, "pg_count", pgCount,
			"diff", oraCount-pgCount)
	}

	return r, nil
}
