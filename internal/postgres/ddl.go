package postgres

import (
	"fmt"
	"strings"

	"github.com/erenalidal/existora2pg/internal/schema"
)

// NamingConfig controls how Oracle names are converted to PostgreSQL names.
type NamingConfig struct {
	// Convention: "lowercase" (default), "uppercase", "keep_original"
	Convention string
	// TypeOverrides: "TABLE.COLUMN" -> "custom_pg_type"
	TypeOverrides map[string]string
	// NameOverrides: "TABLE" -> "pg_table", "TABLE.COLUMN" -> "pg_col"
	NameOverrides map[string]string
}

// DefaultNaming returns the default naming config (lowercase, ora2pg style).
func DefaultNaming() NamingConfig {
	return NamingConfig{Convention: "lowercase"}
}

// convertName applies naming convention to a name.
func (nc NamingConfig) convertName(oracleName string) string {
	// Check for explicit override first
	if nc.NameOverrides != nil {
		if override, ok := nc.NameOverrides[oracleName]; ok {
			return override
		}
	}

	switch nc.Convention {
	case "uppercase":
		return strings.ToUpper(oracleName)
	case "keep_original":
		return oracleName
	default: // "lowercase" (default, ora2pg style)
		return strings.ToLower(oracleName)
	}
}

// convertTableName applies naming convention + override for table names.
func (nc NamingConfig) convertTableName(tableName string) string {
	if nc.NameOverrides != nil {
		if override, ok := nc.NameOverrides[tableName]; ok {
			return override
		}
	}
	return nc.convertName(tableName)
}

// convertColumnName applies naming convention + override for column names.
// Returns a quoted identifier safe for use in DDL.
func (nc NamingConfig) convertColumnName(tableName, colName string) string {
	if nc.NameOverrides != nil {
		key := tableName + "." + colName
		if override, ok := nc.NameOverrides[key]; ok {
			return quoteIdent(override)
		}
	}
	return quoteIdent(nc.convertName(colName))
}

// getColumnType returns the PG type, checking for overrides first.
func (nc NamingConfig) getColumnType(tableName, colName, defaultPGType string) string {
	if nc.TypeOverrides != nil {
		key := tableName + "." + colName
		if override, ok := nc.TypeOverrides[key]; ok {
			return override
		}
	}
	return defaultPGType
}

// GenerateCreateTable generates the CREATE TABLE DDL for a single table.
func GenerateCreateTable(t schema.Table, pgSchema string) string {
	return GenerateCreateTableWithNaming(t, pgSchema, DefaultNaming())
}

// GenerateCreateTableWithNaming generates CREATE TABLE DDL with custom naming.
func GenerateCreateTableWithNaming(t schema.Table, pgSchema string, nc NamingConfig) string {
	return generateCreateTableInternal(t, pgSchema, nc, false)
}

// GenerateCreateTableUnlogged generates CREATE UNLOGGED TABLE DDL for faster bulk loading.
func GenerateCreateTableUnlogged(t schema.Table, pgSchema string, nc NamingConfig) string {
	return generateCreateTableInternal(t, pgSchema, nc, true)
}

func generateCreateTableInternal(t schema.Table, pgSchema string, nc NamingConfig, unlogged bool) string {
	var b strings.Builder
	tableName := qualifiedName(pgSchema, nc.convertTableName(t.Name))

	keyword := "CREATE TABLE"
	if unlogged {
		keyword = "CREATE UNLOGGED TABLE"
	}
	b.WriteString(fmt.Sprintf("%s %s (\n", keyword, tableName))

	for i, col := range t.Columns {
		pgType := nc.getColumnType(t.Name, col.Name, col.PGType)
		colName := nc.convertColumnName(t.Name, col.Name)
		colDef := fmt.Sprintf("    %s %s", colName, pgType)
		if !col.Nullable {
			colDef += " NOT NULL"
		}
		if col.DefaultExpr != nil {
			colDef += " DEFAULT " + *col.DefaultExpr
		}
		if i < len(t.Columns)-1 {
			colDef += ","
		}
		b.WriteString(colDef + "\n")
	}

	b.WriteString(")")

	if t.IsPartitioned() {
		keyCols := make([]string, len(t.Partitioning.KeyColumns))
		for i, k := range t.Partitioning.KeyColumns {
			keyCols[i] = nc.convertColumnName(t.Name, k)
		}
		b.WriteString(fmt.Sprintf(" PARTITION BY %s (%s)",
			t.Partitioning.Strategy,
			strings.Join(keyCols, ", ")))
	}

	b.WriteString(";\n")
	return b.String()
}

