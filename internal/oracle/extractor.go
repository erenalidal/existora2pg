package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/erenalidal/existora2pg/internal/schema"
)

// Extractor reads Oracle schema metadata from system views.
type Extractor struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewExtractor creates a new metadata extractor.
func NewExtractor(db *sql.DB, logger *slog.Logger) *Extractor {
	return &Extractor{db: db, logger: logger}
}

// TableFilter controls which tables and sequences are included in extraction.
type TableFilter struct {
	Include          []string // glob patterns (matched against table name)
	Exclude          []string
	IncludeSequences []string // explicit sequence names (empty = all)
}

// Extract reads the full schema metadata from Oracle.
func (e *Extractor) Extract(ctx context.Context, owner string, filter TableFilter) (*schema.Schema, error) {
	owner = strings.ToUpper(owner)
	e.logger.Info("extracting schema metadata", "owner", owner)

	s := &schema.Schema{Owner: owner}

	tables, err := e.extractTables(ctx, owner, filter)
	if err != nil {
		return nil, fmt.Errorf("extract tables: %w", err)
	}
	e.logger.Info("tables extracted", "count", len(tables))

	// Fetch table stats (row count + size) in bulk
	if err := e.extractTableStats(ctx, owner, tables); err != nil {
		e.logger.Warn("failed to extract table stats, continuing without", "error", err)
	}

	for i := range tables {
		t := &tables[i]
		e.logger.Debug("extracting table metadata", "table", t.Name)

		if err := e.extractColumns(ctx, owner, t); err != nil {
			return nil, fmt.Errorf("extract columns for %s: %w", t.Name, err)
		}

		if err := e.extractConstraints(ctx, owner, t); err != nil {
			return nil, fmt.Errorf("extract constraints for %s: %w", t.Name, err)
		}

		if err := e.extractIndexes(ctx, owner, t); err != nil {
			return nil, fmt.Errorf("extract indexes for %s: %w", t.Name, err)
		}

		if err := e.extractPartitions(ctx, owner, t); err != nil {
			return nil, fmt.Errorf("extract partitions for %s: %w", t.Name, err)
		}
	}

	sequences, err := e.extractSequences(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("extract sequences: %w", err)
	}

	// Filter sequences if include list is specified
	if len(filter.IncludeSequences) > 0 {
		filtered := make([]schema.Sequence, 0)
		includeSet := make(map[string]bool, len(filter.IncludeSequences))
		for _, name := range filter.IncludeSequences {
			includeSet[strings.ToUpper(name)] = true
		}
		for _, seq := range sequences {
			if includeSet[strings.ToUpper(seq.Name)] {
				filtered = append(filtered, seq)
			}
		}
		sequences = filtered
	}
	e.logger.Info("sequences extracted", "count", len(sequences))

	s.Tables = tables
	s.Sequences = sequences

	// Extract non-migratable objects (shown as "manual review required")
	if views, err := e.extractViews(ctx, owner); err != nil {
		e.logger.Warn("failed to extract views", "error", err)
	} else {
		s.Views = views
		e.logger.Info("views extracted", "count", len(views))
	}

	if mviews, err := e.extractMaterializedViews(ctx, owner); err != nil {
		e.logger.Warn("failed to extract materialized views", "error", err)
	} else {
		s.MaterializedViews = mviews
		e.logger.Info("materialized views extracted", "count", len(mviews))
	}

	if triggers, err := e.extractTriggers(ctx, owner); err != nil {
		e.logger.Warn("failed to extract triggers", "error", err)
	} else {
		s.Triggers = triggers
		e.logger.Info("triggers extracted", "count", len(triggers))
	}

	if procs, err := e.extractProcedures(ctx, owner); err != nil {
		e.logger.Warn("failed to extract procedures/functions", "error", err)
	} else {
		s.Procedures = procs
		e.logger.Info("procedures/functions extracted", "count", len(procs))
	}

	schema.ApplyTypeMappings(s)

	return s, nil
}

