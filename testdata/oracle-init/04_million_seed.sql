-- Million-scale seed data generator
-- Usage: sqlplus migtest/migtest123@//localhost:1521/FREEPDB1 @04_million_seed.sql
-- Generates ~5M+ rows for realistic performance testing

SET SERVEROUTPUT ON
SET TIMING ON

-- ==================================================
-- 1. BIG_TABLE: pump to 2M rows (non-partitioned)
--    Columns: ID, NAME, DESCRIPTION, AMOUNT, STATUS, CREATED_AT, UPDATED_AT, CATEGORY, REF_CODE, NOTES
-- ==================================================
DECLARE
    v_batch    CONSTANT PLS_INTEGER := 50000;
    v_total    CONSTANT PLS_INTEGER := 2000000;
    v_start    PLS_INTEGER;
    v_existing PLS_INTEGER;
    TYPE t_id   IS TABLE OF NUMBER(12)     INDEX BY PLS_INTEGER;
    TYPE t_s100 IS TABLE OF VARCHAR2(100)  INDEX BY PLS_INTEGER;
    TYPE t_s500 IS TABLE OF VARCHAR2(500)  INDEX BY PLS_INTEGER;
    TYPE t_s1k  IS TABLE OF VARCHAR2(1000) INDEX BY PLS_INTEGER;
    TYPE t_num  IS TABLE OF NUMBER(15,2)   INDEX BY PLS_INTEGER;
    TYPE t_dt   IS TABLE OF DATE           INDEX BY PLS_INTEGER;
    TYPE t_s10  IS TABLE OF VARCHAR2(10)   INDEX BY PLS_INTEGER;
    TYPE t_s50  IS TABLE OF VARCHAR2(50)   INDEX BY PLS_INTEGER;
    TYPE t_s20  IS TABLE OF VARCHAR2(20)   INDEX BY PLS_INTEGER;
    a_id   t_id;
    a_name t_s100;
    a_desc t_s500;
    a_amt  t_num;
    a_stat t_s10;
    a_dt   t_dt;
    a_cat  t_s50;
    a_ref  t_s20;
    a_note t_s1k;
BEGIN
    SELECT COUNT(*) INTO v_existing FROM big_table;
    v_start := v_existing + 1;
    IF v_start > v_total THEN
        DBMS_OUTPUT.PUT_LINE('BIG_TABLE already has ' || v_existing || ' rows, skipping');
        RETURN;
    END IF;
    DBMS_OUTPUT.PUT_LINE('BIG_TABLE: inserting from ' || v_start || ' to ' || v_total);

    FOR batch_start IN 0 .. CEIL((v_total - v_start + 1) / v_batch) - 1 LOOP
        a_id.DELETE; a_name.DELETE; a_desc.DELETE; a_amt.DELETE;
        a_stat.DELETE; a_dt.DELETE; a_cat.DELETE; a_ref.DELETE; a_note.DELETE;

        FOR j IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := v_start + batch_start * v_batch + j - 1;
            BEGIN
                EXIT WHEN v_row > v_total;
                a_id(j)   := v_row;
                a_name(j) := 'Cust_' || v_row || '_' || DBMS_RANDOM.STRING('A', 10);
                a_desc(j) := 'Desc_' || DBMS_RANDOM.STRING('A', 40);
                a_amt(j)  := ROUND(DBMS_RANDOM.VALUE(1, 999999), 2);
                a_stat(j) := CASE MOD(v_row, 5) WHEN 0 THEN 'ACTIVE' WHEN 1 THEN 'PENDING' WHEN 2 THEN 'CLOSED' WHEN 3 THEN 'HOLD' ELSE 'NEW' END;
                a_dt(j)   := SYSDATE - DBMS_RANDOM.VALUE(0, 1825);
                a_cat(j)  := 'CAT_' || MOD(v_row, 100);
                a_ref(j)  := 'REF' || LPAD(MOD(v_row, 99999), 5, '0');
                a_note(j) := 'Note_' || DBMS_RANDOM.STRING('A', 80);
            END;
        END LOOP;

        FORALL k IN 1 .. a_id.COUNT
            INSERT INTO big_table (id, name, description, amount, status, created_at, updated_at, category, ref_code, notes)
            VALUES (a_id(k), a_name(k), a_desc(k), a_amt(k), a_stat(k), a_dt(k), SYSTIMESTAMP, a_cat(k), a_ref(k), a_note(k));

        COMMIT;
        IF MOD(batch_start + 1, 5) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  BIG_TABLE: ' || LEAST(v_start + (batch_start + 1) * v_batch - 1, v_total) || ' / ' || v_total);
        END IF;
    END LOOP;
    DBMS_OUTPUT.PUT_LINE('BIG_TABLE done');