// GeneratePartitionDDL generates CREATE TABLE ... PARTITION OF for each partition.
func GeneratePartitionDDL(t schema.Table, pgSchema string, bounds []PartitionBound) []string {
	return GeneratePartitionDDLWithNaming(t, pgSchema, bounds, DefaultNaming())
}

// GeneratePartitionDDLWithNaming generates partition DDL with custom naming.
func GeneratePartitionDDLWithNaming(t schema.Table, pgSchema string, bounds []PartitionBound, nc NamingConfig) []string {
	if !t.IsPartitioned() || len(bounds) == 0 {
		return nil
	}

	parentName := qualifiedName(pgSchema, nc.convertTableName(t.Name))
	ddls := make([]string, 0, len(bounds))

	for _, pb := range bounds {
		partName := qualifiedName(pgSchema, nc.convertName(pb.Name))
		var ddl string
		if pb.Strategy == "LIST" {
			if len(pb.Values) == 1 && pb.Values[0] == "DEFAULT" {
				ddl = fmt.Sprintf("CREATE TABLE %s PARTITION OF %s DEFAULT;",
					partName, parentName)
			} else {
				ddl = fmt.Sprintf("CREATE TABLE %s PARTITION OF %s FOR VALUES IN (%s);",
					partName, parentName, strings.Join(pb.Values, ", "))
			}
		} else {
			ddl = fmt.Sprintf("CREATE TABLE %s PARTITION OF %s FOR VALUES FROM (%s) TO (%s);",
				partName, parentName, pb.LowerBound, pb.UpperBound)
		}
		ddls = append(ddls, ddl)
	}

	return ddls
}

// PartitionBound represents partition bounds (RANGE: FROM/TO, LIST: IN values).
type PartitionBound struct {
	Name       string
	LowerBound string   // RANGE only
	UpperBound string   // RANGE only
	Values     []string // LIST only: e.g., ["'US'", "'CA'"]
	Strategy   string   // RANGE or LIST
}

// GeneratePrimaryKey generates ALTER TABLE ... ADD CONSTRAINT for a PK.
func GeneratePrimaryKey(t schema.Table, pgSchema string) string {
	return GeneratePrimaryKeyWithNaming(t, pgSchema, DefaultNaming())
}

// GeneratePrimaryKeyWithNaming generates PK DDL with custom naming.
func GeneratePrimaryKeyWithNaming(t schema.Table, pgSchema string, nc NamingConfig) string {
	if t.PrimaryKey == nil || len(t.PrimaryKey.Columns) == 0 {
		return ""
	}
	tableName := qualifiedName(pgSchema, nc.convertTableName(t.Name))
	pkName := quoteIdent(nc.convertName(t.PrimaryKey.Name))
	cols := nc.convertColumnsJoin(t.Name, t.PrimaryKey.Columns)
	return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s PRIMARY KEY (%s);", tableName, pkName, cols)
}

// GenerateIndexes generates CREATE INDEX statements.
func GenerateIndexes(t schema.Table, pgSchema string) []string {
	return GenerateIndexesWithNaming(t, pgSchema, DefaultNaming())
}

// GenerateIndexesWithNaming generates INDEX DDL with custom naming.
// Incompatible indexes (LOCAL partitioned, BITMAP, FUNCTION-BASED) are
// included as comments so the user can see what was skipped and why.
func GenerateIndexesWithNaming(t schema.Table, pgSchema string, nc NamingConfig) []string {
	var ddls []string
	for _, idx := range t.Indexes {
		idxName := quoteIdent(nc.convertName(idx.Name))
		tableName := qualifiedName(pgSchema, nc.convertTableName(t.Name))

		// LOCAL indexes on partitioned tables: skip with comment
		// PostgreSQL automatically creates local indexes via partition inheritance
		if idx.Local && t.IsPartitioned() {
			ddls = append(ddls, fmt.Sprintf(
				"-- SKIPPED: LOCAL index %s (PostgreSQL creates partition-local indexes automatically)", idxName))
			continue
		}

		// FUNCTION-BASED indexes: skip with comment (expression not captured)
		if idx.Type == "FUNCTION-BASED NORMAL" {
			ddls = append(ddls, fmt.Sprintf(
				"-- SKIPPED: FUNCTION-BASED index %s (expression not supported, create manually)", idxName))
			continue
		}

		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = nc.convertColumnName(t.Name, c.Name)
			if c.Descending {
				cols[i] += " DESC"
			}
		}

		unique := ""
		if idx.Unique {
			unique = "UNIQUE "
		}

		// BITMAP indexes: convert to B-tree with warning comment
		var prefix string
		if idx.Type == "BITMAP" {
			prefix = "-- NOTE: Oracle BITMAP index converted to B-tree (PostgreSQL has no bitmap index type)\n"
		}

		ddl := fmt.Sprintf("%sCREATE %sINDEX %s ON %s (%s);",
			prefix, unique, idxName, tableName, strings.Join(cols, ", "))
		ddls = append(ddls, ddl)
	}
	return ddls
}

