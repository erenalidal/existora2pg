// Package plsql provides regex-based Oracle PL/SQL → PostgreSQL PL/pgSQL conversion.
// Handles common patterns; complex constructs are flagged with TODO comments.
package plsql

import (
	"fmt"
	"regexp"
	"strings"
)

// ConvertResult holds the converted definition and any warnings.
type ConvertResult struct {
	Definition string   // converted PL/pgSQL (or best-effort)
	Warnings   []string // patterns that couldn't be auto-converted
}

// NamingConvention controls identifier case: "lowercase" (default), "uppercase", "keep_original".
type NamingConvention string

const (
	NamingLowercase    NamingConvention = "lowercase"
	NamingUppercase    NamingConvention = "uppercase"
	NamingKeepOriginal NamingConvention = "keep_original"
)

// ConvertView converts an Oracle view definition to PostgreSQL.
func ConvertView(definition string, naming NamingConvention) ConvertResult {
	r := ConvertResult{Definition: definition}
	r.Definition = convertExpressions(r.Definition, &r.Warnings)
	r.Definition = convertROWNUM(r.Definition, &r.Warnings)
	addSQLWarnings(r.Definition, &r.Warnings)
	r.Definition = applyNaming(r.Definition, naming)
	return r
}

// ConvertMaterializedView converts an Oracle MView query to PostgreSQL.
func ConvertMaterializedView(definition string, naming NamingConvention) ConvertResult {
	r := ConvertResult{Definition: definition}
	r.Definition = convertExpressions(r.Definition, &r.Warnings)
	r.Definition = convertROWNUM(r.Definition, &r.Warnings)
	addSQLWarnings(r.Definition, &r.Warnings)
	r.Definition = applyNaming(r.Definition, naming)
	return r
}

// addSQLWarnings adds warnings for Oracle-specific SQL constructs that cannot be auto-converted.
func addSQLWarnings(s string, warnings *[]string) {
	if reConnectBy.MatchString(s) {
		*warnings = append(*warnings, "CONNECT BY → Use recursive CTE (WITH RECURSIVE)")
	}
}

// ConvertTrigger converts an Oracle trigger body to PL/pgSQL trigger function style.
func ConvertTrigger(name, tableName, triggerType, event, definition string, naming NamingConvention) ConvertResult {
	r := ConvertResult{Definition: definition}

	body := r.Definition

	// :NEW.x → NEW.x, :OLD.x → OLD.x
	body = reNewOld.ReplaceAllStringFunc(body, func(s string) string {
		return strings.TrimPrefix(s, ":")
	})

	// Convert expressions within trigger body
	body = convertExpressions(body, &r.Warnings)

	// RAISE_APPLICATION_ERROR
	body = convertRaiseAppError(body)

	// DBMS_OUTPUT.PUT_LINE
	body = convertDbmsOutput(body)

	// seq.NEXTVAL / seq.CURRVAL
	body = convertSequenceRefs(body)

	// INSERTING/UPDATING/DELETING → TG_OP checks
	body = reInserting.ReplaceAllString(body, "TG_OP = 'INSERT'")
	body = reUpdating.ReplaceAllString(body, "TG_OP = 'UPDATE'")
	body = reDeleting.ReplaceAllString(body, "TG_OP = 'DELETE'")

	// SQL%ROWCOUNT → ROW_COUNT variable
	if reSQLRowcount.MatchString(body) {
		body = reSQLRowcount.ReplaceAllString(body, "_row_count")
		// Prepend GET DIAGNOSTICS if not already there
		if !strings.Contains(body, "GET DIAGNOSTICS") {
			body = strings.Replace(body, "BEGIN\n", "BEGIN\n", 1)
			r.Warnings = append(r.Warnings, "SQL%ROWCOUNT → Add 'GET DIAGNOSTICS _row_count = ROW_COUNT;' after DML statements")
		}
	}

	// Build PL/pgSQL trigger function
	pgTriggerType := strings.Replace(strings.ToUpper(triggerType), "EACH ROW ", "", 1)
	pgEvent := strings.ToUpper(event)

	// Extract DECLARE section and body
	declareSection, bodySection := splitDeclareBody(body)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("-- Converted from Oracle trigger: %s\n", strings.ToUpper(name)))
	sb.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION fn_%s()\n", strings.ToLower(name)))
	sb.WriteString("RETURNS TRIGGER\nLANGUAGE plpgsql\nAS $$\n")
	if declareSection != "" {
		sb.WriteString("DECLARE\n")
		sb.WriteString(declareSection)
		sb.WriteString("\n")
	}
	sb.WriteString("BEGIN\n")
	sb.WriteString(bodySection)
	// DELETE-only triggers have no NEW, use RETURN OLD; AFTER triggers can use RETURN NULL
	trigTypeUpper := strings.ToUpper(triggerType)
	if strings.Contains(trigTypeUpper, "AFTER") {
		sb.WriteString("\n  RETURN NULL;\n")
	} else if strings.EqualFold(event, "DELETE") {
		sb.WriteString("\n  RETURN OLD;\n")
	} else {
		sb.WriteString("\n  RETURN NEW;\n")
	}
	sb.WriteString("END;\n")
	sb.WriteString("$$;\n\n")
	sb.WriteString(fmt.Sprintf("CREATE TRIGGER %s\n", strings.ToLower(name)))
	sb.WriteString(fmt.Sprintf("  %s %s ON %s\n", pgTriggerType, pgEvent, strings.ToLower(tableName)))
	sb.WriteString("  FOR EACH ROW\n")
	sb.WriteString(fmt.Sprintf("  EXECUTE FUNCTION fn_%s();\n", strings.ToLower(name)))

	r.Definition = applyNaming(sb.String(), naming)
	return r
}

