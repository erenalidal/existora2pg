package plsql

import (
	"strings"
	"testing"
)

func TestConvertView_NVL(t *testing.T) {
	r := ConvertView("SELECT NVL(a, 0) FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "COALESCE(a, 0)") {
		t.Errorf("expected COALESCE, got: %s", r.Definition)
	}
}

func TestConvertView_NVL2(t *testing.T) {
	r := ConvertView("SELECT NVL2(a, b, c) FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "CASE WHEN a IS NOT NULL THEN b ELSE c END") {
		t.Errorf("expected CASE WHEN, got: %s", r.Definition)
	}
}

func TestConvertView_DECODE(t *testing.T) {
	r := ConvertView("SELECT DECODE(status, 'A', 'Active', 'I', 'Inactive', 'Unknown') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "CASE status WHEN 'A' THEN 'Active' WHEN 'I' THEN 'Inactive' ELSE 'Unknown' END") {
		t.Errorf("expected CASE, got: %s", r.Definition)
	}
}

func TestConvertView_SYSDATE(t *testing.T) {
	r := ConvertView("SELECT SYSDATE FROM dual", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "CURRENT_DATE") {
		t.Errorf("expected CURRENT_DATE, got: %s", r.Definition)
	}
}

func TestConvertView_SYSTIMESTAMP(t *testing.T) {
	r := ConvertView("SELECT SYSTIMESTAMP FROM dual", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "CURRENT_TIMESTAMP") {
		t.Errorf("expected CURRENT_TIMESTAMP, got: %s", r.Definition)
	}
}

func TestConvertView_TRUNC(t *testing.T) {
	r := ConvertView("SELECT TRUNC(order_date) FROM orders", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "(order_date)::date") {
		t.Errorf("expected ::date cast, got: %s", r.Definition)
	}
}

func TestConvertView_TRUNCMonth(t *testing.T) {
	r := ConvertView("SELECT TRUNC(order_date, 'MM') FROM orders", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "DATE_TRUNC('month', order_date)") {
		t.Errorf("expected DATE_TRUNC month, got: %s", r.Definition)
	}
}

func TestConvertView_ROWNUM(t *testing.T) {
	r := ConvertView("SELECT * FROM t WHERE ROWNUM <= 100", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "LIMIT 100") {
		t.Errorf("expected LIMIT, got: %s", r.Definition)
	}
	if strings.Contains(r.Definition, "ROWNUM") {
		t.Errorf("ROWNUM should be removed, got: %s", r.Definition)
	}
}

