-- ==================================================
-- Views, Materialized Views, Triggers, Procedures
-- for manual review / PL/SQL → PL/pgSQL testing
-- ==================================================

-- ==================================================
-- 1. Views
-- ==================================================

-- Simple view: active customers with order summary
CREATE OR REPLACE VIEW v_customer_summary AS
SELECT
    c.customer_id,
    c.name,
    c.email,
    c.status,
    COUNT(o.order_id) AS order_count,
    NVL(SUM(o.total_amount), 0) AS total_spent,
    MAX(o.order_date) AS last_order_date
FROM customers c
LEFT JOIN orders o ON o.customer_id = c.customer_id
WHERE c.status = 'A'
GROUP BY c.customer_id, c.name, c.email, c.status;

-- View with Oracle-specific syntax (DECODE, ROWNUM)
CREATE OR REPLACE VIEW v_top_customers AS
SELECT * FROM (
    SELECT
        c.customer_id,
        c.name,
        NVL(SUM(o.total_amount), 0) AS total_spent,
        DECODE(c.status, 'A', 'Active', 'I', 'Inactive', 'S', 'Suspended', 'Unknown') AS status_text,
        RANK() OVER (ORDER BY NVL(SUM(o.total_amount), 0) DESC) AS spending_rank
    FROM customers c
    LEFT JOIN orders o ON o.customer_id = c.customer_id
    GROUP BY c.customer_id, c.name, c.status
)
WHERE ROWNUM <= 100;

-- ==================================================
-- 2. Materialized Views
-- ==================================================

-- MView: daily meter readings aggregate (COMPLETE refresh)
CREATE MATERIALIZED VIEW mv_daily_readings
BUILD IMMEDIATE
REFRESH COMPLETE ON DEMAND
AS
SELECT
    meter_id,
    TRUNC(reading_date) AS reading_day,
    COUNT(*) AS reading_count,
    SUM(value_kwh) AS total_kwh,
    AVG(value_kwh) AS avg_kwh,
    MIN(value_kwh) AS min_kwh,
    MAX(value_kwh) AS max_kwh
FROM meter_readings
GROUP BY meter_id, TRUNC(reading_date);

-- MView: monthly order summary (FORCE refresh)
CREATE MATERIALIZED VIEW mv_monthly_orders
BUILD IMMEDIATE
REFRESH FORCE ON DEMAND
AS
SELECT
    TRUNC(o.order_date, 'MM') AS order_month,
    o.currency,
    COUNT(*) AS order_count,
    SUM(o.total_amount) AS total_revenue,
    COUNT(DISTINCT o.customer_id) AS unique_customers
FROM orders o
GROUP BY TRUNC(o.order_date, 'MM'), o.currency;

-- ==================================================
-- 3. Triggers
-- ==================================================

-- BEFORE INSERT trigger: auto-set timestamps
CREATE OR REPLACE TRIGGER trg_customers_bi
BEFORE INSERT ON customers
FOR EACH ROW
BEGIN
    IF :NEW.created_at IS NULL THEN
        :NEW.created_at := SYSDATE;
    END IF;
    :NEW.updated_at := SYSTIMESTAMP;
    IF :NEW.customer_id IS NULL THEN
        :NEW.customer_id := seq_customers.NEXTVAL;
    END IF;
END;
/

-- BEFORE UPDATE trigger: update timestamp
CREATE OR REPLACE TRIGGER trg_customers_bu
BEFORE UPDATE ON customers
FOR EACH ROW
BEGIN
    :NEW.updated_at := SYSTIMESTAMP;
END;
/

-- AFTER INSERT trigger: audit log style (writes to a pseudo-audit concept)
CREATE OR REPLACE TRIGGER trg_orders_ai
AFTER INSERT ON orders
FOR EACH ROW
DECLARE
    v_customer_name VARCHAR2(200);
BEGIN
    SELECT name INTO v_customer_name
    FROM customers
    WHERE customer_id = :NEW.customer_id;

    -- In a real system this would INSERT into an audit table
    DBMS_OUTPUT.PUT_LINE('New order ' || :NEW.order_id ||
        ' for customer ' || v_customer_name ||
        ' amount: ' || :NEW.total_amount || ' ' || :NEW.currency);
END;
/

-- ==================================================
-- 4. Stored Procedures
-- ==================================================

-- Procedure: place an order with validation
CREATE OR REPLACE PROCEDURE sp_place_order(
    p_customer_id   IN NUMBER,
    p_total_amount  IN NUMBER,
    p_currency      IN VARCHAR2 DEFAULT 'TRY',
    p_description   IN VARCHAR2 DEFAULT NULL,
    p_order_id      OUT NUMBER
) AS
    v_customer_exists NUMBER;
    v_balance NUMBER;
    e_customer_not_found EXCEPTION;
    e_insufficient_balance EXCEPTION;
BEGIN
    -- Check customer exists
    SELECT COUNT(*), NVL(MAX(balance), 0)
    INTO v_customer_exists, v_balance
    FROM customers
    WHERE customer_id = p_customer_id AND status = 'A';

    IF v_customer_exists = 0 THEN
        RAISE e_customer_not_found;
    END IF;

    -- Check balance (if applicable)
    IF v_balance < p_total_amount THEN
        RAISE e_insufficient_balance;
    END IF;

    -- Create order
    p_order_id := seq_orders.NEXTVAL;
    INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
    VALUES (p_order_id, p_customer_id, SYSDATE, p_total_amount, p_currency, p_description);

    -- Deduct balance
    UPDATE customers
    SET balance = balance - p_total_amount
    WHERE customer_id = p_customer_id;

    COMMIT;