// ConvertProcedure converts an Oracle procedure/function/package to PL/pgSQL.
func ConvertProcedure(name, objectType, definition string, naming NamingConvention) ConvertResult {
	r := ConvertResult{Definition: definition}

	body := r.Definition

	// Common expression conversions
	body = convertExpressions(body, &r.Warnings)
	body = convertRaiseAppError(body)
	body = convertDbmsOutput(body)
	body = convertSequenceRefs(body)

	// PRAGMA removals (must be before other processing)
	body = rePragmaExcInit.ReplaceAllString(body, "")
	body = rePragmaRestrictRef.ReplaceAllString(body, "")
	if rePragmaAutonomous.MatchString(body) {
		body = rePragmaAutonomous.ReplaceAllString(body, "")
		r.Warnings = append(r.Warnings, "PRAGMA AUTONOMOUS_TRANSACTION → PostgreSQL has no autonomous transactions. Use dblink or pg_background for similar effect")
	}

	// NOCOPY → remove (PG passes composite types by reference automatically)
	body = reNocopy.ReplaceAllString(body, "")

	// DETERMINISTIC → will add IMMUTABLE to function header
	isDeterministic := reDeterministic.MatchString(body)
	body = reDeterministic.ReplaceAllString(body, "")

	// RESULT_CACHE → remove + warning
	if reResultCache.MatchString(body) {
		body = reResultCache.ReplaceAllString(body, "")
		r.Warnings = append(r.Warnings, "RESULT_CACHE → PostgreSQL has no built-in result cache. Consider materialized views or application-level caching")
	}

	// AUTHID CURRENT_USER → SECURITY INVOKER
	body = reAuthidCurrentUser.ReplaceAllString(body, "SECURITY INVOKER")
	// AUTHID DEFINER → SECURITY DEFINER
	body = reAuthidDefiner.ReplaceAllString(body, "SECURITY DEFINER")

	// SQL%ROWCOUNT
	if reSQLRowcount.MatchString(body) {
		body = reSQLRowcount.ReplaceAllString(body, "_row_count")
		r.Warnings = append(r.Warnings, "SQL%ROWCOUNT replaced with _row_count — add 'GET DIAGNOSTICS _row_count = ROW_COUNT;' after DML")
	}

	// Oracle exception names → PostgreSQL
	body = reNoDataFound.ReplaceAllString(body, "NO_DATA_FOUND") // PG has this too
	body = reDupValOnIndex.ReplaceAllString(body, "unique_violation")
	body = reZeroDivide.ReplaceAllString(body, "division_by_zero")
	body = reTooManyRows.ReplaceAllString(body, "too_many_rows")
	body = reValueError.ReplaceAllString(body, "data_exception")
	body = reInvalidNumber.ReplaceAllString(body, "invalid_text_representation")
	body = reLoginDenied.ReplaceAllString(body, "insufficient_privilege")
	body = reCursorAlreadyOpen.ReplaceAllString(body, "duplicate_cursor")
	body = reInvalidCursor.ReplaceAllString(body, "invalid_cursor_state")

	// Cursor attributes → PG equivalents
	body = reCursorNotFound.ReplaceAllString(body, "NOT FOUND")
	body = reCursorFound.ReplaceAllString(body, "FOUND")
	body = reSQLNotFound.ReplaceAllString(body, "NOT FOUND")
	body = reSQLFound.ReplaceAllString(body, "FOUND")

	// CURSOR cur IS SELECT → cur CURSOR FOR SELECT
	body = reCursorDecl.ReplaceAllString(body, "$1 CURSOR FOR SELECT")

	// IN OUT → INOUT
	body = reInOut.ReplaceAllString(body, "INOUT")

	// RETURN type syntax: RETURN → RETURNS (in function header)
	if strings.EqualFold(objectType, "FUNCTION") {
		body = reReturnType.ReplaceAllString(body, "${1}RETURNS${2}")
	}

	// IN/OUT parameter modes — mostly compatible, but remove defaults like 'IN'
	// Oracle: p_x IN NUMBER → PG: p_x NUMERIC
	body = convertParamTypes(body)

	// Oracle types in variable declarations
	body = convertOracleTypes(body)

	// PACKAGE / PACKAGE BODY → warning (can't auto-convert)
	upperType := strings.ToUpper(objectType)
	if upperType == "PACKAGE" || upperType == "PACKAGE BODY" {
		r.Warnings = append(r.Warnings, "PACKAGE → PostgreSQL has no package concept. Each procedure/function must be created separately")
		// Still do best-effort conversion of the body
		body = rePackageHeader.ReplaceAllString(body, "-- Package: $1 (split into individual functions)")
		body = rePackageBodyHeader.ReplaceAllString(body, "-- Package Body: $1")
	}

	// COMMIT/ROLLBACK inside procedures — PG procedures can use these, functions cannot
	if strings.EqualFold(objectType, "FUNCTION") {
		if reCommit.MatchString(body) {
			r.Warnings = append(r.Warnings, "COMMIT inside FUNCTION → PostgreSQL functions cannot COMMIT. Convert to PROCEDURE or remove")
		}
	}

	// %TYPE / %ROWTYPE references
	if rePercentType.MatchString(body) {
		r.Warnings = append(r.Warnings, "%TYPE/%ROWTYPE → Verify table/column references are correct for PostgreSQL schema")
	}

	// BULK COLLECT
	if reBulkCollect.MatchString(body) {
		r.Warnings = append(r.Warnings, "BULK COLLECT → No direct equivalent. Use array aggregation or process rows in a loop")
	}

	// FORALL
	if reForall.MatchString(body) {
		r.Warnings = append(r.Warnings, "FORALL → No direct equivalent. Use standard FOR loop with individual DML or batch INSERT")
	}

	// CONNECT BY
	if reConnectBy.MatchString(body) {
		r.Warnings = append(r.Warnings, "CONNECT BY → Use recursive CTE (WITH RECURSIVE)")
	}

	// CURSOR variable with SYS_REFCURSOR
	if reRefCursor.MatchString(body) {
		r.Warnings = append(r.Warnings, "SYS_REFCURSOR → Use REFCURSOR type in PostgreSQL")
	}

	// TYPE ... IS TABLE OF / IS RECORD → warning
	if reTypeDecl.MatchString(body) {
		r.Warnings = append(r.Warnings, "TYPE ... IS TABLE OF / IS RECORD → Use PostgreSQL composite types or arrays")
	}

	// OPEN/FETCH/CLOSE cursor → warning
	if reOpenCursor.MatchString(body) || reFetchCursor.MatchString(body) || reCloseCursor.MatchString(body) {
		r.Warnings = append(r.Warnings, "OPEN/FETCH/CLOSE cursor → Consider rewriting as cursor FOR loop (FOR rec IN cursor_name LOOP)")
	}

	// SAVEPOINT → warning
	if reSavepoint.MatchString(body) {
		r.Warnings = append(r.Warnings, "SAVEPOINT → PostgreSQL supports SAVEPOINT in procedures but not in functions")
	}

	// PIPELINED / PIPE ROW → warning
	if rePipelined.MatchString(body) || rePipeRow.MatchString(body) {
		r.Warnings = append(r.Warnings, "PIPELINED/PIPE ROW → Use RETURNS SETOF with RETURN NEXT or RETURN QUERY")
	}

	// UTL_FILE → warning
	if reUtlFile.MatchString(body) {
		r.Warnings = append(r.Warnings, "UTL_FILE → Use pg_read_file/pg_write_file or COPY, or handle file I/O in application layer")
	}

	// DBMS_SQL → warning
	if reDbmsSql.MatchString(body) {
		r.Warnings = append(r.Warnings, "DBMS_SQL → Use EXECUTE with dynamic SQL or format() for query building")
	}

	// DBMS_JOB / DBMS_SCHEDULER → warning
	if reDbmsJob.MatchString(body) {
		r.Warnings = append(r.Warnings, "DBMS_JOB/DBMS_SCHEDULER → Use pg_cron extension or external scheduler")
	}

	// DBMS_CRYPTO → warning
	if reDbmsCrypto.MatchString(body) {
		r.Warnings = append(r.Warnings, "DBMS_CRYPTO → Use pgcrypto extension (encrypt, decrypt, digest, hmac)")
	}

	// ROWID → warning (only in non-type contexts)
	if reRowidRef.MatchString(body) {
		r.Warnings = append(r.Warnings, "ROWID → PostgreSQL uses ctid (physical row ID) but it changes after VACUUM. Consider using a primary key instead")
	}

	// CONSTANT → works in PG but might need type conversion
	// EXCEPTION declaration → works in PG

	// Build volatility/security clause
	var extraClauses []string
	if isDeterministic {
		extraClauses = append(extraClauses, "IMMUTABLE")
	}
	// Check if SECURITY was set from AUTHID conversion
	if strings.Contains(body, "SECURITY INVOKER") {
		body = reSecurityInvoker.ReplaceAllString(body, "")
		extraClauses = append(extraClauses, "SECURITY INVOKER")
	}
	if strings.Contains(body, "SECURITY DEFINER") {
		body = reSecurityDefiner.ReplaceAllString(body, "")
		extraClauses = append(extraClauses, "SECURITY DEFINER")
	}
	extraStr := ""
	if len(extraClauses) > 0 {
		extraStr = "\n" + strings.Join(extraClauses, "\n")
	}

	// Wrap function/procedure header for PG
	if upperType == "PROCEDURE" {
		body = reProcHeader.ReplaceAllStringFunc(body, func(s string) string {
			// Remove trailing AS/IS and add PG-style wrapper
			s = reTrimAsIs.ReplaceAllString(s, "")
			return s + "\nLANGUAGE plpgsql" + extraStr + "\nAS $$"
		})
		if idx := strings.LastIndex(body, "END;"); idx >= 0 {
			body = body[:idx+4] + "\n$$;"
		}
	} else if upperType == "FUNCTION" {
		body = reFuncHeader.ReplaceAllStringFunc(body, func(s string) string {
			s = reTrimAsIs.ReplaceAllString(s, "")
			return s + "\nLANGUAGE plpgsql" + extraStr + "\nAS $$"
		})
		if idx := strings.LastIndex(body, "END;"); idx >= 0 {
			body = body[:idx+4] + "\n$$;"
		}
	}

	r.Definition = applyNaming(body, naming)
	return r
}

