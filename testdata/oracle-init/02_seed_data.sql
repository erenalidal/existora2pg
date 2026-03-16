-- Seed data for integration tests
-- Small dataset for correctness testing, not performance

-- ==================================================
-- Customers (10 rows)
-- ==================================================
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (1, 'Ahmet Yilmaz', 'ahmet@example.com', 1500.50, 'A', 'VIP müşteri', DATE '2023-01-15');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (2, 'Elif Kaya', 'elif@example.com', 2300.00, 'A', NULL, DATE '2023-02-20');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (3, 'Mehmet Demir', NULL, 0, 'I', 'Hesap kapatıldı', DATE '2023-03-10');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (4, 'Zeynep Arslan', 'zeynep@example.com', 890.75, 'A', NULL, DATE '2023-04-05');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (5, 'Can Ozturk', 'can@example.com', 50000.00, 'A', 'Kurumsal hesap', DATE '2023-05-12');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (6, 'Ayse Celik', 'ayse@example.com', 320.10, 'A', NULL, DATE '2023-06-18');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (7, 'Burak Sahin', NULL, 0, 'S', 'Askıya alındı', DATE '2023-07-22');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (8, 'Deniz Yildiz', 'deniz@example.com', 15600.00, 'A', NULL, DATE '2023-08-30');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (9, 'Fatma Acar', 'fatma@example.com', 4200.25, 'A', 'İndirim hakkı var', DATE '2023-09-14');
INSERT INTO customers (customer_id, name, email, balance, status, notes, created_at)
VALUES (10, 'Gokhan Tas', 'gokhan@example.com', 780.00, 'A', NULL, DATE '2023-10-01');

