package plsql

import (
	"strings"
	"testing"
)

// Test 1: Order Processing Procedure - cursors, NVL, DECODE, sequences, exceptions, DBMS_OUTPUT, SYSDATE
func TestComplex_OrderProcessing(t *testing.T) {
	oracle := `CREATE OR REPLACE PROCEDURE process_pending_orders (
    p_region_id   IN NUMBER,
    p_cutoff_date IN DATE DEFAULT SYSDATE - 30
) AS
    CURSOR c_orders IS
        SELECT o.order_id, o.customer_id, o.order_date, o.status,
               NVL(o.discount_pct, 0) AS discount_pct,
               DECODE(o.priority, 'H', 1, 'M', 2, 'L', 3, 99) AS priority_rank,
               c.credit_limit, c.customer_name
        FROM orders o
        JOIN customers c ON c.customer_id = o.customer_id
        WHERE o.region_id = p_region_id
          AND o.status = 'PENDING'
          AND o.order_date >= p_cutoff_date
        ORDER BY DECODE(o.priority, 'H', 1, 'M', 2, 'L', 3, 99);
    v_total_processed   NUMBER := 0;
    v_total_amount      NUMBER;
    v_new_status        VARCHAR2(20);
    v_log_id            NUMBER;
    e_credit_exceeded   EXCEPTION;
BEGIN
    DBMS_OUTPUT.PUT_LINE('Processing orders for region ' || p_region_id);

    FOR rec IN c_orders LOOP
        BEGIN
            SELECT SUM(NVL(line_total, 0))
            INTO v_total_amount
            FROM order_lines
            WHERE order_id = rec.order_id;

            IF v_total_amount > rec.credit_limit THEN
                RAISE_APPLICATION_ERROR(-20100, 'Credit limit exceeded');
            END IF;

            v_new_status := DECODE(rec.priority_rank, 1, 'EXPEDITED', 2, 'APPROVED', 'QUEUED');

            SELECT order_log_seq.NEXTVAL INTO v_log_id FROM DUAL;

            INSERT INTO order_log (log_id, order_id, changed_by, changed_at)
            VALUES (v_log_id, rec.order_id, USER, SYSDATE);

            UPDATE orders SET status = v_new_status, processed_date = SYSDATE
            WHERE order_id = rec.order_id;

        EXCEPTION
            WHEN e_credit_exceeded THEN
                UPDATE orders SET status = 'HOLD' WHERE order_id = rec.order_id;
                DBMS_OUTPUT.PUT_LINE('HOLD: Order ' || rec.order_id || ' - ' || SQLERRM);
            WHEN NO_DATA_FOUND THEN
                DBMS_OUTPUT.PUT_LINE('No lines for order ' || rec.order_id);
            WHEN OTHERS THEN
                DBMS_OUTPUT.PUT_LINE('Error: ' || SQLCODE || ' - ' || SQLERRM);
                ROLLBACK;
        END;
    END LOOP;
    COMMIT;
END;`

	r := ConvertProcedure("process_pending_orders", "PROCEDURE", oracle, NamingLowercase)

	checks := map[string]string{
		"COALESCE":         "NVL → COALESCE",
		"CASE":             "DECODE → CASE",
		"CURRENT_DATE":     "SYSDATE → CURRENT_DATE",
		"nextval":          "seq.NEXTVAL → nextval()",
		"RAISE EXCEPTION":  "RAISE_APPLICATION_ERROR → RAISE EXCEPTION",
		"RAISE NOTICE":     "DBMS_OUTPUT → RAISE NOTICE",
		"NUMERIC":          "NUMBER → NUMERIC",
		"VARCHAR":          "VARCHAR2 → VARCHAR",
	}

	for expected, desc := range checks {
		if !strings.Contains(r.Definition, expected) {
			t.Errorf("%s: expected %q in output.\nGot:\n%s", desc, expected, r.Definition[:min(500, len(r.Definition))])
		}
	}

	// FROM DUAL should be removed
	if strings.Contains(r.Definition, "DUAL") {
		t.Errorf("FROM DUAL should be removed")
	}

	// Identifiers should be lowercase
	if strings.Contains(r.Definition, "ORDER_LOG") {
		t.Errorf("identifiers should be lowercase, found ORDER_LOG")
	}

	t.Logf("Converted output:\n%s", r.Definition)
	t.Logf("Warnings: %v", r.Warnings)
}