// GenerateConstraints generates ALTER TABLE for FK, UNIQUE, and CHECK constraints.
func GenerateConstraints(t schema.Table, pgSchema string) []string {
	return GenerateConstraintsWithNaming(t, pgSchema, DefaultNaming())
}

// GenerateConstraintsWithNaming generates constraint DDL with custom naming.
func GenerateConstraintsWithNaming(t schema.Table, pgSchema string, nc NamingConfig) []string {
	tableName := qualifiedName(pgSchema, nc.convertTableName(t.Name))
	var ddls []string

	for _, c := range t.Constraints {
		cName := quoteIdent(nc.convertName(c.Name))
		switch c.Type {
		case schema.ConstraintFK:
			refTable := qualifiedName(pgSchema, nc.convertTableName(c.RefTable))
			refCols := nc.convertColumnsJoin(c.RefTable, c.RefColumns)
			ddl := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
				tableName, cName, nc.convertColumnsJoin(t.Name, c.Columns), refTable, refCols)
			if c.OnDelete != "" {
				ddl += " ON DELETE " + c.OnDelete
			}
			ddl += ";"
			ddls = append(ddls, ddl)

		case schema.ConstraintUniq:
			ddl := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s);",
				tableName, cName, nc.convertColumnsJoin(t.Name, c.Columns))
			ddls = append(ddls, ddl)

		case schema.ConstraintCheck:
			if c.Condition != "" {
				ddl := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);",
					tableName, cName, c.Condition)
				ddls = append(ddls, ddl)
			}
		}
	}
	return ddls
}

// GenerateSequence generates CREATE SEQUENCE and setval.
func GenerateSequence(seq schema.Sequence, pgSchema string) string {
	return GenerateSequenceWithNaming(seq, pgSchema, DefaultNaming())
}

// GenerateSequenceWithNaming generates sequence DDL with custom naming.
func GenerateSequenceWithNaming(seq schema.Sequence, pgSchema string, nc NamingConfig) string {
	seqName := qualifiedName(pgSchema, nc.convertName(seq.Name))
	// For setval, we need unquoted name as string literal
	seqNameLiteral := strings.ReplaceAll(seqName, `"`, "")
	var b strings.Builder
	b.WriteString(fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s", seqName))
	b.WriteString(fmt.Sprintf(" INCREMENT BY %d", seq.Increment))
	b.WriteString(fmt.Sprintf(" MINVALUE %d", seq.MinValue))
	if seq.MaxValue > 0 && seq.MaxValue < 9223372036854775806 {
		b.WriteString(fmt.Sprintf(" MAXVALUE %d", seq.MaxValue))
	} else {
		b.WriteString(" NO MAXVALUE")
	}
	if seq.CacheSize > 0 {
		b.WriteString(fmt.Sprintf(" CACHE %d", seq.CacheSize))
	}
	if seq.Cycle {
		b.WriteString(" CYCLE")
	}
	b.WriteString(";\n")
	if seq.LastValue > 0 {
		b.WriteString(fmt.Sprintf("SELECT setval('%s', %d);", seqNameLiteral, seq.LastValue))
	}
	return b.String()
}

// GenerateDropTable generates DROP TABLE IF EXISTS CASCADE.
func GenerateDropTable(tableName, pgSchema string) string {
	return GenerateDropTableWithNaming(tableName, pgSchema, DefaultNaming())
}

// GenerateDropTableWithNaming generates DROP TABLE DDL with custom naming.
func GenerateDropTableWithNaming(tableName, pgSchema string, nc NamingConfig) string {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE;",
		qualifiedName(pgSchema, nc.convertTableName(tableName)))
}

func qualifiedName(pgSchema, name string) string {
	quoted := quoteIdent(name)
	if pgSchema == "" || pgSchema == "public" {
		return quoted
	}
	return quoteIdent(pgSchema) + "." + quoted
}

// quoteIdent wraps a PostgreSQL identifier in double quotes.
// This ensures reserved words (ORDER, USER, GROUP, etc.) and
// mixed-case names work correctly.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// convertColumnsJoin converts and joins column names.
func (nc NamingConfig) convertColumnsJoin(tableName string, cols []string) string {
	result := make([]string, len(cols))
	for i, c := range cols {
		result[i] = nc.convertColumnName(tableName, c)
	}
	return strings.Join(result, ", ")
}