func TestConvertTrigger_NewOld(t *testing.T) {
	r := ConvertTrigger("trg_test", "customers", "BEFORE EACH ROW", "INSERT",
		"BEGIN\n  :NEW.created_at := SYSDATE;\n  :NEW.updated_at := SYSTIMESTAMP;\nEND;", NamingKeepOriginal)
	if strings.Contains(r.Definition, ":NEW") {
		t.Errorf(":NEW should be converted to NEW, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "NEW.created_at") {
		t.Errorf("expected NEW.created_at, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "CURRENT_DATE") {
		t.Errorf("expected CURRENT_DATE, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "RETURNS TRIGGER") {
		t.Errorf("expected RETURNS TRIGGER, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "EXECUTE FUNCTION") {
		t.Errorf("expected EXECUTE FUNCTION, got: %s", r.Definition)
	}
}

func TestConvertProcedure_RaiseAppError(t *testing.T) {
	r := ConvertProcedure("sp_test", "PROCEDURE",
		"BEGIN\n  RAISE_APPLICATION_ERROR(-20001, 'Not found');\nEND;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "RAISE EXCEPTION") {
		t.Errorf("expected RAISE EXCEPTION, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DbmsOutput(t *testing.T) {
	r := ConvertProcedure("sp_test", "PROCEDURE",
		"BEGIN\n  DBMS_OUTPUT.PUT_LINE('hello');\nEND;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "RAISE NOTICE") {
		t.Errorf("expected RAISE NOTICE, got: %s", r.Definition)
	}
}

func TestConvertProcedure_SeqNextval(t *testing.T) {
	r := ConvertProcedure("sp_test", "PROCEDURE",
		"BEGIN\n  v_id := seq_orders.NEXTVAL;\nEND;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "nextval('seq_orders')") {
		t.Errorf("expected nextval(), got: %s", r.Definition)
	}
}

func TestConvertProcedure_PackageWarning(t *testing.T) {
	r := ConvertProcedure("pkg_test", "PACKAGE",
		"PACKAGE pkg_test AS\n  PROCEDURE do_something;\nEND;", NamingKeepOriginal)
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "PACKAGE") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected PACKAGE warning, got: %v", r.Warnings)
	}
}

func TestConvertProcedure_BulkCollectWarning(t *testing.T) {
	r := ConvertProcedure("sp_test", "PROCEDURE",
		"BEGIN\n  SELECT col BULK COLLECT INTO v_tab FROM t;\nEND;", NamingKeepOriginal)
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "BULK COLLECT") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected BULK COLLECT warning, got: %v", r.Warnings)
	}
}

func TestConvertView_AddMonths(t *testing.T) {
	r := ConvertView("SELECT ADD_MONTHS(SYSDATE, 3) FROM dual", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "INTERVAL '3 months'") {
		t.Errorf("expected interval, got: %s", r.Definition)
	}
}

func TestConvertView_ToNumber(t *testing.T) {
	r := ConvertView("SELECT TO_NUMBER(col) FROM t", NamingKeepOriginal)
	if !strings.Contains(strings.ToLower(r.Definition), "(col)::numeric") {
		t.Errorf("expected ::numeric cast, got: %s", r.Definition)
	}
}

func TestSplitTopLevel(t *testing.T) {
	parts := splitTopLevel("a, b, NVL(c, 0), d", ',')
	if len(parts) != 4 {
		t.Errorf("expected 4 parts, got %d: %v", len(parts), parts)
	}
}

// --- Naming convention tests ---

func TestNaming_Lowercase(t *testing.T) {
	r := ConvertView("SELECT CUSTOMER_ID, NAME FROM CUSTOMERS WHERE STATUS = 'A'", NamingLowercase)
	if !strings.Contains(r.Definition, "customer_id") {
		t.Errorf("expected lowercase customer_id, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "customers") {
		t.Errorf("expected lowercase customers, got: %s", r.Definition)
	}
	// Keywords should stay uppercase
	if !strings.Contains(r.Definition, "SELECT") {
		t.Errorf("expected SELECT keyword uppercase, got: %s", r.Definition)
	}
	// String literals should be preserved
	if !strings.Contains(r.Definition, "'A'") {
		t.Errorf("expected string literal preserved, got: %s", r.Definition)
	}
}

func TestNaming_Uppercase(t *testing.T) {
	r := ConvertView("SELECT customer_id FROM customers", NamingUppercase)
	if !strings.Contains(r.Definition, "CUSTOMER_ID") {
		t.Errorf("expected uppercase CUSTOMER_ID, got: %s", r.Definition)
	}
}

func TestNaming_KeepOriginal(t *testing.T) {
	input := "SELECT Customer_Id FROM Customers"
	r := ConvertView(input, NamingKeepOriginal)
	if !strings.Contains(r.Definition, "Customer_Id") {
		t.Errorf("expected original case, got: %s", r.Definition)
	}
}

func TestNaming_PreservesStringLiterals(t *testing.T) {
	r := ConvertView("SELECT DECODE(STATUS, 'ACTIVE', 'Yes', 'No') FROM CUSTOMERS", NamingLowercase)
	// String literals should NOT be lowercased
	if !strings.Contains(r.Definition, "'ACTIVE'") {
		t.Errorf("string literal should be preserved, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "'Yes'") {
		t.Errorf("string literal should be preserved, got: %s", r.Definition)
	}
}

func TestNaming_PreservesComments(t *testing.T) {
	r := ConvertView("SELECT COL1 -- THIS IS A COMMENT\nFROM TABLE1", NamingLowercase)
	if !strings.Contains(r.Definition, "-- THIS IS A COMMENT") {
		t.Errorf("comment should be preserved, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "col1") {
		t.Errorf("identifier should be lowered, got: %s", r.Definition)
	}
}

func TestNaming_TriggerLowercase(t *testing.T) {
	r := ConvertTrigger("TRG_TEST", "CUSTOMERS", "BEFORE EACH ROW", "INSERT",
		"BEGIN\n  :NEW.CREATED_AT := SYSDATE;\nEND;", NamingLowercase)
	if !strings.Contains(r.Definition, "fn_trg_test") {
		t.Errorf("expected lowercase function name, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "customers") {
		t.Errorf("expected lowercase table name, got: %s", r.Definition)
	}
}

// --- New conversion pattern tests ---

func TestConvertView_FromDual(t *testing.T) {
	r := ConvertView("SELECT CURRENT_DATE FROM DUAL", NamingKeepOriginal)
	if strings.Contains(r.Definition, "DUAL") {
		t.Errorf("FROM DUAL should be removed, got: %s", r.Definition)
	}
}

func TestConvertView_Minus(t *testing.T) {
	r := ConvertView("SELECT a FROM t1 MINUS SELECT a FROM t2", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "EXCEPT") {
		t.Errorf("expected EXCEPT, got: %s", r.Definition)
	}
}

func TestConvertView_Instr(t *testing.T) {
	r := ConvertView("SELECT INSTR(name, 'test') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "position('test' IN name)") {
		t.Errorf("expected position(), got: %s", r.Definition)
	}
}

func TestConvertView_SysGuid(t *testing.T) {
	r := ConvertView("SELECT SYS_GUID() FROM DUAL", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "gen_random_uuid()") {
		t.Errorf("expected gen_random_uuid(), got: %s", r.Definition)
	}
}

func TestConvertView_EmptyClob(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "v_text := EMPTY_CLOB();", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "''") {
		t.Errorf("expected empty string, got: %s", r.Definition)
	}
}

func TestConvertView_Bitand(t *testing.T) {
	r := ConvertView("SELECT BITAND(flags, 4) FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "(flags & 4)") {
		t.Errorf("expected bitwise AND, got: %s", r.Definition)
	}
}

func TestConvertView_LastDay(t *testing.T) {
	r := ConvertView("SELECT LAST_DAY(hire_date) FROM emp", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "DATE_TRUNC") && !strings.Contains(r.Definition, "1 month") {
		t.Errorf("expected date_trunc + interval, got: %s", r.Definition)
	}
}

func TestConvertView_MonthsBetween(t *testing.T) {
	r := ConvertView("SELECT MONTHS_BETWEEN(d1, d2) FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "EXTRACT") && !strings.Contains(r.Definition, "age") {
		t.Errorf("expected EXTRACT + age, got: %s", r.Definition)
	}
}

func TestConvertView_LengthB(t *testing.T) {
	r := ConvertView("SELECT LENGTHB(col) FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "octet_length(") {
		t.Errorf("expected octet_length, got: %s", r.Definition)
	}
}

func TestConvertView_RegexpLike(t *testing.T) {
	r := ConvertView("SELECT * FROM t WHERE REGEXP_LIKE(name, '^A')", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "name ~ '^A'") {
		t.Errorf("expected ~ operator, got: %s", r.Definition)
	}
}

func TestConvertProcedure_ExecImmediate(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "EXECUTE IMMEDIATE v_sql;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "EXECUTE v_sql") {
		t.Errorf("expected EXECUTE without IMMEDIATE, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DupValOnIndex(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE",
		"EXCEPTION WHEN DUP_VAL_ON_INDEX THEN NULL;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "unique_violation") {
		t.Errorf("expected unique_violation, got: %s", r.Definition)
	}
}

func TestConvertProcedure_CursorNotFound(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE",
		"EXIT WHEN cur%NOTFOUND;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "NOT FOUND") {
		t.Errorf("expected NOT FOUND, got: %s", r.Definition)
	}
}

func TestConvertProcedure_InOut(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE",
		"p_val IN OUT NUMBER", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "INOUT") {
		t.Errorf("expected INOUT, got: %s", r.Definition)
	}
}

func TestConvertTrigger_Inserting(t *testing.T) {
	r := ConvertTrigger("trg", "t", "BEFORE EACH ROW", "INSERT OR UPDATE",
		"BEGIN\n  IF INSERTING THEN NULL; ELSIF UPDATING THEN NULL; END IF;\nEND;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "TG_OP = 'INSERT'") {
		t.Errorf("expected TG_OP INSERT, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "TG_OP = 'UPDATE'") {
		t.Errorf("expected TG_OP UPDATE, got: %s", r.Definition)
	}
}

func TestConvertView_SysContext(t *testing.T) {
	r := ConvertView("SELECT SYS_CONTEXT('USERENV', 'CURRENT_USER') FROM DUAL", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "current_user") {
		t.Errorf("expected current_user, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DbmsLobGetLength(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "v_len := DBMS_LOB.GETLENGTH(v_clob);", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "octet_length(") {
		t.Errorf("expected octet_length, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DbmsLockSleep(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "DBMS_LOCK.SLEEP(5);", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "pg_sleep(5)") {
		t.Errorf("expected pg_sleep, got: %s", r.Definition)
	}
}

func TestConvertView_FromTZ(t *testing.T) {
	r := ConvertView("SELECT FROM_TZ(ts_col, 'UTC') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "AT TIME ZONE") {
		t.Errorf("expected AT TIME ZONE, got: %s", r.Definition)
	}
}

// --- New pattern tests ---

func TestConvertView_ToDateFormat(t *testing.T) {
	r := ConvertView("SELECT TO_DATE('01-JAN-24', 'DD-MON-RR') FROM DUAL", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "'DD-MON-YY'") {
		t.Errorf("expected RR→YY format conversion, got: %s", r.Definition)
	}
}

func TestConvertView_ToDateWithTime(t *testing.T) {
	r := ConvertView("SELECT TO_DATE('2024-01-15 10:30', 'YYYY-MM-DD HH24:MI') FROM DUAL", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "TO_TIMESTAMP") {
		t.Errorf("expected TO_TIMESTAMP for time-containing format, got: %s", r.Definition)
	}
}

func TestConvertView_ToCharFormat(t *testing.T) {
	r := ConvertView("SELECT TO_CHAR(hire_date, 'DD-MON-RRRR HH24:MI:SS') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "'DD-MON-YYYY HH24:MI:SS'") {
		t.Errorf("expected RRRR→YYYY conversion, got: %s", r.Definition)
	}
}

func TestConvertProcedure_PragmaExcInit(t *testing.T) {
	oracle := `DECLARE
    e_custom EXCEPTION;
    PRAGMA EXCEPTION_INIT(e_custom, -20001);
BEGIN
    NULL;
END;`
	r := ConvertProcedure("sp", "PROCEDURE", oracle, NamingKeepOriginal)
	if strings.Contains(r.Definition, "PRAGMA") {
		t.Errorf("PRAGMA EXCEPTION_INIT should be removed, got: %s", r.Definition)
	}
}

func TestConvertProcedure_PragmaAutonomous(t *testing.T) {
	oracle := `PRAGMA AUTONOMOUS_TRANSACTION;
BEGIN
    INSERT INTO log_table VALUES ('test');
    COMMIT;
END;`
	r := ConvertProcedure("sp", "PROCEDURE", oracle, NamingKeepOriginal)
	if strings.Contains(r.Definition, "PRAGMA") {
		t.Errorf("PRAGMA should be removed")
	}
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "AUTONOMOUS_TRANSACTION") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected AUTONOMOUS_TRANSACTION warning, got: %v", r.Warnings)
	}
}

func TestConvertProcedure_Nocopy(t *testing.T) {
	oracle := `CREATE OR REPLACE PROCEDURE sp(p_data IN OUT NOCOPY CLOB) IS
BEGIN NULL; END;`
	r := ConvertProcedure("sp", "PROCEDURE", oracle, NamingKeepOriginal)
	if strings.Contains(r.Definition, "NOCOPY") {
		t.Errorf("NOCOPY should be removed, got: %s", r.Definition)
	}
}

func TestConvertProcedure_Deterministic(t *testing.T) {
	oracle := `CREATE OR REPLACE FUNCTION fn_calc(x NUMBER) RETURN NUMBER DETERMINISTIC IS
BEGIN
    RETURN x * 2;
END;`
	r := ConvertProcedure("fn_calc", "FUNCTION", oracle, NamingKeepOriginal)
	if !strings.Contains(r.Definition, "IMMUTABLE") {
		t.Errorf("DETERMINISTIC should become IMMUTABLE, got: %s", r.Definition)
	}
	if strings.Contains(r.Definition, "DETERMINISTIC") {
		t.Errorf("DETERMINISTIC keyword should be removed")
	}
}

func TestConvertProcedure_AuthidCurrentUser(t *testing.T) {
	oracle := `CREATE OR REPLACE FUNCTION fn_test() RETURN NUMBER AUTHID CURRENT_USER IS
BEGIN RETURN 1; END;`
	r := ConvertProcedure("fn_test", "FUNCTION", oracle, NamingKeepOriginal)
	if !strings.Contains(r.Definition, "SECURITY INVOKER") {
		t.Errorf("AUTHID CURRENT_USER should become SECURITY INVOKER, got: %s", r.Definition)
	}
}

func TestConvertProcedure_ResultCache(t *testing.T) {
	oracle := `CREATE OR REPLACE FUNCTION fn_get RETURN NUMBER RESULT_CACHE IS
BEGIN RETURN 1; END;`
	r := ConvertProcedure("fn_get", "FUNCTION", oracle, NamingKeepOriginal)
	if strings.Contains(r.Definition, "RESULT_CACHE") {
		t.Errorf("RESULT_CACHE should be removed")
	}
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "RESULT_CACHE") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected RESULT_CACHE warning")
	}
}

func TestConvertView_RegexpSubstr(t *testing.T) {
	r := ConvertView("SELECT REGEXP_SUBSTR(col, '[0-9]+') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "substring(col FROM '[0-9]+'") {
		t.Errorf("expected substring(... FROM ...), got: %s", r.Definition)
	}
}

func TestConvertView_RegexpCount(t *testing.T) {
	r := ConvertView("SELECT REGEXP_COUNT(col, '[0-9]+') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "regexp_matches") {
		t.Errorf("expected regexp_matches, got: %s", r.Definition)
	}
}

func TestConvertView_NumToYMInterval(t *testing.T) {
	r := ConvertView("SELECT hire_date + NUMTOYMINTERVAL(2, 'YEAR') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "INTERVAL '1 year'") {
		t.Errorf("expected interval conversion, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DbmsUtilGetTime(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "v_start := DBMS_UTILITY.GET_TIME;", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "clock_timestamp") {
		t.Errorf("expected clock_timestamp, got: %s", r.Definition)
	}
}

func TestConvertProcedure_OpenFetchCloseWarning(t *testing.T) {
	oracle := `OPEN c_emp;
FETCH c_emp INTO v_rec;
CLOSE c_emp;`
	r := ConvertProcedure("sp", "PROCEDURE", oracle, NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "OPEN/FETCH/CLOSE") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected OPEN/FETCH/CLOSE cursor warning, got: %v", r.Warnings)
	}
}

func TestConvertProcedure_SavepointWarning(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "SAVEPOINT before_update;", NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "SAVEPOINT") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected SAVEPOINT warning")
	}
}

func TestConvertProcedure_PipelinedWarning(t *testing.T) {
	oracle := `FUNCTION get_data RETURN t_tab PIPELINED IS
BEGIN
    PIPE ROW(v_rec);
END;`
	r := ConvertProcedure("get_data", "FUNCTION", oracle, NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "PIPELINED") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected PIPELINED warning, got: %v", r.Warnings)
	}
}

func TestConvertProcedure_UtlFileWarning(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "UTL_FILE.FOPEN('/tmp', 'out.csv', 'w');", NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "UTL_FILE") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected UTL_FILE warning")
	}
}