END;
/

-- ==================================================
-- 2. BIG_PARTITIONED: pump to 2.4M rows (partitioned by READING_DATE)
--    Columns: ID, SENSOR_ID, READING_DATE, VALUE_KWH, QUALITY, REGION, NOTES
-- ==================================================
DECLARE
    v_batch    CONSTANT PLS_INTEGER := 50000;
    v_total    CONSTANT PLS_INTEGER := 2400000;
    v_start    PLS_INTEGER;
    v_existing PLS_INTEGER;
    TYPE t_id  IS TABLE OF NUMBER(12)     INDEX BY PLS_INTEGER;
    TYPE t_s50 IS TABLE OF VARCHAR2(50)   INDEX BY PLS_INTEGER;
    TYPE t_num IS TABLE OF NUMBER(15,2)   INDEX BY PLS_INTEGER;
    TYPE t_dt  IS TABLE OF DATE           INDEX BY PLS_INTEGER;
    TYPE t_s10 IS TABLE OF VARCHAR2(10)   INDEX BY PLS_INTEGER;
    TYPE t_s200 IS TABLE OF VARCHAR2(200) INDEX BY PLS_INTEGER;
    a_id   t_id;
    a_sens t_id;
    a_dt   t_dt;
    a_val  t_num;
    a_qual t_s10;
    a_reg  t_s50;
    a_note t_s200;
BEGIN
    SELECT COUNT(*) INTO v_existing FROM big_partitioned;
    v_start := v_existing + 1;
    IF v_start > v_total THEN
        DBMS_OUTPUT.PUT_LINE('BIG_PARTITIONED already has ' || v_existing || ' rows, skipping');
        RETURN;
    END IF;
    DBMS_OUTPUT.PUT_LINE('BIG_PARTITIONED: inserting from ' || v_start || ' to ' || v_total);

    FOR batch_start IN 0 .. CEIL((v_total - v_start + 1) / v_batch) - 1 LOOP
        a_id.DELETE; a_sens.DELETE; a_dt.DELETE; a_val.DELETE;
        a_qual.DELETE; a_reg.DELETE; a_note.DELETE;

        FOR j IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := v_start + batch_start * v_batch + j - 1;
                v_month PLS_INTEGER := MOD(v_row - 1, 24);
            BEGIN
                EXIT WHEN v_row > v_total;
                a_id(j)   := v_row;
                a_sens(j) := MOD(v_row, 10000) + 1;
                a_dt(j)   := ADD_MONTHS(DATE '2023-01-01', v_month) + DBMS_RANDOM.VALUE(0, 28);
                a_val(j)  := ROUND(DBMS_RANDOM.VALUE(0.1, 500), 2);
                a_qual(j) := CASE MOD(v_row, 4) WHEN 0 THEN 'GOOD' WHEN 1 THEN 'FAIR' WHEN 2 THEN 'POOR' ELSE 'EST' END;
                a_reg(j)  := 'REGION_' || MOD(v_row, 20);
                a_note(j) := 'Reading_' || DBMS_RANDOM.STRING('A', 30);
            END;
        END LOOP;

        FORALL k IN 1 .. a_id.COUNT
            INSERT INTO big_partitioned (id, sensor_id, reading_date, value_kwh, quality, region, notes)
            VALUES (a_id(k), a_sens(k), a_dt(k), a_val(k), a_qual(k), a_reg(k), a_note(k));

        COMMIT;
        IF MOD(batch_start + 1, 5) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  BIG_PARTITIONED: ' || LEAST(v_start + (batch_start + 1) * v_batch - 1, v_total) || ' / ' || v_total);
        END IF;
    END LOOP;
    DBMS_OUTPUT.PUT_LINE('BIG_PARTITIONED done');
