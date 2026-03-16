-- Large seed data generator for performance testing
-- Usage: sqlplus migtest/migtest123@//localhost:1521/FREEPDB1 @03_big_seed.sql
-- Generates ~100K+ rows across tables for benchmarking

SET SERVEROUTPUT ON
SET TIMING ON

-- ==================================================
-- 1. BIG_TABLE: non-partitioned, configurable row count
--    Good for testing ROWID chunking
-- ==================================================
BEGIN
    EXECUTE IMMEDIATE 'DROP TABLE big_table CASCADE CONSTRAINTS' ;
EXCEPTION WHEN OTHERS THEN NULL;
END;
/

CREATE TABLE big_table (
    id            NUMBER(12,0) NOT NULL,
    name          VARCHAR2(100),
    description   VARCHAR2(500),
    amount        NUMBER(15,2),
    status        VARCHAR2(10),
    created_at    DATE,
    updated_at    TIMESTAMP,
    category      VARCHAR2(50),
    ref_code      VARCHAR2(20),
    notes         VARCHAR2(1000),
    CONSTRAINT pk_big_table PRIMARY KEY (id)
);

DECLARE
    v_batch    CONSTANT PLS_INTEGER := 10000;
    v_total    CONSTANT PLS_INTEGER := 100000;
    v_start    TIMESTAMP := SYSTIMESTAMP;

    TYPE t_id   IS TABLE OF NUMBER(12) INDEX BY PLS_INTEGER;
    TYPE t_str  IS TABLE OF VARCHAR2(1000) INDEX BY PLS_INTEGER;
    TYPE t_num  IS TABLE OF NUMBER(15,2) INDEX BY PLS_INTEGER;
    TYPE t_date IS TABLE OF DATE INDEX BY PLS_INTEGER;
    TYPE t_ts   IS TABLE OF TIMESTAMP INDEX BY PLS_INTEGER;

    a_id   t_id;
    a_name t_str;
    a_desc t_str;
    a_amt  t_num;
    a_st   t_str;
    a_cr   t_date;
    a_up   t_ts;
    a_cat  t_str;
    a_ref  t_str;
    a_note t_str;

    v_categories CONSTANT VARCHAR2(200) := 'Elektrik,Dogalgaz,Su,Telekom,Finans,Saglik,Uretim,Lojistik';
    v_statuses   CONSTANT VARCHAR2(50)  := 'ACTIVE,PASSIVE,PENDING,CLOSED';
BEGIN
    DBMS_OUTPUT.PUT_LINE('Generating ' || v_total || ' rows into BIG_TABLE...');

    FOR batch_start IN 0 .. (v_total / v_batch) - 1 LOOP
        FOR i IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := batch_start * v_batch + i;
            BEGIN
                a_id(i)   := v_row;
                a_name(i) := 'Customer_' || v_row || '_' || DBMS_RANDOM.STRING('A', 10);
                a_desc(i) := 'Description for row ' || v_row || ' ' || DBMS_RANDOM.STRING('A', TRUNC(DBMS_RANDOM.VALUE(50, 400)));
                a_amt(i)  := ROUND(DBMS_RANDOM.VALUE(1, 999999), 2);
                a_st(i)   := REGEXP_SUBSTR(v_statuses, '[^,]+', 1, MOD(v_row, 4) + 1);
                a_cr(i)   := DATE '2020-01-01' + TRUNC(DBMS_RANDOM.VALUE(0, 1825));
                a_up(i)   := SYSTIMESTAMP - NUMTODSINTERVAL(DBMS_RANDOM.VALUE(0, 86400 * 365), 'SECOND');
                a_cat(i)  := REGEXP_SUBSTR(v_categories, '[^,]+', 1, MOD(v_row, 8) + 1);
                a_ref(i)  := 'REF-' || LPAD(v_row, 8, '0');
                a_note(i) := CASE WHEN MOD(v_row, 3) = 0 THEN 'Note: ' || DBMS_RANDOM.STRING('A', TRUNC(DBMS_RANDOM.VALUE(100, 800))) ELSE NULL END;
            END;
        END LOOP;

        FORALL i IN 1 .. v_batch
            INSERT INTO big_table (id, name, description, amount, status, created_at, updated_at, category, ref_code, notes)
            VALUES (a_id(i), a_name(i), a_desc(i), a_amt(i), a_st(i), a_cr(i), a_up(i), a_cat(i), a_ref(i), a_note(i));

        COMMIT;
        DBMS_OUTPUT.PUT_LINE('  Batch ' || (batch_start + 1) || '/' || (v_total / v_batch) || ' done (' || ((batch_start + 1) * v_batch) || ' rows)');
    END LOOP;

    DBMS_OUTPUT.PUT_LINE('BIG_TABLE: ' || v_total || ' rows in ' ||
        EXTRACT(SECOND FROM (SYSTIMESTAMP - v_start)) || 's');