// --- Internal conversion helpers ---

func convertExpressions(s string, warnings *[]string) string {
	// NVL2(a, b, c) → CASE WHEN a IS NOT NULL THEN b ELSE c END
	s = convertNVL2(s)

	// NVL(a, b) → COALESCE(a, b)
	s = reNVL.ReplaceAllString(s, "COALESCE(")

	// DECODE → CASE
	s = convertDECODE(s, warnings)

	// SYSDATE → CURRENT_DATE
	s = reSysdate.ReplaceAllString(s, "CURRENT_DATE")

	// SYSTIMESTAMP → CURRENT_TIMESTAMP
	s = reSystimestamp.ReplaceAllString(s, "CURRENT_TIMESTAMP")

	// TRUNC(date_col) → (date_col)::date  (simple case)
	s = reTruncDate.ReplaceAllStringFunc(s, func(m string) string {
		parts := reTruncDate.FindStringSubmatch(m)
		if len(parts) >= 2 {
			inner := strings.TrimSpace(parts[1])
			// Check if it has a second argument (format)
			if strings.Contains(inner, ",") {
				args := splitTopLevel(inner, ',')
				if len(args) == 2 {
					fmt2 := strings.TrimSpace(strings.Trim(args[1], "'\" "))
					switch strings.ToUpper(fmt2) {
					case "MM":
						return "DATE_TRUNC('month', " + strings.TrimSpace(args[0]) + ")"
					case "YY", "YYYY":
						return "DATE_TRUNC('year', " + strings.TrimSpace(args[0]) + ")"
					case "DD":
						return "(" + strings.TrimSpace(args[0]) + ")::date"
					default:
						return "DATE_TRUNC('" + strings.ToLower(fmt2) + "', " + strings.TrimSpace(args[0]) + ")"
					}
				}
			}
			return "(" + inner + ")::date"
		}
		return m
	})

	// ADD_MONTHS(d, n) → d + interval 'n months'
	s = reAddMonths.ReplaceAllStringFunc(s, func(m string) string {
		parts := reAddMonths.FindStringSubmatch(m)
		if len(parts) >= 3 {
			return fmt.Sprintf("(%s + INTERVAL '%s months')", strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]))
		}
		return m
	})

	// TO_NUMBER(x) → (x)::numeric, TO_NUMBER(x, fmt) → (x)::numeric (fmt ignored)
	s = reToNumber.ReplaceAllStringFunc(s, func(m string) string {
		parts := reToNumber.FindStringSubmatch(m)
		if len(parts) >= 2 {
			inner := strings.TrimSpace(parts[1])
			// If has format arg, take only the first arg
			if strings.Contains(inner, ",") {
				args := splitTopLevel(inner, ',')
				return "(" + strings.TrimSpace(args[0]) + ")::NUMERIC"
			}
			return "(" + inner + ")::NUMERIC"
		}
		return m
	})

	// FROM DUAL → remove
	s = reFromDual.ReplaceAllString(s, "")

	// MINUS → EXCEPT
	s = reMinus.ReplaceAllString(s, "EXCEPT")

	// INSTR(str, substr) → position(substr in str)
	s = convertINSTR(s, warnings)

	// LISTAGG(expr, delim) WITHIN GROUP (ORDER BY col) → string_agg(expr, delim ORDER BY col)
	s = convertLISTAGG(s)

	// WM_CONCAT(expr) → string_agg(expr, ',')
	s = reWMConcat.ReplaceAllString(s, "string_agg($1, ',')")

	// LAST_DAY(date) → (date_trunc('month', date) + interval '1 month - 1 day')::date
	s = reLastDay.ReplaceAllString(s, "(DATE_TRUNC('month', $1) + INTERVAL '1 month - 1 day')::DATE")

	// MONTHS_BETWEEN(d1, d2) → (EXTRACT(YEAR FROM age(d1,d2))*12 + EXTRACT(MONTH FROM age(d1,d2)))
	s = reMonthsBetween.ReplaceAllStringFunc(s, func(m string) string {
		parts := reMonthsBetween.FindStringSubmatch(m)
		if len(parts) >= 3 {
			d1 := strings.TrimSpace(parts[1])
			d2 := strings.TrimSpace(parts[2])
			return fmt.Sprintf("(EXTRACT(YEAR FROM age(%s, %s)) * 12 + EXTRACT(MONTH FROM age(%s, %s)))", d1, d2, d1, d2)
		}
		return m
	})

	// SYS_GUID() → gen_random_uuid()
	s = reSysGuid.ReplaceAllString(s, "gen_random_uuid()")

	// EMPTY_CLOB() → ''
	s = reEmptyClob.ReplaceAllString(s, "''")

	// EMPTY_BLOB() → '\x'::bytea
	s = reEmptyBlob.ReplaceAllString(s, "'\\x'::BYTEA")

	// BITAND(a, b) → (a & b)
	s = reBitand.ReplaceAllString(s, "($1 & $2)")

	// LENGTHB(str) → octet_length(str)
	s = reLengthB.ReplaceAllString(s, "octet_length(")

	// REGEXP_LIKE(str, pattern[, flags]) → str ~ pattern or str ~* pattern
	s = convertRegexpLike(s)

	// SYS_CONTEXT conversions
	s = reSysCtxUser.ReplaceAllString(s, "current_user")
	s = reSysCtxSessionUser.ReplaceAllString(s, "session_user")
	s = reSysCtxDbName.ReplaceAllString(s, "current_database()")
	s = reSysCtxIPAddr.ReplaceAllString(s, "inet_client_addr()")
	s = reSysCtxSessionID.ReplaceAllString(s, "pg_backend_pid()")

	// DBMS package functions
	s = reDbmsLobGetLength.ReplaceAllString(s, "octet_length($1)")
	s = reDbmsLobSubstr.ReplaceAllStringFunc(s, func(m string) string {
		parts := reDbmsLobSubstr.FindStringSubmatch(m)
		if len(parts) >= 4 {
			return fmt.Sprintf("substr(%s, %s, %s)", strings.TrimSpace(parts[1]), strings.TrimSpace(parts[3]), strings.TrimSpace(parts[2]))
		}
		return m
	})
	s = reDbmsLockSleep.ReplaceAllString(s, "pg_sleep($1)")

	// EXECUTE IMMEDIATE → EXECUTE
	s = reExecImmediate.ReplaceAllString(s, "EXECUTE ")

	// FROM_TZ(ts, tz) → ts AT TIME ZONE tz
	s = reFromTZ.ReplaceAllString(s, "($1 AT TIME ZONE $2)")

	// NUMTODSINTERVAL(n, 'SECOND'/'MINUTE'/'HOUR'/'DAY') → n * interval '1 unit'
	s = reNumToDSInterval.ReplaceAllStringFunc(s, func(m string) string {
		parts := reNumToDSInterval.FindStringSubmatch(m)
		if len(parts) >= 3 {
			unit := strings.Trim(strings.TrimSpace(parts[2]), "'\"")
			return fmt.Sprintf("(%s * INTERVAL '1 %s')", strings.TrimSpace(parts[1]), strings.ToLower(unit))
		}
		return m
	})

	// TO_DATE format conversion (must come after SYSDATE conversion)
	s = convertToDate(s)

	// TO_CHAR date format element conversion
	s = convertToCharFmt(s)

	// REGEXP_SUBSTR(str, pat) → substring(str FROM pat)
	s = reRegexpSubstr.ReplaceAllStringFunc(s, func(m string) string {
		parts := reRegexpSubstr.FindStringSubmatch(m)
		if len(parts) >= 3 {
			return fmt.Sprintf("substring(%s FROM %s)", strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]))
		}
		return m
	})

	// REGEXP_REPLACE(str, pat, rep) → regexp_replace(str, pat, rep) — same name in PG, compatible
	// REGEXP_COUNT(str, pat) → (SELECT count(*) FROM regexp_matches(str, pat, 'g'))
	s = reRegexpCount.ReplaceAllStringFunc(s, func(m string) string {
		parts := reRegexpCount.FindStringSubmatch(m)
		if len(parts) >= 3 {
			return fmt.Sprintf("(SELECT count(*) FROM regexp_matches(%s, %s, 'g'))", strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]))
		}
		return m
	})

	// NUMTOYMINTERVAL(n, 'YEAR'/'MONTH') → n * interval '1 unit'
	s = reNumToYMInterval.ReplaceAllStringFunc(s, func(m string) string {
		parts := reNumToYMInterval.FindStringSubmatch(m)
		if len(parts) >= 3 {
			unit := strings.Trim(strings.TrimSpace(parts[2]), "'\"")
			return fmt.Sprintf("(%s * INTERVAL '1 %s')", strings.TrimSpace(parts[1]), strings.ToLower(unit))
		}
		return m
	})

	// DBMS_UTILITY.GET_TIME → extract(epoch from clock_timestamp())::bigint * 100
	s = reDbmsUtilGetTime.ReplaceAllString(s, "(extract(epoch FROM clock_timestamp()) * 100)::BIGINT")

	// DBMS_OUTPUT.PUT (without newline) → RAISE NOTICE (closest equivalent)
	s = reDbmsOutputPut.ReplaceAllStringFunc(s, func(m string) string {
		parts := reDbmsOutputPut.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return fmt.Sprintf("RAISE NOTICE '%%', %s", strings.TrimSpace(parts[1]))
		}
		return m
	})

	// (+) outer join syntax → warning
	if reOuterJoinPlus.MatchString(s) {
		*warnings = append(*warnings, "(+) outer join syntax → Rewrite using LEFT/RIGHT JOIN")
	}

	// MERGE INTO → warning
	if reMergeInto.MatchString(s) {
		*warnings = append(*warnings, "MERGE INTO → Rewrite using INSERT ... ON CONFLICT ... DO UPDATE (PG 15+ also supports MERGE)")
	}

	return s
}