END;
/

-- ==================================================
-- 3. ORDERS: pump to 1M rows
--    Columns: ORDER_ID, CUSTOMER_ID, ORDER_DATE, TOTAL_AMOUNT, CURRENCY, DESCRIPTION
-- ==================================================
DECLARE
    v_batch    CONSTANT PLS_INTEGER := 50000;
    v_total    CONSTANT PLS_INTEGER := 1000000;
    v_start    PLS_INTEGER;
    v_existing PLS_INTEGER;
    TYPE t_id  IS TABLE OF NUMBER        INDEX BY PLS_INTEGER;
    TYPE t_num IS TABLE OF NUMBER(12,2)  INDEX BY PLS_INTEGER;
    TYPE t_dt  IS TABLE OF DATE          INDEX BY PLS_INTEGER;
    TYPE t_s10 IS TABLE OF VARCHAR2(10)  INDEX BY PLS_INTEGER;
    TYPE t_s200 IS TABLE OF VARCHAR2(200) INDEX BY PLS_INTEGER;
    a_id   t_id;
    a_cust t_id;
    a_dt   t_dt;
    a_amt  t_num;
    a_cur  t_s10;
    a_desc t_s200;
BEGIN
    SELECT NVL(MAX(order_id), 0) INTO v_existing FROM orders;
    v_start := v_existing + 1;
    IF v_start > v_total THEN
        DBMS_OUTPUT.PUT_LINE('ORDERS already has enough rows, skipping');
        RETURN;
    END IF;
    DBMS_OUTPUT.PUT_LINE('ORDERS: inserting from ' || v_start || ' to ' || v_total);

    FOR batch_start IN 0 .. CEIL((v_total - v_start + 1) / v_batch) - 1 LOOP
        a_id.DELETE; a_cust.DELETE; a_dt.DELETE;
        a_amt.DELETE; a_cur.DELETE; a_desc.DELETE;

        FOR j IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := v_start + batch_start * v_batch + j - 1;
            BEGIN
                EXIT WHEN v_row > v_total;
                a_id(j)   := v_row;
                a_cust(j) := MOD(v_row, 10000) + 1;
                a_dt(j)   := SYSDATE - DBMS_RANDOM.VALUE(0, 730);
                a_amt(j)  := ROUND(DBMS_RANDOM.VALUE(5, 5000), 2);
                a_cur(j)  := CASE MOD(v_row, 3) WHEN 0 THEN 'USD' WHEN 1 THEN 'EUR' ELSE 'TRY' END;
                a_desc(j) := 'Order_' || v_row || '_' || DBMS_RANDOM.STRING('A', 20);
            END;
        END LOOP;

        FORALL k IN 1 .. a_id.COUNT
            INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
            VALUES (a_id(k), a_cust(k), a_dt(k), a_amt(k), a_cur(k), a_desc(k));

        COMMIT;
        IF MOD(batch_start + 1, 5) = 0 THEN
            DBMS_OUTPUT.PUT_LINE('  ORDERS: ' || LEAST(v_start + (batch_start + 1) * v_batch - 1, v_total) || ' / ' || v_total);
        END IF;
    END LOOP;

    -- Update sequence
    DECLARE v_val NUMBER;
    BEGIN
        SELECT NVL(MAX(order_id), 0) + 1 INTO v_val FROM orders;
        EXECUTE IMMEDIATE 'DROP SEQUENCE seq_orders';
        EXECUTE IMMEDIATE 'CREATE SEQUENCE seq_orders START WITH ' || v_val || ' INCREMENT BY 1 NOCACHE';
    EXCEPTION WHEN OTHERS THEN NULL;
    END;
    DBMS_OUTPUT.PUT_LINE('ORDERS done');
END;
/

