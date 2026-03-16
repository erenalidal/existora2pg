-- Test schema for existora2pg integration tests
-- Runs as APP_USER (migtest) on Oracle Free 23ai

-- ==================================================
-- 1. Simple table with various data types
-- ==================================================
CREATE TABLE customers (
    customer_id   NUMBER(10,0) NOT NULL,
    name          VARCHAR2(200) NOT NULL,
    email         VARCHAR2(300),
    balance       NUMBER(15,2) DEFAULT 0,
    status        CHAR(1) DEFAULT 'A',
    notes         CLOB,
    avatar        BLOB,
    created_at    DATE DEFAULT SYSDATE,
    updated_at    TIMESTAMP DEFAULT SYSTIMESTAMP,
    CONSTRAINT pk_customers PRIMARY KEY (customer_id)
);

CREATE SEQUENCE seq_customers START WITH 1 INCREMENT BY 1 NOCACHE;

CREATE INDEX idx_customers_email ON customers(email);
CREATE INDEX idx_customers_status ON customers(status, created_at);

-- ==================================================
-- 2. Table with foreign keys
-- ==================================================
CREATE TABLE orders (
    order_id      NUMBER(19,0) NOT NULL,
    customer_id   NUMBER(10,0) NOT NULL,
    order_date    DATE NOT NULL,
    total_amount  NUMBER(15,2) NOT NULL,
    currency      VARCHAR2(3) DEFAULT 'TRY',
    description   VARCHAR2(4000),
    CONSTRAINT pk_orders PRIMARY KEY (order_id),
    CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id)
        REFERENCES customers(customer_id)
);

CREATE SEQUENCE seq_orders START WITH 1 INCREMENT BY 1 NOCACHE;

CREATE INDEX idx_orders_customer ON orders(customer_id);
CREATE INDEX idx_orders_date ON orders(order_date);

-- ==================================================
-- 3. Range-partitioned table (by month)
-- ==================================================
CREATE TABLE meter_readings (
    reading_id    NUMBER(19,0) NOT NULL,
    meter_id      NUMBER(10,0) NOT NULL,
    reading_date  DATE NOT NULL,
    value_kwh     NUMBER(12,4) NOT NULL,
    quality_code  VARCHAR2(10),
    raw_data      RAW(200),
    CONSTRAINT pk_meter_readings PRIMARY KEY (reading_id, reading_date)
)
PARTITION BY RANGE (reading_date) (
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

CREATE SEQUENCE seq_meter_readings START WITH 1 INCREMENT BY 1 NOCACHE;

CREATE INDEX idx_meter_readings_meter ON meter_readings(meter_id) LOCAL;

-- ==================================================
-- 4. Table with all exotic types for type mapping tests
-- ==================================================
CREATE TABLE type_test (
    id                NUMBER(10,0) NOT NULL,
    col_smallint      NUMBER(4,0),
    col_integer       NUMBER(9,0),
    col_bigint        NUMBER(19,0),
    col_numeric       NUMBER(20,5),
    col_number_nop    NUMBER,
    col_varchar2      VARCHAR2(500),
    col_nvarchar2     NVARCHAR2(500),
    col_char          CHAR(10),
    col_clob          CLOB,
    col_nclob         NCLOB,
    col_blob          BLOB,
    col_raw           RAW(100),
    col_date          DATE,
    col_timestamp     TIMESTAMP(6),
    col_timestamp_tz  TIMESTAMP(6) WITH TIME ZONE,
    col_timestamp_ltz TIMESTAMP(6) WITH LOCAL TIME ZONE,
    col_float         FLOAT(53),
    col_binary_float  BINARY_FLOAT,
    col_binary_double BINARY_DOUBLE,
    col_xmltype       XMLTYPE,
    col_long          LONG,
    CONSTRAINT pk_type_test PRIMARY KEY (id)
);

-- ==================================================
-- 5. Unique constraint + check constraint table
-- ==================================================
CREATE TABLE products (
    product_id    NUMBER(10,0) NOT NULL,
    sku           VARCHAR2(50) NOT NULL,
    name          VARCHAR2(300) NOT NULL,
    price         NUMBER(10,2) NOT NULL,
    weight_kg     NUMBER(8,3),
    category      VARCHAR2(100),
    CONSTRAINT pk_products PRIMARY KEY (product_id),
    CONSTRAINT uq_products_sku UNIQUE (sku),
    CONSTRAINT ck_products_price CHECK (price >= 0)
);

CREATE SEQUENCE seq_products START WITH 1 INCREMENT BY 1 NOCACHE;

-- ==================================================
-- 6. List-partitioned table (by region)
-- ==================================================
CREATE TABLE sales_by_region (
    sale_id       NUMBER(19,0) NOT NULL,
    region        VARCHAR2(20) NOT NULL,
    sale_date     DATE NOT NULL,
    amount        NUMBER(15,2) NOT NULL,
    product_name  VARCHAR2(200),
    customer_name VARCHAR2(200),
    CONSTRAINT pk_sales_by_region PRIMARY KEY (sale_id, region)
)
PARTITION BY LIST (region) (
    PARTITION p_europe VALUES ('TR', 'DE', 'FR', 'UK', 'IT', 'ES'),
    PARTITION p_americas VALUES ('US', 'CA', 'BR', 'MX'),
    PARTITION p_asia VALUES ('JP', 'CN', 'KR', 'IN', 'SG'),
    PARTITION p_other VALUES (DEFAULT)
);

CREATE SEQUENCE seq_sales START WITH 1 INCREMENT BY 1 NOCACHE;
CREATE INDEX idx_sales_region_date ON sales_by_region(region, sale_date) LOCAL;