func TestConvertProcedure_DbmsSqlWarning(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "v_cur := DBMS_SQL.OPEN_CURSOR;", NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "DBMS_SQL") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected DBMS_SQL warning")
	}
}

func TestConvertProcedure_DbmsJobWarning(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "DBMS_SCHEDULER.CREATE_JOB('job1');", NamingKeepOriginal)
	hasWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "DBMS_JOB") || strings.Contains(w, "DBMS_SCHEDULER") {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Errorf("expected DBMS_JOB/SCHEDULER warning")
	}
}

func TestConvertProcedure_AdditionalExceptions(t *testing.T) {
	oracle := `EXCEPTION
WHEN LOGIN_DENIED THEN NULL;
WHEN CURSOR_ALREADY_OPEN THEN NULL;
WHEN INVALID_CURSOR THEN NULL;`
	r := ConvertProcedure("sp", "PROCEDURE", oracle, NamingKeepOriginal)
	if !strings.Contains(r.Definition, "insufficient_privilege") {
		t.Errorf("LOGIN_DENIED should → insufficient_privilege, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "duplicate_cursor") {
		t.Errorf("CURSOR_ALREADY_OPEN should → duplicate_cursor, got: %s", r.Definition)
	}
	if !strings.Contains(r.Definition, "invalid_cursor_state") {
		t.Errorf("INVALID_CURSOR should → invalid_cursor_state, got: %s", r.Definition)
	}
}

func TestConvertView_RegexpLikeInsensitive(t *testing.T) {
	r := ConvertView("SELECT * FROM t WHERE REGEXP_LIKE(name, '^A', 'i')", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "~*") {
		t.Errorf("expected ~* for case-insensitive, got: %s", r.Definition)
	}
}

func TestConvertView_ToDateRRRR(t *testing.T) {
	r := ConvertView("SELECT TO_DATE(col, 'DD/MM/RRRR') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "'DD/MM/YYYY'") {
		t.Errorf("expected RRRR→YYYY, got: %s", r.Definition)
	}
}

func TestConvertView_ToCharFractionalSeconds(t *testing.T) {
	r := ConvertView("SELECT TO_CHAR(ts, 'YYYY-MM-DD HH24:MI:SS.FF6') FROM t", NamingKeepOriginal)
	if !strings.Contains(r.Definition, ".US") {
		t.Errorf("expected FF6→US, got: %s", r.Definition)
	}
}

func TestConvertProcedure_DbmsOutputPut(t *testing.T) {
	r := ConvertProcedure("sp", "PROCEDURE", "DBMS_OUTPUT.PUT('hello');", NamingKeepOriginal)
	if !strings.Contains(r.Definition, "RAISE NOTICE") {
		t.Errorf("expected RAISE NOTICE, got: %s", r.Definition)
	}
}