// Test 2: Full Audit Trigger - INSERTING/UPDATING/DELETING, :NEW/:OLD, SYS_CONTEXT, SYSTIMESTAMP
func TestComplex_AuditTrigger(t *testing.T) {
	oracle := `BEGIN
    v_app_user := NVL(SYS_CONTEXT('USERENV', 'SESSION_USER'), 'unknown');
    v_ip_addr  := SYS_CONTEXT('USERENV', 'IP_ADDRESS');

    IF INSERTING THEN
        v_action := 'INSERT';
        v_new_data := 'EMP_ID=' || :NEW.employee_id ||
                      '|NAME=' || :NEW.first_name || ' ' || :NEW.last_name ||
                      '|SAL=' || NVL(TO_CHAR(:NEW.salary), 'NULL');
    ELSIF UPDATING THEN
        v_action := 'UPDATE';
        v_old_data := 'SAL=' || NVL(TO_CHAR(:OLD.salary), 'NULL');
        v_new_data := 'SAL=' || NVL(TO_CHAR(:NEW.salary), 'NULL');
    ELSIF DELETING THEN
        v_action := 'DELETE';
        v_old_data := 'EMP_ID=' || :OLD.employee_id;
    END IF;

    INSERT INTO audit_trail (
        audit_id, table_name, action, old_values, new_values,
        changed_by, change_ip, changed_at
    ) VALUES (
        audit_seq.NEXTVAL, 'EMPLOYEES',
        v_action, v_old_data, v_new_data,
        v_app_user, v_ip_addr, SYSTIMESTAMP
    );
    COMMIT;
END;`

	r := ConvertTrigger("trg_employees_audit", "employees", "AFTER EACH ROW", "INSERT OR UPDATE OR DELETE", oracle, NamingLowercase)

	checks := map[string]string{
		"TG_OP = 'INSERT'":    "INSERTING → TG_OP",
		"TG_OP = 'UPDATE'":    "UPDATING → TG_OP",
		"TG_OP = 'DELETE'":    "DELETING → TG_OP",
		"NEW.employee_id":     ":NEW → NEW",
		"OLD.employee_id":     ":OLD → OLD",
		"session_user":        "SYS_CONTEXT SESSION_USER",
		"inet_client_addr()":  "SYS_CONTEXT IP_ADDRESS",
		"CURRENT_TIMESTAMP":   "SYSTIMESTAMP → CURRENT_TIMESTAMP",
		"nextval('audit_seq')": "seq.NEXTVAL → nextval()",
		"COALESCE":            "NVL → COALESCE",
		"RETURNS TRIGGER":     "trigger function wrapper",
		"EXECUTE FUNCTION":    "CREATE TRIGGER",
	}

	for expected, desc := range checks {
		if !strings.Contains(r.Definition, expected) {
			t.Errorf("%s: expected %q", desc, expected)
		}
	}

	// :NEW/:OLD should not remain
	if strings.Contains(r.Definition, ":NEW") || strings.Contains(r.Definition, ":OLD") {
		t.Errorf(":NEW/:OLD should be converted")
	}

	t.Logf("Converted output:\n%s", r.Definition)
	t.Logf("Warnings: %v", r.Warnings)
}

// Test 3: Hierarchical View - CONNECT BY → should warn
func TestComplex_HierarchicalView(t *testing.T) {
	oracle := `SELECT
    employee_id,
    LPAD(' ', (LEVEL - 1) * 4) || last_name AS org_tree,
    first_name || ' ' || last_name AS full_name,
    LEVEL AS depth,
    SYS_CONNECT_BY_PATH(last_name, ' / ') AS mgmt_chain,
    DECODE(CONNECT_BY_ISLEAF, 1, 'Individual', 'Manager') AS role_type,
    NVL(manager_id, -1) AS manager_id,
    salary,
    DECODE(job_id, 'AD_PRES', 'Executive', 'IT_PROG', 'Engineering', 'Other') AS job_family,
    NVL2(commission_pct, salary + salary * commission_pct, salary) AS total_comp,
    ROW_NUMBER() OVER (PARTITION BY department_id ORDER BY salary DESC) AS dept_rank,
    ROUND(MONTHS_BETWEEN(SYSDATE, hire_date) / 12, 1) AS years_service
FROM employees
START WITH manager_id IS NULL
CONNECT BY PRIOR employee_id = manager_id
ORDER SIBLINGS BY last_name`

	r := ConvertView(oracle, NamingLowercase)

	// Should have CONNECT BY warning
	hasConnectByWarning := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "CONNECT BY") {
			hasConnectByWarning = true
		}
	}
	if !hasConnectByWarning {
		t.Errorf("expected CONNECT BY warning, got: %v", r.Warnings)
	}

	// Basic conversions should still work
	if !strings.Contains(r.Definition, "COALESCE") {
		t.Errorf("NVL should be converted to COALESCE")
	}
	if !strings.Contains(r.Definition, "CASE") {
		t.Errorf("DECODE should be converted to CASE")
	}
	if !strings.Contains(r.Definition, "CURRENT_DATE") {
		t.Errorf("SYSDATE should be converted")
	}

	// NVL2 conversion
	if !strings.Contains(r.Definition, "CASE WHEN") {
		t.Errorf("NVL2 should be converted to CASE WHEN")
	}

	t.Logf("Converted output:\n%s", r.Definition)
	t.Logf("Warnings: %v", r.Warnings)
}