func convertNVL2(s string) string {
	// NVL2(expr, val_if_not_null, val_if_null)
	return reNVL2.ReplaceAllStringFunc(s, func(m string) string {
		inner := reNVL2.FindStringSubmatch(m)
		if len(inner) >= 2 {
			args := splitTopLevel(inner[1], ',')
			if len(args) == 3 {
				return fmt.Sprintf("CASE WHEN %s IS NOT NULL THEN %s ELSE %s END",
					strings.TrimSpace(args[0]),
					strings.TrimSpace(args[1]),
					strings.TrimSpace(args[2]))
			}
		}
		return m
	})
}

func convertDECODE(s string, warnings *[]string) string {
	// Loop to handle nested DECODE calls (inner ones get converted first)
	for i := 0; i < 10; i++ {
		prev := s
		s = convertDECODEOnce(s)
		if s == prev {
			break
		}
	}
	return s
}

// reDECODEStart finds the start of DECODE( to locate positions for balanced-paren extraction
var reDECODEStart = regexp.MustCompile(`(?i)\bDECODE\s*\(`)

func convertDECODEOnce(s string) string {
	// Find the innermost DECODE (one that has no nested DECODE in its args)
	loc := reDECODEStart.FindStringIndex(s)
	if loc == nil {
		return s
	}

	// Find matching closing paren using balanced parenthesis counting
	openPos := loc[1] - 1 // position of '('
	depth := 1
	i := loc[1]
	for i < len(s) && depth > 0 {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '\'':
			// skip string literals
			i++
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					break
				}
				i++
			}
		}
		i++
	}
	if depth != 0 {
		return s // unbalanced
	}
	closePos := i - 1

	inner := s[openPos+1 : closePos]
	args := splitTopLevel(inner, ',')
	if len(args) < 3 {
		return s
	}

	var sb strings.Builder
	sb.WriteString("CASE ")
	sb.WriteString(strings.TrimSpace(args[0]))

	j := 1
	for j+1 < len(args) {
		sb.WriteString(" WHEN ")
		sb.WriteString(strings.TrimSpace(args[j]))
		sb.WriteString(" THEN ")
		sb.WriteString(strings.TrimSpace(args[j+1]))
		j += 2
	}
	if j < len(args) {
		sb.WriteString(" ELSE ")
		sb.WriteString(strings.TrimSpace(args[j]))
	}
	sb.WriteString(" END")

	return s[:loc[0]] + sb.String() + s[closePos+1:]
}

func convertROWNUM(s string, warnings *[]string) string {
	// Simple WHERE ROWNUM <= N → append LIMIT N
	if m := reRownumWhere.FindStringSubmatch(s); len(m) >= 2 {
		// Find the non-empty capture group
		limit := m[1]
		if limit == "" {
			limit = m[2]
		}
		s = reRownumWhere.ReplaceAllString(s, "")
		s = strings.TrimRight(s, " \t\n;")
		s += "\nLIMIT " + strings.TrimSpace(limit)
		return s
	}
	// If ROWNUM is used in other contexts, flag it
	if reRownum.MatchString(s) {
		*warnings = append(*warnings, "ROWNUM → Use ROW_NUMBER() window function or LIMIT/OFFSET")
	}
	return s
}