EXCEPTION
    WHEN e_customer_not_found THEN
        RAISE_APPLICATION_ERROR(-20001, 'Customer not found or inactive: ' || p_customer_id);
    WHEN e_insufficient_balance THEN
        RAISE_APPLICATION_ERROR(-20002, 'Insufficient balance for customer: ' || p_customer_id);
    WHEN OTHERS THEN
        ROLLBACK;
        RAISE;
END;
/

-- ==================================================
-- 5. Functions
-- ==================================================

-- Function: calculate customer lifetime value
CREATE OR REPLACE FUNCTION fn_customer_ltv(
    p_customer_id IN NUMBER
) RETURN NUMBER
AS
    v_ltv NUMBER;
BEGIN
    SELECT NVL(SUM(total_amount), 0)
    INTO v_ltv
    FROM orders
    WHERE customer_id = p_customer_id;

    RETURN v_ltv;
END;
/

-- Function: format reading value with unit
CREATE OR REPLACE FUNCTION fn_format_reading(
    p_value   IN NUMBER,
    p_unit    IN VARCHAR2 DEFAULT 'kWh'
) RETURN VARCHAR2
AS
BEGIN
    IF p_value IS NULL THEN
        RETURN 'N/A';
    ELSIF p_value >= 1000000 THEN
        RETURN TO_CHAR(p_value / 1000000, 'FM999,999.00') || ' M' || p_unit;
    ELSIF p_value >= 1000 THEN
        RETURN TO_CHAR(p_value / 1000, 'FM999,999.00') || ' K' || p_unit;
    ELSE
        RETURN TO_CHAR(p_value, 'FM999,999.00') || ' ' || p_unit;
    END IF;
END;
/

-- ==================================================
-- 6. Package (header + body)
-- ==================================================

-- Package spec: customer management utilities
CREATE OR REPLACE PACKAGE pkg_customer_mgmt AS
    -- Constants
    STATUS_ACTIVE   CONSTANT CHAR(1) := 'A';
    STATUS_INACTIVE CONSTANT CHAR(1) := 'I';
    STATUS_SUSPENDED CONSTANT CHAR(1) := 'S';

    -- Types
    TYPE t_customer_rec IS RECORD (
        customer_id   customers.customer_id%TYPE,
        name          customers.name%TYPE,
        email         customers.email%TYPE,
        balance       customers.balance%TYPE,
        order_count   NUMBER
    );

    TYPE t_customer_tab IS TABLE OF t_customer_rec;

    -- Procedures
    PROCEDURE activate_customer(p_customer_id IN NUMBER);
    PROCEDURE suspend_customer(p_customer_id IN NUMBER, p_reason IN VARCHAR2);
    PROCEDURE update_balance(p_customer_id IN NUMBER, p_amount IN NUMBER);

    -- Functions
    FUNCTION get_status_text(p_status IN CHAR) RETURN VARCHAR2;
    FUNCTION get_customer_count(p_status IN CHAR DEFAULT NULL) RETURN NUMBER;
END pkg_customer_mgmt;
/

-- Package body
CREATE OR REPLACE PACKAGE BODY pkg_customer_mgmt AS

    PROCEDURE activate_customer(p_customer_id IN NUMBER) AS
    BEGIN
        UPDATE customers
        SET status = STATUS_ACTIVE, updated_at = SYSTIMESTAMP
        WHERE customer_id = p_customer_id;

        IF SQL%ROWCOUNT = 0 THEN
            RAISE_APPLICATION_ERROR(-20010, 'Customer not found: ' || p_customer_id);
        END IF;
        COMMIT;
    END activate_customer;

    PROCEDURE suspend_customer(p_customer_id IN NUMBER, p_reason IN VARCHAR2) AS
    BEGIN
        UPDATE customers
        SET status = STATUS_SUSPENDED, notes = p_reason, updated_at = SYSTIMESTAMP
        WHERE customer_id = p_customer_id;

        IF SQL%ROWCOUNT = 0 THEN
            RAISE_APPLICATION_ERROR(-20010, 'Customer not found: ' || p_customer_id);
        END IF;
        COMMIT;
    END suspend_customer;

    PROCEDURE update_balance(p_customer_id IN NUMBER, p_amount IN NUMBER) AS
        v_current_balance NUMBER;
    BEGIN
        SELECT balance INTO v_current_balance
        FROM customers
        WHERE customer_id = p_customer_id
        FOR UPDATE;

        UPDATE customers
        SET balance = balance + p_amount, updated_at = SYSTIMESTAMP
        WHERE customer_id = p_customer_id;

        COMMIT;
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            RAISE_APPLICATION_ERROR(-20010, 'Customer not found: ' || p_customer_id);
    END update_balance;

    FUNCTION get_status_text(p_status IN CHAR) RETURN VARCHAR2 AS
    BEGIN
        RETURN CASE p_status
            WHEN STATUS_ACTIVE THEN 'Active'
            WHEN STATUS_INACTIVE THEN 'Inactive'
            WHEN STATUS_SUSPENDED THEN 'Suspended'
            ELSE 'Unknown'
        END;
    END get_status_text;

    FUNCTION get_customer_count(p_status IN CHAR DEFAULT NULL) RETURN NUMBER AS
        v_count NUMBER;
    BEGIN
        IF p_status IS NULL THEN
            SELECT COUNT(*) INTO v_count FROM customers;
        ELSE
            SELECT COUNT(*) INTO v_count FROM customers WHERE status = p_status;
        END IF;
        RETURN v_count;
    END get_customer_count;

END pkg_customer_mgmt;
/