func (e *Extractor) extractTables(ctx context.Context, owner string, filter TableFilter) ([]schema.Table, error) {
	query := `SELECT table_name FROM all_tables WHERE owner = :1 AND temporary = 'N'
		AND table_name NOT IN (SELECT mview_name FROM all_mviews WHERE owner = :2)
		ORDER BY table_name`
	rows, err := e.db.QueryContext(ctx, query, owner, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []schema.Table
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !matchFilter(name, filter) {
			continue
		}
		tables = append(tables, schema.Table{Owner: owner, Name: name})
	}
	return tables, rows.Err()
}

func (e *Extractor) extractColumns(ctx context.Context, owner string, t *schema.Table) error {
	query := `SELECT column_name, data_type, data_precision, data_scale, data_length,
	                 CASE WHEN nullable = 'Y' THEN 1 ELSE 0 END, data_default, column_id
	          FROM all_tab_columns
	          WHERE owner = :1 AND table_name = :2
	          ORDER BY column_id`

	rows, err := e.db.QueryContext(ctx, query, owner, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var col schema.Column
		var precision, scale, length sql.NullInt64
		var nullable int
		var defaultExpr sql.NullString

		if err := rows.Scan(&col.Name, &col.OracleType, &precision, &scale, &length,
			&nullable, &defaultExpr, &col.Position); err != nil {
			return err
		}

		if precision.Valid {
			p := int(precision.Int64)
			col.Precision = &p
		}
		if scale.Valid {
			s := int(scale.Int64)
			col.Scale = &s
		}
		if length.Valid {
			l := int(length.Int64)
			col.Length = &l
		}
		col.Nullable = nullable == 1
		if defaultExpr.Valid {
			trimmed := strings.TrimSpace(defaultExpr.String)
			if trimmed != "" {
				converted := convertDefaultExpr(trimmed)
				col.DefaultExpr = &converted
			}
		}

		t.Columns = append(t.Columns, col)
	}
	return rows.Err()
}