// Test 4: BULK COLLECT + FORALL → warnings
func TestComplex_BulkCollectForall(t *testing.T) {
	oracle := `CREATE OR REPLACE PROCEDURE sync_customer_balances (
    p_batch_date IN DATE DEFAULT TRUNC(SYSDATE)
) AS
    CURSOR c_stale IS
        SELECT c.customer_id, NVL(c.credit_limit, 0) AS credit_limit,
               NVL(s.total_balance, 0) AS total_balance
        FROM customers c
        LEFT JOIN (
            SELECT customer_id, SUM(NVL(amount, 0)) AS total_balance
            FROM transactions
            WHERE txn_date >= ADD_MONTHS(p_batch_date, -12)
            GROUP BY customer_id
        ) s ON s.customer_id = c.customer_id;

    TYPE t_cust_tab IS TABLE OF c_stale%ROWTYPE INDEX BY PLS_INTEGER;
    l_customers   t_cust_tab;
BEGIN
    OPEN c_stale;
    LOOP
        FETCH c_stale BULK COLLECT INTO l_customers LIMIT 5000;
        EXIT WHEN l_customers.COUNT = 0;

        FORALL i IN 1 .. l_customers.COUNT
            UPDATE customers
            SET current_balance = l_customers(i).total_balance,
                last_sync_date  = p_batch_date,
                updated_at      = SYSDATE
            WHERE customer_id = l_customers(i).customer_id;

        COMMIT;
    END LOOP;
    CLOSE c_stale;
END;`

	r := ConvertProcedure("sync_customer_balances", "PROCEDURE", oracle, NamingLowercase)

	// BULK COLLECT and FORALL should have warnings
	hasBulk := false
	hasForall := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "BULK COLLECT") {
			hasBulk = true
		}
		if strings.Contains(w, "FORALL") {
			hasForall = true
		}
	}
	if !hasBulk {
		t.Errorf("expected BULK COLLECT warning")
	}
	if !hasForall {
		t.Errorf("expected FORALL warning")
	}

	// Basic conversions should still work
	if !strings.Contains(r.Definition, "COALESCE") {
		t.Errorf("NVL should be converted")
	}
	if !strings.Contains(r.Definition, "INTERVAL") {
		t.Errorf("ADD_MONTHS should be converted")
	}
	if !strings.Contains(r.Definition, "INTEGER") {
		t.Errorf("PLS_INTEGER should be converted")
	}

	t.Logf("Warnings: %v", r.Warnings)
}