func convertINSTR(s string, warnings *[]string) string {
	return reInstr.ReplaceAllStringFunc(s, func(m string) string {
		parts := reInstr.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		args := splitTopLevel(parts[1], ',')
		if len(args) == 2 {
			// INSTR(str, substr) → position(substr in str)
			return fmt.Sprintf("position(%s IN %s)", strings.TrimSpace(args[1]), strings.TrimSpace(args[0]))
		}
		// INSTR with 3 or 4 args → can't auto-convert
		*warnings = append(*warnings, "INSTR with position/occurrence args → Needs manual conversion or custom function")
		return m
	})
}

func convertLISTAGG(s string) string {
	return reListagg.ReplaceAllStringFunc(s, func(m string) string {
		parts := reListagg.FindStringSubmatch(m)
		if len(parts) >= 3 {
			expr := strings.TrimSpace(parts[1])
			delim := strings.TrimSpace(parts[2])
			orderBy := ""
			if len(parts) >= 4 && parts[3] != "" {
				orderBy = " " + strings.TrimSpace(parts[3])
			}
			return fmt.Sprintf("string_agg(%s, %s%s)", expr, delim, orderBy)
		}
		return m
	})
}

func convertRegexpLike(s string) string {
	return reRegexpLike.ReplaceAllStringFunc(s, func(m string) string {
		parts := reRegexpLike.FindStringSubmatch(m)
		if len(parts) >= 3 {
			str := strings.TrimSpace(parts[1])
			pattern := strings.TrimSpace(parts[2])
			flags := ""
			if len(parts) >= 4 && parts[3] != "" {
				flags = strings.Trim(strings.TrimSpace(parts[3]), "'\"")
			}
			op := "~"
			if strings.Contains(flags, "i") {
				op = "~*"
			}
			return fmt.Sprintf("%s %s %s", str, op, pattern)
		}
		return m
	})
}

func convertRaiseAppError(s string) string {
	return reRaiseAppError.ReplaceAllStringFunc(s, func(m string) string {
		parts := reRaiseAppError.FindStringSubmatch(m)
		if len(parts) >= 3 {
			return fmt.Sprintf("RAISE EXCEPTION '%%', %s", strings.TrimSpace(parts[2]))
		}
		return m
	})
}

func convertDbmsOutput(s string) string {
	return reDbmsOutput.ReplaceAllStringFunc(s, func(m string) string {
		parts := reDbmsOutput.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return fmt.Sprintf("RAISE NOTICE '%%', %s", strings.TrimSpace(parts[1]))
		}
		return m
	})
}

func convertSequenceRefs(s string) string {
	// seq.NEXTVAL → nextval('seq')
	s = reSeqNextval.ReplaceAllStringFunc(s, func(m string) string {
		parts := reSeqNextval.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return fmt.Sprintf("nextval('%s')", strings.ToLower(parts[1]))
		}
		return m
	})
	// seq.CURRVAL → currval('seq')
	s = reSeqCurrval.ReplaceAllStringFunc(s, func(m string) string {
		parts := reSeqCurrval.FindStringSubmatch(m)
		if len(parts) >= 2 {
			return fmt.Sprintf("currval('%s')", strings.ToLower(parts[1]))
		}
		return m
	})
	return s
}

// convertToDate handles TO_DATE format conversion and TO_DATE→TO_TIMESTAMP upgrade
// when format contains time components.
func convertToDate(s string) string {
	return reToDate.ReplaceAllStringFunc(s, func(m string) string {
		parts := reToDate.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		inner := parts[1]
		args := splitTopLevel(inner, ',')
		if len(args) < 2 {
			return m // TO_DATE with single arg, keep as-is
		}
		val := strings.TrimSpace(args[0])
		fmtStr := strings.TrimSpace(args[1])
		pgFmt := convertOracleDateFormat(fmtStr)

		// If format contains time components, upgrade to TO_TIMESTAMP
		fmtUpper := strings.ToUpper(fmtStr)
		hasTime := strings.Contains(fmtUpper, "HH") ||
			strings.Contains(fmtUpper, "MI") ||
			strings.Contains(fmtUpper, "SS") ||
			strings.Contains(fmtUpper, "FF")
		if hasTime {
			return fmt.Sprintf("TO_TIMESTAMP(%s, %s)", val, pgFmt)
		}
		return fmt.Sprintf("TO_DATE(%s, %s)", val, pgFmt)
	})
}

// convertToCharFmt converts Oracle format elements in TO_CHAR date calls.
func convertToCharFmt(s string) string {
	return reToChar.ReplaceAllStringFunc(s, func(m string) string {
		parts := reToChar.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		inner := parts[1]
		args := splitTopLevel(inner, ',')
		if len(args) < 2 {
			return m // TO_CHAR with single arg, keep as-is
		}
		val := strings.TrimSpace(args[0])
		fmtStr := strings.TrimSpace(args[1])
		pgFmt := convertOracleDateFormat(fmtStr)
		if len(args) == 3 {
			return fmt.Sprintf("TO_CHAR(%s, %s, %s)", val, pgFmt, strings.TrimSpace(args[2]))
		}
		return fmt.Sprintf("TO_CHAR(%s, %s)", val, pgFmt)
	})
}

// convertOracleDateFormat converts Oracle date format elements to PostgreSQL equivalents.
func convertOracleDateFormat(fmtStr string) string {
	// Only process if it looks like a quoted string
	trimmed := strings.TrimSpace(fmtStr)
	if len(trimmed) < 2 || trimmed[0] != '\'' {
		return fmtStr
	}

	// Extract content between quotes
	inner := trimmed[1 : len(trimmed)-1]

	// Apply replacements (longer patterns first to avoid partial matches)
	replacements := []struct{ old, new string }{
		{"RRRR", "YYYY"},
		{"RR", "YY"},
		{"SSSSS", "SSSS"},
		{"FF9", "US"},
		{"FF6", "US"},
		{"FF3", "MS"},
		{"FF", "US"},
		{"TZR", "TZ"},
		{"TZH", "OF"},
	}
	for _, r := range replacements {
		inner = strings.ReplaceAll(inner, r.old, r.new)
		inner = strings.ReplaceAll(inner, strings.ToLower(r.old), strings.ToLower(r.new))
	}

	return "'" + inner + "'"
}

func convertParamTypes(s string) string {
	// NUMBER → NUMERIC, NUMBER(p) → NUMERIC(p), NUMBER(p,s) → NUMERIC(p,s)
	s = reParamNumber.ReplaceAllString(s, "NUMERIC${1}")
	// VARCHAR2(n) → VARCHAR(n)
	s = reParamVarchar2.ReplaceAllString(s, "VARCHAR${1}")
	// CLOB → TEXT
	s = reParamClob.ReplaceAllString(s, "TEXT")
	// BLOB → BYTEA
	s = reParamBlob.ReplaceAllString(s, "BYTEA")
	// DATE (standalone type) → TIMESTAMP
	// Be careful not to replace inside strings
	s = reParamDate.ReplaceAllString(s, "${1}TIMESTAMP${2}")
	return s
}

func convertOracleTypes(s string) string {
	// Same type replacements in variable declarations
	s = convertParamTypes(s)
	// BOOLEAN is supported in PG, no change needed
	// PLS_INTEGER → INTEGER
	s = rePLSInteger.ReplaceAllString(s, "INTEGER")
	// BINARY_INTEGER → INTEGER
	s = reBinaryInteger.ReplaceAllString(s, "INTEGER")
	return s
}