func (e *Extractor) extractConstraints(ctx context.Context, owner string, t *schema.Table) error {
	// Get constraints
	query := `SELECT c.constraint_name, c.constraint_type, c.search_condition,
	                 c.r_constraint_name, c.delete_rule
	          FROM all_constraints c
	          WHERE c.owner = :1 AND c.table_name = :2
	            AND c.constraint_type IN ('P', 'R', 'U', 'C')
	            AND c.status = 'ENABLED'
	          ORDER BY c.constraint_type, c.constraint_name`

	rows, err := e.db.QueryContext(ctx, query, owner, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rawConstraint struct {
		Name        string
		Type        string
		Condition   sql.NullString
		RConstraint sql.NullString
		DeleteRule  sql.NullString
	}

	var raws []rawConstraint
	for rows.Next() {
		var rc rawConstraint
		if err := rows.Scan(&rc.Name, &rc.Type, &rc.Condition, &rc.RConstraint, &rc.DeleteRule); err != nil {
			return err
		}
		raws = append(raws, rc)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, rc := range raws {
		cols, err := e.getConstraintColumns(ctx, owner, rc.Name)
		if err != nil {
			return err
		}

		c := schema.Constraint{
			Name:    rc.Name,
			Type:    schema.ConstraintType(rc.Type),
			Columns: cols,
		}

		if rc.Condition.Valid {
			c.Condition = strings.TrimSpace(rc.Condition.String)
		}

		// Skip system-generated NOT NULL check constraints
		if c.Type == schema.ConstraintCheck && isNotNullCheck(c.Condition) {
			continue
		}

		if rc.DeleteRule.Valid {
			c.OnDelete = rc.DeleteRule.String
		}

		// Resolve FK reference
		if c.Type == schema.ConstraintFK && rc.RConstraint.Valid {
			refTable, refCols, err := e.resolveRefConstraint(ctx, owner, rc.RConstraint.String)
			if err != nil {
				return fmt.Errorf("resolve FK %s: %w", rc.Name, err)
			}
			c.RefTable = refTable
			c.RefColumns = refCols
		}

		if c.Type == schema.ConstraintPK {
			t.PrimaryKey = &c
		} else {
			t.Constraints = append(t.Constraints, c)
		}
	}

	return nil
}

func (e *Extractor) getConstraintColumns(ctx context.Context, owner, constraintName string) ([]string, error) {
	query := `SELECT column_name FROM all_cons_columns
	          WHERE owner = :1 AND constraint_name = :2
	          ORDER BY position`
	rows, err := e.db.QueryContext(ctx, query, owner, constraintName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

func (e *Extractor) resolveRefConstraint(ctx context.Context, owner, rConstraintName string) (string, []string, error) {
	// Get the referenced table
	var refTable string
	err := e.db.QueryRowContext(ctx,
		`SELECT table_name FROM all_constraints WHERE owner = :1 AND constraint_name = :2`,
		owner, rConstraintName).Scan(&refTable)
	if err != nil {
		return "", nil, err
	}

	refCols, err := e.getConstraintColumns(ctx, owner, rConstraintName)
	if err != nil {
		return "", nil, err
	}

	return refTable, refCols, nil
}

func (e *Extractor) extractIndexes(ctx context.Context, owner string, t *schema.Table) error {
	query := `SELECT i.index_name, i.uniqueness, i.index_type,
	                 CASE WHEN i.partitioned = 'YES' THEN 1 ELSE 0 END
	          FROM all_indexes i
	          WHERE i.table_owner = :1 AND i.table_name = :2
	            AND i.index_type IN ('NORMAL', 'NORMAL/REV', 'BITMAP', 'FUNCTION-BASED NORMAL')
	          ORDER BY i.index_name`

	rows, err := e.db.QueryContext(ctx, query, owner, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Collect constraint index names to skip (PK and UNIQUE indexes are created via constraints)
	constraintIndexes := make(map[string]bool)
	if t.PrimaryKey != nil {
		constraintIndexes[t.PrimaryKey.Name] = true
	}
	for _, c := range t.Constraints {
		if c.Type == schema.ConstraintUniq {
			constraintIndexes[c.Name] = true
		}
	}

	for rows.Next() {
		var idx schema.Index
		var uniqueness string
		var local int

		if err := rows.Scan(&idx.Name, &uniqueness, &idx.Type, &local); err != nil {
			return err
		}

		// Skip indexes that back constraints
		if constraintIndexes[idx.Name] {
			continue
		}

		idx.Unique = uniqueness == "UNIQUE"
		idx.Local = local == 1

		// Get index columns
		colRows, err := e.db.QueryContext(ctx,
			`SELECT column_name, descend, column_position
			 FROM all_ind_columns
			 WHERE index_owner = :1 AND index_name = :2
			 ORDER BY column_position`, owner, idx.Name)
		if err != nil {
			return err
		}

		for colRows.Next() {
			var ic schema.IndexColumn
			var descend string
			if err := colRows.Scan(&ic.Name, &descend, &ic.Position); err != nil {
				colRows.Close()
				return err
			}
			ic.Descending = descend == "DESC"
			idx.Columns = append(idx.Columns, ic)
		}
		colRows.Close()

		if len(idx.Columns) > 0 {
			t.Indexes = append(t.Indexes, idx)
		}
	}
	return rows.Err()
}

func (e *Extractor) extractPartitions(ctx context.Context, owner string, t *schema.Table) error {
	// Check if table is partitioned
	var partType sql.NullString
	err := e.db.QueryRowContext(ctx,
		`SELECT partitioning_type FROM all_part_tables WHERE owner = :1 AND table_name = :2`,
		owner, t.Name).Scan(&partType)
	if err == sql.ErrNoRows || !partType.Valid {
		return nil // not partitioned
	}
	if err != nil {
		return err
	}

	// Get partition key columns
	keyCols, err := e.getPartitionKeyColumns(ctx, owner, t.Name)
	if err != nil {
		return err
	}

	// Get partitions with row count stats
	partRows, err := e.db.QueryContext(ctx,
		`SELECT partition_name, high_value, partition_position, NVL(num_rows, 0)
		 FROM all_tab_partitions
		 WHERE table_owner = :1 AND table_name = :2
		 ORDER BY partition_position`, owner, t.Name)
	if err != nil {
		return err
	}
	defer partRows.Close()

	var partitions []schema.Partition
	for partRows.Next() {
		var p schema.Partition
		var highValue sql.NullString
		if err := partRows.Scan(&p.Name, &highValue, &p.Position, &p.NumRows); err != nil {
			return err
		}
		if highValue.Valid {
			p.HighValue = highValue.String
		}
		partitions = append(partitions, p)
	}

	// Try to get partition sizes from ALL_SEGMENTS (may fail if no access)
	if len(partitions) > 0 {
		sizeRows, err := e.db.QueryContext(ctx,
			`SELECT partition_name, SUM(bytes)/1048576
			 FROM all_segments
			 WHERE owner = :1 AND segment_name = :2 AND segment_type = 'TABLE PARTITION'
			 GROUP BY partition_name`, owner, t.Name)
		if err == nil {
			defer sizeRows.Close()
			sizeMap := make(map[string]float64)
			for sizeRows.Next() {
				var name string
				var sizeMB float64
				if err := sizeRows.Scan(&name, &sizeMB); err == nil {
					sizeMap[name] = sizeMB
				}
			}
			for i := range partitions {
				if s, ok := sizeMap[partitions[i].Name]; ok {
					partitions[i].SizeMB = s
				}
			}
		}
	}
	if err := partRows.Err(); err != nil {
		return err
	}

	if len(partitions) > 0 {
		strategy := schema.PartitionRange
		switch strings.ToUpper(partType.String) {
		case "LIST":
			strategy = schema.PartitionList
		case "HASH":
			strategy = schema.PartitionHash
		}

		t.Partitioning = &schema.PartitionInfo{
			Strategy:   strategy,
			KeyColumns: keyCols,
			Partitions: partitions,
		}

		e.logger.Debug("partitions found", "table", t.Name, "count", len(partitions), "strategy", strategy)

		// If table-level NumRows is 0 but partitions have stats, sum them up
		if t.NumRows == 0 {
			var sum int64
			for _, p := range partitions {
				sum += p.NumRows
			}
			if sum > 0 {
				t.NumRows = sum
			}
		}
		// Same for SizeMB
		if t.SizeMB == 0 {
			var sum float64
			for _, p := range partitions {
				sum += p.SizeMB
			}
			if sum > 0 {
				t.SizeMB = sum
			}
		}
	}

	return nil
}

func (e *Extractor) getPartitionKeyColumns(ctx context.Context, owner, tableName string) ([]string, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT column_name FROM all_part_key_columns
		 WHERE owner = :1 AND name = :2
		 ORDER BY column_position`, owner, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

func (e *Extractor) extractSequences(ctx context.Context, owner string) ([]schema.Sequence, error) {
	query := `SELECT sequence_name,
	                 CASE WHEN min_value < -9223372036854775807 THEN -9223372036854775807 ELSE min_value END,
	                 CASE WHEN max_value > 9223372036854775807 THEN 9223372036854775807 ELSE max_value END,
	                 increment_by, last_number, cache_size,
	                 CASE WHEN cycle_flag = 'Y' THEN 1 ELSE 0 END
	          FROM all_sequences
	          WHERE sequence_owner = :1
	          ORDER BY sequence_name`

	rows, err := e.db.QueryContext(ctx, query, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seqs []schema.Sequence
	for rows.Next() {
		var s schema.Sequence
		var cycle int
		if err := rows.Scan(&s.Name, &s.MinValue, &s.MaxValue, &s.Increment,
			&s.LastValue, &s.CacheSize, &cycle); err != nil {
			return nil, err
		}
		s.Cycle = cycle == 1
		seqs = append(seqs, s)
	}
	return seqs, rows.Err()
}

// extractTableStats fetches estimated row counts and sizes for all tables in bulk.
func (e *Extractor) extractTableStats(ctx context.Context, owner string, tables []schema.Table) error {
	// Build a lookup map
	tableMap := make(map[string]int, len(tables))
	for i, t := range tables {
		tableMap[t.Name] = i
	}

	// Get row counts from ALL_TABLES (uses DBMS_STATS estimates)
	rows, err := e.db.QueryContext(ctx,
		`SELECT table_name, NVL(num_rows, 0)
		 FROM all_tables
		 WHERE owner = :1`, owner)
	if err != nil {
		return fmt.Errorf("query num_rows: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var numRows int64
		if err := rows.Scan(&name, &numRows); err != nil {
			return err
		}
		if idx, ok := tableMap[name]; ok {
			tables[idx].NumRows = numRows
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Get sizes from ALL_SEGMENTS (sum of all segments for each table)
	sizeRows, err := e.db.QueryContext(ctx,
		`SELECT segment_name, SUM(bytes) / 1048576
		 FROM all_segments
		 WHERE owner = :1 AND segment_type LIKE 'TABLE%'
		 GROUP BY segment_name`, owner)
	if err != nil {
		// Not critical — some users may not have access to ALL_SEGMENTS
		e.logger.Debug("could not query segment sizes", "error", err)
		return nil
	}
	defer sizeRows.Close()

	for sizeRows.Next() {
		var name string
		var sizeMB float64
		if err := sizeRows.Scan(&name, &sizeMB); err != nil {
			return err
		}
		if idx, ok := tableMap[name]; ok {
			tables[idx].SizeMB = sizeMB
		}
	}
	return sizeRows.Err()
}

// matchFilter checks if a table name passes the include/exclude filter.
func matchFilter(name string, filter TableFilter) bool {
	name = strings.ToUpper(name)

	// If include list is specified, table must match at least one pattern
	if len(filter.Include) > 0 {
		matched := false
		for _, pattern := range filter.Include {
			if ok, _ := filepath.Match(strings.ToUpper(pattern), name); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check exclude list
	for _, pattern := range filter.Exclude {
		if ok, _ := filepath.Match(strings.ToUpper(pattern), name); ok {
			return false
		}
	}

	return true
}

// convertDefaultExpr converts Oracle DEFAULT expressions to PostgreSQL equivalents.
func convertDefaultExpr(expr string) string {
	upper := strings.ToUpper(strings.TrimSpace(expr))

	switch upper {
	case "SYSDATE":
		return "CURRENT_DATE"
	case "SYSTIMESTAMP":
		return "CURRENT_TIMESTAMP"
	case "SYS_GUID()":
		return "gen_random_uuid()"
	case "USER":
		return "CURRENT_USER"
	case "NULL":
		return "NULL"
	}

	// Handle SYSDATE +/- N (e.g., "SYSDATE + 30")
	if strings.HasPrefix(upper, "SYSDATE") && len(upper) > 7 {
		rest := strings.TrimSpace(upper[7:])
		if rest != "" && (rest[0] == '+' || rest[0] == '-') {
			return "CURRENT_DATE " + rest
		}
	}

	// Handle TO_DATE(...) → date literal
	if strings.HasPrefix(upper, "TO_DATE(") {
		// Extract the date value from TO_DATE('value', 'format')
		start := strings.Index(expr, "'")
		if start >= 0 {
			end := strings.Index(expr[start+1:], "'")
			if end >= 0 {
				dateVal := strings.TrimSpace(expr[start+1 : start+1+end])
				return "'" + dateVal + "'"
			}
		}
	}

	// Handle TO_CHAR(...) → cast or remove
	if strings.HasPrefix(upper, "TO_CHAR(SYSDATE") {
		return "CURRENT_DATE::text"
	}

	// Return as-is for numeric literals, string literals, etc.
	return expr
}

// isNotNullCheck returns true if a CHECK condition is just a NOT NULL check
// (Oracle creates these automatically, we skip them).
func isNotNullCheck(condition string) bool {
	c := strings.ToUpper(strings.TrimSpace(condition))
	return strings.Contains(c, "IS NOT NULL")
}

// --- Non-migratable objects (extract for manual review) ---

func (e *Extractor) extractViews(ctx context.Context, owner string) ([]schema.View, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT view_name, text FROM all_views WHERE owner = :1 ORDER BY view_name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var views []schema.View
	for rows.Next() {
		var v schema.View
		v.Owner = owner
		var text sql.NullString
		if err := rows.Scan(&v.Name, &text); err != nil {
			return nil, err
		}
		if text.Valid {
			v.Definition = text.String
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

func (e *Extractor) extractMaterializedViews(ctx context.Context, owner string) ([]schema.MaterializedView, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT mview_name, query, refresh_mode FROM all_mviews WHERE owner = :1 ORDER BY mview_name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mviews []schema.MaterializedView
	for rows.Next() {
		var mv schema.MaterializedView
		mv.Owner = owner
		var query, refreshMode sql.NullString
		if err := rows.Scan(&mv.Name, &query, &refreshMode); err != nil {
			return nil, err
		}
		if query.Valid {
			mv.Definition = query.String
		}
		if refreshMode.Valid {
			mv.RefreshMode = refreshMode.String
		}
		mviews = append(mviews, mv)
	}
	return mviews, rows.Err()
}

func (e *Extractor) extractTriggers(ctx context.Context, owner string) ([]schema.Trigger, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT trigger_name, NVL(table_name, ''), trigger_type, triggering_event, trigger_body
		 FROM all_triggers WHERE owner = :1 ORDER BY trigger_name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var triggers []schema.Trigger
	for rows.Next() {
		var t schema.Trigger
		t.Owner = owner
		var body sql.NullString
		if err := rows.Scan(&t.Name, &t.TableName, &t.TriggerType, &t.Event, &body); err != nil {
			return nil, err
		}
		if body.Valid {
			t.Definition = body.String
		}
		triggers = append(triggers, t)
	}
	return triggers, rows.Err()
}

func (e *Extractor) extractProcedures(ctx context.Context, owner string) ([]schema.Procedure, error) {
	// Get distinct object names of type PROCEDURE, FUNCTION, PACKAGE, PACKAGE BODY
	rows, err := e.db.QueryContext(ctx,
		`SELECT object_name, object_type FROM all_objects
		 WHERE owner = :1 AND object_type IN ('PROCEDURE', 'FUNCTION', 'PACKAGE', 'PACKAGE BODY')
		 AND object_name NOT LIKE 'SYS_%' AND object_name NOT LIKE 'BIN$%'
		 ORDER BY object_type, object_name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type objRef struct {
		name     string
		objType  string
	}
	var refs []objRef
	for rows.Next() {
		var r objRef
		if err := rows.Scan(&r.name, &r.objType); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// For each object, get its source from ALL_SOURCE
	var procs []schema.Procedure
	for _, ref := range refs {
		srcRows, err := e.db.QueryContext(ctx,
			`SELECT text FROM all_source WHERE owner = :1 AND name = :2 AND type = :3 ORDER BY line`,
			owner, ref.name, ref.objType)
		if err != nil {
			e.logger.Warn("failed to get source", "name", ref.name, "type", ref.objType, "error", err)
			continue
		}

		var sb strings.Builder
		for srcRows.Next() {
			var line string
			if err := srcRows.Scan(&line); err != nil {
				break
			}
			sb.WriteString(line)
		}
		srcRows.Close()

		procs = append(procs, schema.Procedure{
			Owner:      owner,
			Name:       ref.name,
			ObjectType: ref.objType,
			Definition: sb.String(),
		})
	}
	return procs, nil
}