// Test 5: Package with PIPELINED, %TYPE → warnings
func TestComplex_PipelinedPackage(t *testing.T) {
	oracle := `PACKAGE BODY pkg_employee_reports AS
    FUNCTION get_emp_summary (
        p_dept_id   IN NUMBER DEFAULT NULL,
        p_min_sal   IN NUMBER DEFAULT 0
    ) RETURN t_emp_summary_tab PIPELINED IS
        CURSOR c_emps IS
            SELECT e.employee_id, e.first_name, e.salary, e.hire_date,
                   e.commission_pct, d.department_name
            FROM employees e
            JOIN departments d ON d.department_id = e.department_id
            WHERE (p_dept_id IS NULL OR e.department_id = p_dept_id)
              AND e.salary >= p_min_sal;
        v_rec   t_emp_summary_rec;
    BEGIN
        FOR emp IN c_emps LOOP
            v_rec.employee_id   := emp.employee_id;
            v_rec.full_name     := INITCAP(emp.first_name || ' ' || emp.last_name);
            v_rec.years_service := TRUNC(MONTHS_BETWEEN(SYSDATE, emp.hire_date) / 12);
            v_rec.salary_grade  := DECODE(
                SIGN(emp.salary - 10000), -1, 'LOW',
                DECODE(SIGN(emp.salary - 20000), -1, 'MID', 'HIGH')
            );
            v_rec.annual_bonus  := emp.salary * NVL(emp.commission_pct, 0.05);
            PIPE ROW (v_rec);
        END LOOP;
        RETURN;
    END get_emp_summary;

    PROCEDURE print_department_stats (p_dept_id IN NUMBER) IS
    BEGIN
        DBMS_OUTPUT.PUT_LINE('Department stats for ' || p_dept_id);
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            DBMS_OUTPUT.PUT_LINE('Not found.');
    END print_department_stats;
END pkg_employee_reports;`

	r := ConvertProcedure("pkg_employee_reports", "PACKAGE BODY", oracle, NamingLowercase)

	// Should have package warning
	hasPkg := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "PACKAGE") {
			hasPkg = true
		}
	}
	if !hasPkg {
		t.Errorf("expected PACKAGE warning")
	}

	// DECODE conversions (nested)
	caseCount := strings.Count(r.Definition, "CASE")
	if caseCount < 2 {
		t.Errorf("expected at least 2 CASE conversions for nested DECODE, got %d", caseCount)
	}

	// Basic conversions
	if !strings.Contains(r.Definition, "COALESCE") {
		t.Errorf("NVL should be converted")
	}
	if !strings.Contains(r.Definition, "RAISE NOTICE") {
		t.Errorf("DBMS_OUTPUT should be converted")
	}

	t.Logf("Converted output (first 800):\n%s", r.Definition[:min(800, len(r.Definition))])
	t.Logf("Warnings: %v", r.Warnings)
}

// Test 6: Complex view with CTE, DECODE nested, NVL2, LISTAGG, MONTHS_BETWEEN, ADD_MONTHS
func TestComplex_Customer360View(t *testing.T) {
	oracle := `WITH customer_orders AS (
    SELECT
        c.customer_id,
        c.customer_name,
        c.registration_date,
        NVL(c.segment, 'UNKNOWN') AS segment,
        o.order_id,
        NVL(o.total_amount, 0) AS total_amount,
        DECODE(o.status,
            'COMPLETED', 1,
            'SHIPPED', 0.9,
            'PROCESSING', 0.5,
            'CANCELLED', 0,
            0.1) AS fulfillment_score
    FROM customers c
    LEFT JOIN orders o ON o.customer_id = c.customer_id
        AND o.order_date >= ADD_MONTHS(SYSDATE, -24)
),
customer_agg AS (
    SELECT
        customer_id, customer_name, segment, registration_date,
        COUNT(order_id) AS order_count,
        NVL(SUM(total_amount), 0) AS lifetime_value,
        MAX(order_date) AS last_order_date,
        ROUND(MONTHS_BETWEEN(SYSDATE, registration_date) / 12, 1) AS years_as_customer
    FROM customer_orders
    GROUP BY customer_id, customer_name, segment, registration_date
)
SELECT
    customer_id, customer_name, segment, order_count, lifetime_value,
    DECODE(
        SIGN(order_count - 10), 1,
        DECODE(SIGN(lifetime_value - 50000), 1, 'PLATINUM', 'GOLD'),
        DECODE(SIGN(order_count - 3), 1, 'SILVER', 'BRONZE')
    ) AS loyalty_tier,
    NVL2(last_order_date,
         CASE
             WHEN SYSDATE - last_order_date <= 30 THEN 'ACTIVE'
             WHEN SYSDATE - last_order_date <= 90 THEN 'WARM'
             ELSE 'DORMANT'
         END,
         'NEVER_ORDERED') AS engagement_status,
    NTILE(10) OVER (ORDER BY lifetime_value DESC) AS value_decile
FROM customer_agg`

	r := ConvertView(oracle, NamingLowercase)

	// Nested DECODE → nested CASE
	caseCount := strings.Count(r.Definition, "CASE")
	if caseCount < 3 {
		t.Errorf("expected 3+ CASE for nested DECODE + CASE WHEN, got %d", caseCount)
	}

	// NVL → COALESCE
	if strings.Contains(strings.ToUpper(r.Definition), "NVL(") {
		t.Errorf("NVL should be fully converted")
	}

	// NVL2 → CASE WHEN IS NOT NULL
	if !strings.Contains(r.Definition, "IS NOT NULL") {
		t.Errorf("NVL2 should be converted to IS NOT NULL check")
	}

	// ADD_MONTHS → interval
	if !strings.Contains(r.Definition, "INTERVAL") {
		t.Errorf("ADD_MONTHS should be converted")
	}

	// MONTHS_BETWEEN → EXTRACT + age
	if !strings.Contains(r.Definition, "age") {
		t.Errorf("MONTHS_BETWEEN should be converted")
	}

	// SYSDATE → CURRENT_DATE
	if strings.Contains(strings.ToUpper(r.Definition), "SYSDATE") {
		t.Errorf("SYSDATE should be fully converted")
	}

	// Identifiers lowercase
	if strings.Contains(r.Definition, "CUSTOMER_ID") {
		t.Errorf("identifiers should be lowercase")
	}

	t.Logf("Converted output:\n%s", r.Definition)
}