END;
/

-- ==================================================
-- 2. BIG_PARTITIONED: range-partitioned by month, ~100K rows
--    Good for testing partition pruning + per-table parallelism
-- ==================================================
BEGIN
    EXECUTE IMMEDIATE 'DROP TABLE big_partitioned CASCADE CONSTRAINTS';
EXCEPTION WHEN OTHERS THEN NULL;
END;
/

CREATE TABLE big_partitioned (
    id            NUMBER(12,0) NOT NULL,
    sensor_id     NUMBER(8,0) NOT NULL,
    reading_date  DATE NOT NULL,
    value_kwh     NUMBER(12,4),
    quality       VARCHAR2(10),
    region        VARCHAR2(50),
    notes         VARCHAR2(500),
    CONSTRAINT pk_big_partitioned PRIMARY KEY (id, reading_date)
)
PARTITION BY RANGE (reading_date) (
    PARTITION p202301 VALUES LESS THAN (DATE '2023-02-01'),
    PARTITION p202302 VALUES LESS THAN (DATE '2023-03-01'),
    PARTITION p202303 VALUES LESS THAN (DATE '2023-04-01'),
    PARTITION p202304 VALUES LESS THAN (DATE '2023-05-01'),
    PARTITION p202305 VALUES LESS THAN (DATE '2023-06-01'),
    PARTITION p202306 VALUES LESS THAN (DATE '2023-07-01'),
    PARTITION p202307 VALUES LESS THAN (DATE '2023-08-01'),
    PARTITION p202308 VALUES LESS THAN (DATE '2023-09-01'),
    PARTITION p202309 VALUES LESS THAN (DATE '2023-10-01'),
    PARTITION p202310 VALUES LESS THAN (DATE '2023-11-01'),
    PARTITION p202311 VALUES LESS THAN (DATE '2023-12-01'),
    PARTITION p202312 VALUES LESS THAN (DATE '2024-01-01'),
    PARTITION p202401 VALUES LESS THAN (DATE '2024-02-01'),
    PARTITION p202402 VALUES LESS THAN (DATE '2024-03-01'),
    PARTITION p202403 VALUES LESS THAN (DATE '2024-04-01'),
    PARTITION p202404 VALUES LESS THAN (DATE '2024-05-01'),
    PARTITION p202405 VALUES LESS THAN (DATE '2024-06-01'),
    PARTITION p202406 VALUES LESS THAN (DATE '2024-07-01'),
    PARTITION p202407 VALUES LESS THAN (DATE '2024-08-01'),
    PARTITION p202408 VALUES LESS THAN (DATE '2024-09-01'),
    PARTITION p202409 VALUES LESS THAN (DATE '2024-10-01'),
    PARTITION p202410 VALUES LESS THAN (DATE '2024-11-01'),
    PARTITION p202411 VALUES LESS THAN (DATE '2024-12-01'),
    PARTITION p202412 VALUES LESS THAN (DATE '2025-01-01')
);

CREATE INDEX idx_big_part_sensor ON big_partitioned(sensor_id) LOCAL;

DECLARE
    v_batch    CONSTANT PLS_INTEGER := 10000;
    v_total    CONSTANT PLS_INTEGER := 120000;  -- ~5K per partition
    v_start    TIMESTAMP := SYSTIMESTAMP;

    TYPE t_num  IS TABLE OF NUMBER INDEX BY PLS_INTEGER;
    TYPE t_str  IS TABLE OF VARCHAR2(500) INDEX BY PLS_INTEGER;
    TYPE t_date IS TABLE OF DATE INDEX BY PLS_INTEGER;

    a_id    t_num;
    a_sid   t_num;
    a_dt    t_date;
    a_val   t_num;
    a_qual  t_str;
    a_reg   t_str;
    a_note  t_str;

    v_regions CONSTANT VARCHAR2(200) := 'Istanbul,Ankara,Izmir,Bursa,Antalya,Adana,Konya,Gaziantep';