// applyNaming converts identifier case while preserving string literals, comments, and SQL keywords.
func applyNaming(s string, naming NamingConvention) string {
	if naming == NamingKeepOriginal || naming == "" {
		return s
	}

	// SQL/PL/pgSQL keywords that should stay in their standard case
	keywords := map[string]bool{
		"SELECT": true, "FROM": true, "WHERE": true, "AND": true, "OR": true,
		"INSERT": true, "INTO": true, "UPDATE": true, "DELETE": true, "SET": true,
		"VALUES": true, "CREATE": true, "DROP": true, "ALTER": true, "TABLE": true,
		"INDEX": true, "ON": true, "AS": true, "IS": true, "IN": true, "OUT": true,
		"NOT": true, "NULL": true, "DEFAULT": true, "PRIMARY": true, "KEY": true,
		"FOREIGN": true, "REFERENCES": true, "CONSTRAINT": true, "UNIQUE": true,
		"CHECK": true, "CASCADE": true, "IF": true, "THEN": true, "ELSE": true,
		"ELSIF": true, "END": true, "BEGIN": true, "DECLARE": true, "EXCEPTION": true,
		"WHEN": true, "OTHERS": true, "RAISE": true, "RETURN": true, "RETURNS": true,
		"LOOP": true, "FOR": true, "WHILE": true, "EXIT": true, "CONTINUE": true,
		"CASE": true, "FUNCTION": true, "PROCEDURE": true, "TRIGGER": true,
		"EXECUTE": true, "LANGUAGE": true, "REPLACE": true, "VIEW": true,
		"MATERIALIZED": true, "REFRESH": true, "COMPLETE": true, "FORCE": true,
		"DEMAND": true, "BUILD": true, "IMMEDIATE": true, "COMMIT": true,
		"ROLLBACK": true, "SAVEPOINT": true, "PARTITION": true, "BY": true,
		"RANGE": true, "LIST": true, "HASH": true, "GROUP": true, "ORDER": true,
		"ASC": true, "DESC": true, "HAVING": true, "LIMIT": true, "OFFSET": true,
		"JOIN": true, "LEFT": true, "RIGHT": true, "INNER": true, "OUTER": true,
		"FULL": true, "CROSS": true, "NATURAL": true, "USING": true,
		"DISTINCT": true, "COUNT": true, "SUM": true, "AVG": true, "MIN": true,
		"MAX": true, "COALESCE": true, "NULLIF": true, "CAST": true,
		"NUMERIC": true, "INTEGER": true, "BIGINT": true, "SMALLINT": true,
		"VARCHAR": true, "CHAR": true, "TEXT": true, "BOOLEAN": true,
		"TIMESTAMP": true, "TIMESTAMPTZ": true, "DATE": true, "INTERVAL": true,
		"BYTEA": true, "XML": true, "JSON": true, "JSONB": true,
		"TRUE": true, "FALSE": true, "LIKE": true, "BETWEEN": true, "EXISTS": true,
		"OVER": true, "RANK": true, "ROW_NUMBER": true, "DENSE_RANK": true,
		"DATE_TRUNC": true, "CURRENT_DATE": true, "CURRENT_TIMESTAMP": true,
		"EACH": true, "ROW": true, "BEFORE": true, "AFTER": true, "INSTEAD": true,
		"OF": true, "NEW": true, "OLD": true, "TYPE": true, "RECORD": true,
		"CONSTANT": true, "NOTICE": true, "PACKAGE": true,
		"BODY": true, "WITH": true, "RECURSIVE": true, "UNION": true, "ALL": true,
		"INTERSECT": true, "EXCEPT": true, "SEQUENCE": true, "OWNED": true,
		"NONE": true, "GRANT": true, "REVOKE": true, "TO": true,
		"DIAGNOSTICS": true, "GET": true, "ROW_COUNT": true,
		// PG-specific identifiers that must keep their case
		"TG_OP": true, "TG_TABLE_NAME": true, "TG_TABLE_SCHEMA": true,
		"TG_NAME": true, "TG_WHEN": true, "TG_LEVEL": true, "TG_RELID": true,
		"TG_NARGS": true, "TG_ARGV": true,
		"SQLERRM": true, "SQLSTATE": true, "SQLCODE": true,
		"FOUND": true, "NOT_FOUND": true,
		"PERFORM": true, "SETOF": true, "VOID": true, "INOUT": true,
	}

	var result strings.Builder
	result.Grow(len(s))
	i := 0

	for i < len(s) {
		// Skip string literals (single quotes)
		if s[i] == '\'' {
			j := i + 1
			for j < len(s) {
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						j += 2 // escaped quote
						continue
					}
					break
				}
				j++
			}
			if j < len(s) {
				j++ // include closing quote
			}
			result.WriteString(s[i:j])
			i = j
			continue
		}

		// Skip double-quoted identifiers
		if s[i] == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j < len(s) {
				j++ // include closing quote
			}
			result.WriteString(s[i:j])
			i = j
			continue
		}

		// Skip line comments
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			j := i
			for j < len(s) && s[j] != '\n' {
				j++
			}
			result.WriteString(s[i:j])
			i = j
			continue
		}

		// Skip block comments
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			j := i + 2
			for j+1 < len(s) && !(s[j] == '*' && s[j+1] == '/') {
				j++
			}
			if j+1 < len(s) {
				j += 2
			}
			result.WriteString(s[i:j])
			i = j
			continue
		}

		// Skip $$ delimiters
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '$' {
			result.WriteString("$$")
			i += 2
			continue
		}

		// Identifier: letter or underscore followed by alnum/underscore
		if isIdentStart(s[i]) {
			j := i + 1
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			word := s[i:j]
			upper := strings.ToUpper(word)

			if keywords[upper] {
				// Keywords stay uppercase for readability
				result.WriteString(upper)
			} else {
				switch naming {
				case NamingLowercase:
					result.WriteString(strings.ToLower(word))
				case NamingUppercase:
					result.WriteString(strings.ToUpper(word))
				default:
					result.WriteString(word)
				}
			}
			i = j
			continue
		}

		result.WriteByte(s[i])
		i++
	}

	return result.String()
}