// Test 7: Trigger with UPDATING('column'), complex :NEW/:OLD, SYS_CONTEXT
func TestComplex_SalaryControlTrigger(t *testing.T) {
	oracle := `BEGIN
    IF UPDATING AND :OLD.salary IS NOT NULL AND :NEW.salary != :OLD.salary THEN
        v_pct_change := (:NEW.salary - :OLD.salary) / :OLD.salary;

        IF v_pct_change > 0.25 THEN
            SELECT COUNT(*) INTO v_is_hr
            FROM user_role_privs
            WHERE username = SYS_CONTEXT('USERENV', 'SESSION_USER');

            IF v_is_hr = 0 THEN
                RAISE_APPLICATION_ERROR(-20300,
                    'Salary increase of ' || TO_CHAR(v_pct_change * 100, '990.0') || '% too high.');
            END IF;
        END IF;

        :NEW.last_salary_change := SYSDATE;
        :NEW.salary_change_count := NVL(:OLD.salary_change_count, 0) + 1;
    END IF;

    :NEW.updated_at := SYSTIMESTAMP;
    :NEW.updated_by := NVL(SYS_CONTEXT('USERENV', 'SESSION_USER'), USER);
END;`

	r := ConvertTrigger("trg_salary_control", "employees", "BEFORE EACH ROW", "UPDATE", oracle, NamingLowercase)

	// All :NEW/:OLD should be converted
	if strings.Contains(r.Definition, ":NEW") || strings.Contains(r.Definition, ":OLD") {
		t.Errorf(":NEW/:OLD should be converted")
	}

	// TG_OP for UPDATING
	if !strings.Contains(r.Definition, "TG_OP = 'UPDATE'") {
		t.Errorf("UPDATING should convert to TG_OP")
	}

	// SYS_CONTEXT → session_user
	if !strings.Contains(r.Definition, "session_user") {
		t.Errorf("SYS_CONTEXT SESSION_USER should convert")
	}

	// RAISE_APPLICATION_ERROR → RAISE EXCEPTION
	if !strings.Contains(r.Definition, "RAISE EXCEPTION") {
		t.Errorf("RAISE_APPLICATION_ERROR should convert")
	}

	t.Logf("Converted trigger:\n%s", r.Definition)
}

// Test 8: Procedure with DBMS_LOB, EMPTY_CLOB, EXECUTE IMMEDIATE
func TestComplex_ArchiveWithDbmsLob(t *testing.T) {
	oracle := `BEGIN
    DBMS_LOB.CREATETEMPORARY(v_clob, TRUE);
    DBMS_LOB.WRITEAPPEND(v_clob, LENGTH('test'), 'test');
    v_empty := EMPTY_CLOB();
    v_len := DBMS_LOB.GETLENGTH(v_data);
    DBMS_LOCK.SLEEP(5);
    EXECUTE IMMEDIATE v_sql;
    DBMS_LOB.FREETEMPORARY(v_clob);
END;`

	r := ConvertProcedure("sp_archive", "PROCEDURE", oracle, NamingLowercase)

	if !strings.Contains(r.Definition, "octet_length(") {
		t.Errorf("DBMS_LOB.GETLENGTH should convert to octet_length")
	}
	if !strings.Contains(r.Definition, "pg_sleep(5)") {
		t.Errorf("DBMS_LOCK.SLEEP should convert to pg_sleep")
	}
	if !strings.Contains(r.Definition, "EXECUTE v_sql") {
		t.Errorf("EXECUTE IMMEDIATE should convert to EXECUTE")
	}
	if !strings.Contains(r.Definition, "''") {
		t.Errorf("EMPTY_CLOB() should convert to ''")
	}

	t.Logf("Converted output:\n%s", r.Definition)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