-- ==================================================
-- 4. CUSTOMERS: pump to 200K rows
--    Columns: CUSTOMER_ID, NAME, EMAIL, BALANCE, STATUS, NOTES, AVATAR, CREATED_AT, UPDATED_AT
-- ==================================================
DECLARE
    v_batch    CONSTANT PLS_INTEGER := 50000;
    v_total    CONSTANT PLS_INTEGER := 200000;
    v_start    PLS_INTEGER;
    v_existing PLS_INTEGER;
    TYPE t_id  IS TABLE OF NUMBER         INDEX BY PLS_INTEGER;
    TYPE t_s100 IS TABLE OF VARCHAR2(100) INDEX BY PLS_INTEGER;
    TYPE t_num IS TABLE OF NUMBER(12,2)   INDEX BY PLS_INTEGER;
    TYPE t_s20 IS TABLE OF VARCHAR2(20)   INDEX BY PLS_INTEGER;
    TYPE t_s500 IS TABLE OF VARCHAR2(500) INDEX BY PLS_INTEGER;
    TYPE t_dt  IS TABLE OF DATE           INDEX BY PLS_INTEGER;
    a_id    t_id;
    a_name  t_s100;
    a_email t_s100;
    a_bal   t_num;
    a_stat  t_s20;
    a_note  t_s500;
    a_dt    t_dt;
BEGIN
    SELECT NVL(MAX(customer_id), 0) INTO v_existing FROM customers;
    v_start := v_existing + 1;
    IF v_start > v_total THEN
        DBMS_OUTPUT.PUT_LINE('CUSTOMERS already has enough rows, skipping');
        RETURN;
    END IF;
    DBMS_OUTPUT.PUT_LINE('CUSTOMERS: inserting from ' || v_start || ' to ' || v_total);

    FOR batch_start IN 0 .. CEIL((v_total - v_start + 1) / v_batch) - 1 LOOP
        a_id.DELETE; a_name.DELETE; a_email.DELETE;
        a_bal.DELETE; a_stat.DELETE; a_note.DELETE; a_dt.DELETE;

        FOR j IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := v_start + batch_start * v_batch + j - 1;
            BEGIN
                EXIT WHEN v_row > v_total;
                a_id(j)    := v_row;
                a_name(j)  := 'Customer_' || v_row || '_' || DBMS_RANDOM.STRING('A', 8);
                a_email(j) := 'user' || v_row || '@test' || MOD(v_row, 50) || '.com';
                a_bal(j)   := ROUND(DBMS_RANDOM.VALUE(0, 99999), 2);
                a_stat(j)  := CASE MOD(v_row, 3) WHEN 0 THEN 'A' WHEN 1 THEN 'I' ELSE 'S' END;
                a_note(j)  := 'CustNote_' || DBMS_RANDOM.STRING('A', 30);
                a_dt(j)    := SYSDATE - DBMS_RANDOM.VALUE(0, 1825);
            END;
        END LOOP;

        FORALL k IN 1 .. a_id.COUNT
            INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at, updated_at)
            VALUES (a_id(k), a_name(k), a_email(k), a_bal(k), a_stat(k), TO_CLOB(a_note(k)), a_dt(k), SYSDATE);

        COMMIT;
    END LOOP;

    DECLARE v_val NUMBER;
    BEGIN
        SELECT NVL(MAX(customer_id), 0) + 1 INTO v_val FROM customers;
        EXECUTE IMMEDIATE 'DROP SEQUENCE seq_customers';
        EXECUTE IMMEDIATE 'CREATE SEQUENCE seq_customers START WITH ' || v_val || ' INCREMENT BY 1 NOCACHE';
    EXCEPTION WHEN OTHERS THEN NULL;
    END;
    DBMS_OUTPUT.PUT_LINE('CUSTOMERS done');
END;
/

-- ==================================================
-- Gather stats
-- ==================================================
BEGIN
    DBMS_STATS.GATHER_SCHEMA_STATS('MIGTEST');
    DBMS_OUTPUT.PUT_LINE('Schema stats gathered');
END;
/

-- Summary
COL TABLE_NAME FORMAT A25
COL NUM_ROWS FORMAT 999,999,999
COL BLOCKS FORMAT 999,999
COL SIZE_MB FORMAT 999,999.9
SELECT table_name, num_rows, blocks,
       ROUND(blocks * 8 / 1024, 1) AS size_mb
FROM all_tables
WHERE owner = 'MIGTEST'
ORDER BY num_rows DESC NULLS LAST;

EXIT;