func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isIdentChar(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func splitDeclareBody(body string) (string, string) {
	upper := strings.ToUpper(body)

	declareIdx := strings.Index(upper, "DECLARE")
	beginIdx := strings.Index(upper, "BEGIN")

	if declareIdx >= 0 && beginIdx > declareIdx {
		declare := strings.TrimSpace(body[declareIdx+7 : beginIdx])
		// Find last END;
		lastEnd := strings.LastIndex(upper, "END;")
		if lastEnd > beginIdx {
			return declare, strings.TrimSpace(body[beginIdx+5 : lastEnd])
		}
		return declare, strings.TrimSpace(body[beginIdx+5:])
	}

	if beginIdx >= 0 {
		lastEnd := strings.LastIndex(upper, "END;")
		if lastEnd > beginIdx {
			return "", strings.TrimSpace(body[beginIdx+5 : lastEnd])
		}
		return "", strings.TrimSpace(body[beginIdx+5:])
	}

	return "", body
}

// splitTopLevel splits a string by separator, respecting parentheses nesting.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// --- Compiled regexes ---

var (
	reNVL          = regexp.MustCompile(`(?i)\bNVL\s*\(`)
	reNVL2         = regexp.MustCompile(`(?i)\bNVL2\s*\(([^)]+(?:\([^)]*\))*[^)]*)\)`)
	reSysdate      = regexp.MustCompile(`(?i)\bSYSDATE\b`)
	reSystimestamp = regexp.MustCompile(`(?i)\bSYSTIMESTAMP\b`)
	reTruncDate    = regexp.MustCompile(`(?i)\bTRUNC\s*\(([^)]+)\)`)
	reAddMonths    = regexp.MustCompile(`(?i)\bADD_MONTHS\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	reToNumber     = regexp.MustCompile(`(?i)\bTO_NUMBER\s*\(\s*([^)]+)\s*\)`)
	reRownum       = regexp.MustCompile(`(?i)\bROWNUM\b`)
	reRownumWhere  = regexp.MustCompile(`(?i)\bAND\s+ROWNUM\s*<=\s*(\d+)|\bWHERE\s+ROWNUM\s*<=\s*(\d+)`)
	reNewOld       = regexp.MustCompile(`(?i):(NEW|OLD)\.\w+`)
	reRaiseAppError = regexp.MustCompile(`(?i)\bRAISE_APPLICATION_ERROR\s*\(\s*(-?\d+)\s*,\s*(.+?)\s*\)`)
	reDbmsOutput   = regexp.MustCompile(`(?i)\bDBMS_OUTPUT\.PUT_LINE\s*\(\s*(.+?)\s*\)`)
	reSeqNextval   = regexp.MustCompile(`(?i)\b(\w+)\.NEXTVAL\b`)
	reSeqCurrval   = regexp.MustCompile(`(?i)\b(\w+)\.CURRVAL\b`)
	reSQLRowcount  = regexp.MustCompile(`(?i)\bSQL%ROWCOUNT\b`)
	reNoDataFound  = regexp.MustCompile(`(?i)\bNO_DATA_FOUND\b`)
	reReturnType   = regexp.MustCompile(`(?i)(\)\s*)RETURN(\s+)`)
	rePercentType  = regexp.MustCompile(`(?i)\w+%(?:ROW)?TYPE\b`)
	reBulkCollect  = regexp.MustCompile(`(?i)\bBULK\s+COLLECT\b`)
	reForall       = regexp.MustCompile(`(?i)\bFORALL\b`)
	reConnectBy    = regexp.MustCompile(`(?i)\bCONNECT\s+BY\b`)
	reRefCursor    = regexp.MustCompile(`(?i)\bSYS_REFCURSOR\b`)
	reTypeDecl     = regexp.MustCompile(`(?i)\bTYPE\s+\w+\s+IS\s+(?:TABLE|RECORD)\b`)
	rePackageHeader     = regexp.MustCompile(`(?i)\bPACKAGE\s+(\w+)\s+AS\b`)
	rePackageBodyHeader = regexp.MustCompile(`(?i)\bPACKAGE\s+BODY\s+(\w+)\s+AS\b`)
	reCommit       = regexp.MustCompile(`(?i)\bCOMMIT\s*;`)
	reProcHeader   = regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+PROCEDURE\s+\w+\s*\([^)]*\)\s*(?:AS|IS)`)
	reFuncHeader   = regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+FUNCTION\s+\w+\s*\([^)]*\)\s*RETURNS?\s+\w+\s*(?:AS|IS)`)
	reTrimAsIs     = regexp.MustCompile(`(?i)\s+(?:AS|IS)\s*$`)
	reParamNumber  = regexp.MustCompile(`(?i)\bNUMBER\b(\([^)]*\))?`)
	reParamVarchar2 = regexp.MustCompile(`(?i)\bN?VARCHAR2\b(\([^)]*\))?`)
	reParamClob    = regexp.MustCompile(`(?i)\bN?CLOB\b`)
	reParamBlob    = regexp.MustCompile(`(?i)\bBLOB\b`)
	reParamDate    = regexp.MustCompile(`(?i)(\s)DATE(\s|;|,|\))`)
	rePLSInteger   = regexp.MustCompile(`(?i)\bPLS_INTEGER\b`)
	reBinaryInteger = regexp.MustCompile(`(?i)\bBINARY_INTEGER\b`)

	// New patterns
	reFromDual        = regexp.MustCompile(`(?i)\s+FROM\s+DUAL\b`)
	reMinus           = regexp.MustCompile(`(?i)\bMINUS\b`)
	reInstr           = regexp.MustCompile(`(?i)\bINSTR\s*\(([^)]+(?:\([^)]*\))*[^)]*)\)`)
	reListagg         = regexp.MustCompile(`(?i)\bLISTAGG\s*\(\s*([^,]+)\s*,\s*([^)]+)\)\s*WITHIN\s+GROUP\s*\(\s*(ORDER\s+BY\s+[^)]+)\)`)
	reWMConcat        = regexp.MustCompile(`(?i)\bWM_CONCAT\s*\(\s*([^)]+)\s*\)`)
	reLastDay         = regexp.MustCompile(`(?i)\bLAST_DAY\s*\(\s*([^)]+)\s*\)`)
	reMonthsBetween   = regexp.MustCompile(`(?i)\bMONTHS_BETWEEN\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	reSysGuid         = regexp.MustCompile(`(?i)\bSYS_GUID\s*\(\s*\)`)
	reEmptyClob       = regexp.MustCompile(`(?i)\bEMPTY_CLOB\s*\(\s*\)`)
	reEmptyBlob       = regexp.MustCompile(`(?i)\bEMPTY_BLOB\s*\(\s*\)`)
	reBitand          = regexp.MustCompile(`(?i)\bBITAND\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	reLengthB         = regexp.MustCompile(`(?i)\bLENGTHB\s*\(`)
	reRegexpLike      = regexp.MustCompile(`(?i)\bREGEXP_LIKE\s*\(\s*([^,]+)\s*,\s*([^,)]+)\s*(?:,\s*([^)]+)\s*)?\)`)
	reOuterJoinPlus   = regexp.MustCompile(`\(\+\)`)
	reMergeInto       = regexp.MustCompile(`(?i)\bMERGE\s+INTO\b`)
	reExecImmediate   = regexp.MustCompile(`(?i)\bEXECUTE\s+IMMEDIATE\s+`)
	reFromTZ          = regexp.MustCompile(`(?i)\bFROM_TZ\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	reNumToDSInterval = regexp.MustCompile(`(?i)\bNUMTODSINTERVAL\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)

	// SYS_CONTEXT
	reSysCtxUser       = regexp.MustCompile(`(?i)\bSYS_CONTEXT\s*\(\s*'USERENV'\s*,\s*'CURRENT_USER'\s*\)`)
	reSysCtxSessionUser = regexp.MustCompile(`(?i)\bSYS_CONTEXT\s*\(\s*'USERENV'\s*,\s*'SESSION_USER'\s*\)`)
	reSysCtxDbName     = regexp.MustCompile(`(?i)\bSYS_CONTEXT\s*\(\s*'USERENV'\s*,\s*'DB_NAME'\s*\)`)
	reSysCtxIPAddr     = regexp.MustCompile(`(?i)\bSYS_CONTEXT\s*\(\s*'USERENV'\s*,\s*'IP_ADDRESS'\s*\)`)
	reSysCtxSessionID  = regexp.MustCompile(`(?i)\bSYS_CONTEXT\s*\(\s*'USERENV'\s*,\s*'SESSIONID'\s*\)`)

	// DBMS packages
	reDbmsLobGetLength = regexp.MustCompile(`(?i)\bDBMS_LOB\.GETLENGTH\s*\(\s*([^)]+)\s*\)`)
	reDbmsLobSubstr    = regexp.MustCompile(`(?i)\bDBMS_LOB\.SUBSTR\s*\(\s*([^,]+)\s*,\s*([^,]+)\s*,\s*([^)]+)\s*\)`)
	reDbmsLockSleep    = regexp.MustCompile(`(?i)\bDBMS_LOCK\.SLEEP\s*\(\s*([^)]+)\s*\)`)

	// Trigger-specific
	reInserting = regexp.MustCompile(`(?i)\bINSERTING\b`)
	reUpdating  = regexp.MustCompile(`(?i)\bUPDATING\b`)
	reDeleting  = regexp.MustCompile(`(?i)\bDELETING\b`)

	// Exception names
	reDupValOnIndex = regexp.MustCompile(`(?i)\bDUP_VAL_ON_INDEX\b`)
	reZeroDivide    = regexp.MustCompile(`(?i)\bZERO_DIVIDE\b`)
	reTooManyRows   = regexp.MustCompile(`(?i)\bTOO_MANY_ROWS\b`)
	reValueError    = regexp.MustCompile(`(?i)\bVALUE_ERROR\b`)
	reInvalidNumber = regexp.MustCompile(`(?i)\bINVALID_NUMBER\b`)

	// Cursor attributes
	reCursorNotFound = regexp.MustCompile(`(?i)\b\w+%NOTFOUND\b`)
	reCursorFound    = regexp.MustCompile(`(?i)\b\w+%FOUND\b`)
	reSQLNotFound    = regexp.MustCompile(`(?i)\bSQL%NOTFOUND\b`)
	reSQLFound       = regexp.MustCompile(`(?i)\bSQL%FOUND\b`)

	// CURSOR cur IS SELECT → cur CURSOR FOR SELECT
	reCursorDecl = regexp.MustCompile(`(?i)\bCURSOR\s+(\w+)\s+IS\s+SELECT\b`)

	// IN OUT → INOUT
	reInOut = regexp.MustCompile(`(?i)\bIN\s+OUT\b`)

	// TO_DATE / TO_CHAR with format — balanced-paren extraction
	reToDate = regexp.MustCompile(`(?i)\bTO_DATE\s*\(([^)]+(?:\([^)]*\))*[^)]*)\)`)
	reToChar = regexp.MustCompile(`(?i)\bTO_CHAR\s*\(([^)]+(?:\([^)]*\))*[^)]*)\)`)

	// PRAGMA
	rePragmaExcInit    = regexp.MustCompile(`(?im)^\s*PRAGMA\s+EXCEPTION_INIT\s*\([^)]+\)\s*;`)
	rePragmaAutonomous = regexp.MustCompile(`(?im)^\s*PRAGMA\s+AUTONOMOUS_TRANSACTION\s*;`)
	rePragmaRestrictRef = regexp.MustCompile(`(?im)^\s*PRAGMA\s+RESTRICT_REFERENCES\s*\([^)]+\)\s*;`)

	// NOCOPY → remove
	reNocopy = regexp.MustCompile(`(?i)\bNOCOPY\s+`)

	// DETERMINISTIC → IMMUTABLE
	reDeterministic = regexp.MustCompile(`(?i)\bDETERMINISTIC\b`)

	// RESULT_CACHE
	reResultCache = regexp.MustCompile(`(?i)\bRESULT_CACHE\b`)

	// AUTHID
	reAuthidCurrentUser = regexp.MustCompile(`(?i)\bAUTHID\s+CURRENT_USER\b`)
	reAuthidDefiner     = regexp.MustCompile(`(?i)\bAUTHID\s+DEFINER\b`)
	reSecurityInvoker   = regexp.MustCompile(`(?i)\bSECURITY\s+INVOKER\b`)
	reSecurityDefiner   = regexp.MustCompile(`(?i)\bSECURITY\s+DEFINER\b`)

	// REGEXP_SUBSTR(str, pat) → substring(str FROM pat)
	reRegexpSubstr = regexp.MustCompile(`(?i)\bREGEXP_SUBSTR\s*\(\s*([^,]+)\s*,\s*([^,)]+)\s*\)`)
	// REGEXP_COUNT(str, pat) → regexp_matches count
	reRegexpCount = regexp.MustCompile(`(?i)\bREGEXP_COUNT\s*\(\s*([^,]+)\s*,\s*([^,)]+)\s*\)`)

	// NUMTOYMINTERVAL
	reNumToYMInterval = regexp.MustCompile(`(?i)\bNUMTOYMINTERVAL\s*\(\s*([^,]+)\s*,\s*([^)]+)\s*\)`)

	// DBMS_UTILITY.GET_TIME
	reDbmsUtilGetTime = regexp.MustCompile(`(?i)\bDBMS_UTILITY\.GET_TIME\b`)

	// DBMS_OUTPUT.PUT (without _LINE)
	reDbmsOutputPut = regexp.MustCompile(`(?i)\bDBMS_OUTPUT\.PUT\s*\(\s*(.+?)\s*\)`)

	// Additional exception names
	reLoginDenied      = regexp.MustCompile(`(?i)\bLOGIN_DENIED\b`)
	reCursorAlreadyOpen = regexp.MustCompile(`(?i)\bCURSOR_ALREADY_OPEN\b`)
	reInvalidCursor    = regexp.MustCompile(`(?i)\bINVALID_CURSOR\b`)

	// OPEN/FETCH/CLOSE cursor
	reOpenCursor  = regexp.MustCompile(`(?i)\bOPEN\s+\w+(?:\s*\([^)]*\))?\s*;|\bOPEN\s+\w+\s+FOR\b`)
	reFetchCursor = regexp.MustCompile(`(?i)\bFETCH\s+\w+\s+INTO\b`)
	reCloseCursor = regexp.MustCompile(`(?i)\bCLOSE\s+\w+\s*;`)

	// SAVEPOINT
	reSavepoint = regexp.MustCompile(`(?i)\bSAVEPOINT\s+\w+\s*;`)

	// PIPELINED / PIPE ROW
	rePipelined = regexp.MustCompile(`(?i)\bPIPELINED\b`)
	rePipeRow   = regexp.MustCompile(`(?i)\bPIPE\s+ROW\s*\(`)

	// DBMS package warnings
	reUtlFile    = regexp.MustCompile(`(?i)\bUTL_FILE\.\w+`)
	reDbmsSql    = regexp.MustCompile(`(?i)\bDBMS_SQL\.\w+`)
	reDbmsJob    = regexp.MustCompile(`(?i)\bDBMS_(?:JOB|SCHEDULER)\.\w+`)
	reDbmsCrypto = regexp.MustCompile(`(?i)\bDBMS_CRYPTO\.\w+`)

	// ROWID reference
	reRowidRef = regexp.MustCompile(`(?i)\bROWID\b`)
)