-- ==================================================
-- Orders (20 rows)
-- ==================================================
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (1, 1, DATE '2024-01-05', 250.00, 'TRY', 'İlk sipariş');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (2, 1, DATE '2024-01-15', 1200.50, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (3, 2, DATE '2024-02-01', 89.90, 'TRY', 'Küçük sipariş');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (4, 2, DATE '2024-02-14', 3500.00, 'USD', 'Yurtdışı sipariş');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (5, 4, DATE '2024-03-01', 670.25, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (6, 5, DATE '2024-03-10', 45000.00, 'TRY', 'Toplu alım');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (7, 5, DATE '2024-03-20', 12000.00, 'EUR', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (8, 6, DATE '2024-04-01', 155.00, 'TRY', 'Deneme siparişi');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (9, 8, DATE '2024-04-15', 8900.00, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (10, 8, DATE '2024-05-01', 2200.75, 'TRY', 'Aylık sipariş');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (11, 9, DATE '2024-05-10', 560.00, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (12, 9, DATE '2024-06-01', 3400.00, 'TRY', 'İndirimli');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (13, 10, DATE '2024-06-15', 120.50, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (14, 1, DATE '2024-07-01', 5600.00, 'TRY', 'Büyük sipariş');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (15, 4, DATE '2024-07-20', 890.00, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (16, 5, DATE '2024-08-01', 23000.00, 'TRY', 'Q3 toplu');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (17, 6, DATE '2024-08-15', 445.00, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (18, 2, DATE '2024-09-01', 1100.00, 'TRY', 'Yeni dönem');
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (19, 8, DATE '2024-09-15', 6700.00, 'TRY', NULL);
INSERT INTO orders (order_id, customer_id, order_date, total_amount, currency, description)
VALUES (20, 10, DATE '2024-10-01', 330.00, 'TRY', 'Son sipariş');

-- ==================================================
-- Meter readings (120 rows, 10 per partition/month)
-- ==================================================
DECLARE
    v_id NUMBER := 1;
    v_date DATE;
BEGIN
    FOR m IN 1..12 LOOP
        FOR i IN 1..10 LOOP
            v_date := ADD_MONTHS(DATE '2024-01-01', m - 1) + (i - 1);
            INSERT INTO meter_readings (reading_id, meter_id, reading_date, value_kwh, quality_code)
            VALUES (v_id, MOD(i - 1, 5) + 1, v_date, ROUND(DBMS_RANDOM.VALUE(10, 500), 4), 'OK');
            v_id := v_id + 1;
        END LOOP;
    END LOOP;
    COMMIT;
END;
/

-- ==================================================
-- Products (5 rows)
-- ==================================================
INSERT INTO products (product_id, sku, name, price, weight_kg, category)
VALUES (1, 'ELK-001', 'Akıllı Sayaç v2', 2500.00, 1.200, 'Elektrik');
INSERT INTO products (product_id, sku, name, price, weight_kg, category)
VALUES (2, 'ELK-002', 'Akıllı Sayaç v3', 3200.00, 0.950, 'Elektrik');
INSERT INTO products (product_id, sku, name, price, weight_kg, category)
VALUES (3, 'GAZ-001', 'Gaz Ölçer Pro', 1800.00, 2.500, 'Doğalgaz');
INSERT INTO products (product_id, sku, name, price, weight_kg, category)
VALUES (4, 'SU-001', 'Su Sayacı Dijital', 950.00, 0.600, 'Su');
INSERT INTO products (product_id, sku, name, price, weight_kg, category)
VALUES (5, 'MOD-001', 'Haberleşme Modülü', 450.00, 0.150, 'Aksesuar');

-- ==================================================
-- Type test (3 rows with various type combinations)
-- ==================================================
INSERT INTO type_test (id, col_smallint, col_integer, col_bigint, col_numeric, col_number_nop,
    col_varchar2, col_nvarchar2, col_char, col_clob, col_nclob, col_blob, col_raw,
    col_date, col_timestamp, col_timestamp_tz, col_float, col_binary_float, col_binary_double)
VALUES (1, 42, 123456, 9876543210, 12345.67890, 99.99,
    'Hello World', N'Merhaba Dünya', 'ABC       ', 'Bu bir CLOB verisi', N'Bu bir NCLOB verisi',
    UTL_RAW.CAST_TO_RAW('binary data'), UTL_RAW.CAST_TO_RAW('raw'),
    DATE '2024-06-15', TIMESTAMP '2024-06-15 14:30:00.123456',
    TIMESTAMP '2024-06-15 14:30:00.123456 +03:00',
    3.14159, 2.71828, 1.41421356);

INSERT INTO type_test (id, col_smallint, col_integer, col_bigint, col_numeric, col_number_nop,
    col_varchar2, col_date, col_timestamp)
VALUES (2, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL);

INSERT INTO type_test (id, col_smallint, col_integer, col_bigint, col_numeric, col_number_nop,
    col_varchar2, col_nvarchar2, col_char, col_date, col_timestamp, col_float, col_binary_float, col_binary_double)
VALUES (3, -99, -2147483648, 9223372036854775807, 99999999999999999999.99999, 0,
    '', N'', '          ', DATE '1970-01-01', TIMESTAMP '1970-01-01 00:00:00.000000',
    0, 0, 0);

-- ==================================================
-- Sales by region (30 rows across LIST partitions)
-- ==================================================
-- Europe (10 rows)
INSERT INTO sales_by_region VALUES (1, 'TR', DATE '2024-01-15', 5200.50, 'Laptop', 'Ahmet Yilmaz');
INSERT INTO sales_by_region VALUES (2, 'DE', DATE '2024-02-20', 3100.00, 'Monitor', 'Hans Mueller');
INSERT INTO sales_by_region VALUES (3, 'FR', DATE '2024-03-10', 890.75, 'Keyboard', 'Pierre Dupont');
INSERT INTO sales_by_region VALUES (4, 'UK', DATE '2024-04-05', 7600.00, 'Laptop', 'John Smith');
INSERT INTO sales_by_region VALUES (5, 'TR', DATE '2024-05-12', 1250.00, 'Tablet', 'Elif Kaya');
INSERT INTO sales_by_region VALUES (6, 'IT', DATE '2024-06-18', 4300.00, 'Phone', 'Marco Rossi');
INSERT INTO sales_by_region VALUES (7, 'ES', DATE '2024-07-22', 2100.50, 'Monitor', 'Carlos Garcia');
INSERT INTO sales_by_region VALUES (8, 'DE', DATE '2024-08-30', 6800.00, 'Laptop', 'Anna Schmidt');
INSERT INTO sales_by_region VALUES (9, 'TR', DATE '2024-09-14', 950.25, 'Keyboard', 'Mehmet Demir');
INSERT INTO sales_by_region VALUES (10, 'FR', DATE '2024-10-01', 3400.00, 'Tablet', 'Marie Martin');
-- Americas (8 rows)
INSERT INTO sales_by_region VALUES (11, 'US', DATE '2024-01-20', 8900.00, 'Laptop', 'Bob Wilson');
INSERT INTO sales_by_region VALUES (12, 'CA', DATE '2024-02-15', 2300.50, 'Phone', 'Sarah Brown');
INSERT INTO sales_by_region VALUES (13, 'BR', DATE '2024-03-25', 1500.00, 'Tablet', 'Jose Silva');
INSERT INTO sales_by_region VALUES (14, 'US', DATE '2024-04-10', 4200.75, 'Monitor', 'Alice Johnson');
INSERT INTO sales_by_region VALUES (15, 'MX', DATE '2024-05-05', 670.00, 'Keyboard', 'Miguel Lopez');
INSERT INTO sales_by_region VALUES (16, 'US', DATE '2024-06-20', 9500.00, 'Laptop', 'David Lee');
INSERT INTO sales_by_region VALUES (17, 'CA', DATE '2024-07-15', 1800.25, 'Phone', 'Emily Chen');
INSERT INTO sales_by_region VALUES (18, 'BR', DATE '2024-08-10', 3200.00, 'Tablet', 'Ana Costa');
-- Asia (7 rows)
INSERT INTO sales_by_region VALUES (19, 'JP', DATE '2024-01-25', 12000.00, 'Laptop', 'Yuki Tanaka');
INSERT INTO sales_by_region VALUES (20, 'CN', DATE '2024-02-28', 5600.50, 'Phone', 'Wei Zhang');
INSERT INTO sales_by_region VALUES (21, 'KR', DATE '2024-03-15', 7800.00, 'Monitor', 'Min Park');
INSERT INTO sales_by_region VALUES (22, 'IN', DATE '2024-04-20', 2100.00, 'Tablet', 'Raj Patel');
INSERT INTO sales_by_region VALUES (23, 'SG', DATE '2024-05-10', 4500.75, 'Laptop', 'Lim Wei');
INSERT INTO sales_by_region VALUES (24, 'JP', DATE '2024-06-25', 8900.00, 'Phone', 'Kenji Sato');
INSERT INTO sales_by_region VALUES (25, 'CN', DATE '2024-07-30', 3300.00, 'Keyboard', 'Li Ming');
-- Other/Default (5 rows)
INSERT INTO sales_by_region VALUES (26, 'AU', DATE '2024-01-30', 6200.00, 'Laptop', 'James Cook');
INSERT INTO sales_by_region VALUES (27, 'ZA', DATE '2024-03-05', 1900.50, 'Phone', 'Nelson Mandela');
INSERT INTO sales_by_region VALUES (28, 'NG', DATE '2024-05-15', 800.00, 'Keyboard', 'Ade Ojo');
INSERT INTO sales_by_region VALUES (29, 'AU', DATE '2024-07-20', 4100.25, 'Tablet', 'Emma Wilson');
INSERT INTO sales_by_region VALUES (30, 'ZA', DATE '2024-09-10', 2700.00, 'Monitor', 'Thabo Mbeki');

COMMIT;