BEGIN
    DBMS_OUTPUT.PUT_LINE('Generating ' || v_total || ' rows into BIG_PARTITIONED...');

    FOR batch_start IN 0 .. (v_total / v_batch) - 1 LOOP
        FOR i IN 1 .. v_batch LOOP
            DECLARE
                v_row PLS_INTEGER := batch_start * v_batch + i;
            BEGIN
                a_id(i)   := v_row;
                a_sid(i)  := MOD(v_row, 1000) + 1;
                -- Spread across 24 months (2023-01 to 2024-12)
                a_dt(i)   := DATE '2023-01-01' + TRUNC(DBMS_RANDOM.VALUE(0, 730));
                a_val(i)  := ROUND(DBMS_RANDOM.VALUE(1, 9999), 4);
                a_qual(i) := CASE MOD(v_row, 10) WHEN 0 THEN 'BAD' WHEN 1 THEN 'SUSPECT' ELSE 'OK' END;
                a_reg(i)  := REGEXP_SUBSTR(v_regions, '[^,]+', 1, MOD(v_row, 8) + 1);
                a_note(i) := CASE WHEN MOD(v_row, 5) = 0 THEN 'Sensor note #' || v_row ELSE NULL END;
            END;
        END LOOP;

        FORALL i IN 1 .. v_batch
            INSERT INTO big_partitioned (id, sensor_id, reading_date, value_kwh, quality, region, notes)
            VALUES (a_id(i), a_sid(i), a_dt(i), a_val(i), a_qual(i), a_reg(i), a_note(i));

        COMMIT;
        DBMS_OUTPUT.PUT_LINE('  Batch ' || (batch_start + 1) || '/' || (v_total / v_batch) || ' done');
    END LOOP;

    DBMS_OUTPUT.PUT_LINE('BIG_PARTITIONED: ' || v_total || ' rows in ' ||
        EXTRACT(SECOND FROM (SYSTIMESTAMP - v_start)) || 's');
END;
/

-- ==================================================
-- 3. Pump up existing CUSTOMERS to 10K
-- ==================================================
DECLARE
    v_start TIMESTAMP := SYSTIMESTAMP;
    v_max   NUMBER;
BEGIN
    SELECT NVL(MAX(customer_id), 0) INTO v_max FROM customers;
    IF v_max < 10000 THEN
        FOR i IN (v_max + 1) .. 10000 LOOP
            INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
            VALUES (i,
                    'Customer_' || i,
                    CASE WHEN MOD(i, 3) = 0 THEN NULL ELSE 'cust' || i || '@example.com' END,
                    ROUND(DBMS_RANDOM.VALUE(0, 100000), 2),
                    CASE MOD(i, 5) WHEN 0 THEN 'I' WHEN 1 THEN 'S' ELSE 'A' END,
                    CASE WHEN MOD(i, 4) = 0 THEN 'Note for customer ' || i ELSE NULL END,
                    DATE '2020-01-01' + TRUNC(DBMS_RANDOM.VALUE(0, 1825)));
            IF MOD(i, 5000) = 0 THEN COMMIT; END IF;
        END LOOP;
        COMMIT;
        DBMS_OUTPUT.PUT_LINE('CUSTOMERS pumped to 10K in ' ||
            EXTRACT(SECOND FROM (SYSTIMESTAMP - v_start)) || 's');
    ELSE
        DBMS_OUTPUT.PUT_LINE('CUSTOMERS already has ' || v_max || ' rows, skipping');
    END IF;
END;
/

-- ==================================================
-- 4. Pump up ORDERS to 50K
-- ==================================================
DECLARE
    v_start TIMESTAMP := SYSTIMESTAMP;
    v_max   NUMBER;
BEGIN
    SELECT NVL(MAX(order_id), 0) INTO v_max FROM orders;
    IF v_max < 50000 THEN
        FOR i IN (v_max + 1) .. 50000 LOOP
            INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
            VALUES (i,
                    TRUNC(DBMS_RANDOM.VALUE(1, 10001)),
                    DATE '2024-01-01' + TRUNC(DBMS_RANDOM.VALUE(0, 365)),
                    ROUND(DBMS_RANDOM.VALUE(10, 50000), 2),
                    CASE MOD(i, 3) WHEN 0 THEN 'USD' WHEN 1 THEN 'EUR' ELSE 'TRY' END,
                    CASE WHEN MOD(i, 2) = 0 THEN 'Order #' || i ELSE NULL END);
            IF MOD(i, 5000) = 0 THEN COMMIT; END IF;
        END LOOP;
        COMMIT;
        DBMS_OUTPUT.PUT_LINE('ORDERS pumped to 50K in ' ||
            EXTRACT(SECOND FROM (SYSTIMESTAMP - v_start)) || 's');
    ELSE
        DBMS_OUTPUT.PUT_LINE('ORDERS already has ' || v_max || ' rows, skipping');
    END IF;
END;
/

-- ==================================================
-- Summary
-- ==================================================
PROMPT
PROMPT === Seed Data Summary ===
SELECT table_name, COUNT(*) AS row_count
FROM (
    SELECT 'CUSTOMERS' AS table_name FROM customers UNION ALL
    SELECT 'ORDERS' FROM orders UNION ALL
    SELECT 'PRODUCTS' FROM products UNION ALL
    SELECT 'TYPE_TEST' FROM type_test UNION ALL
    SELECT 'METER_READINGS' FROM meter_readings UNION ALL
    SELECT 'BIG_TABLE' FROM big_table UNION ALL
    SELECT 'BIG_PARTITIONED' FROM big_partitioned
)
GROUP BY table_name
ORDER BY row_count DESC;
