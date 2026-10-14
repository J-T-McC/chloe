DROP SCHEMA IF EXISTS shop CASCADE;
CREATE SCHEMA shop;
CREATE TYPE shop.order_status AS ENUM ('new','paid','shipped');
CREATE TABLE shop.customers (
  id serial PRIMARY KEY,
  email text NOT NULL UNIQUE,
  full_name varchar(120) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  avatar bytea
);
COMMENT ON TABLE shop.customers IS 'Registered customers';
COMMENT ON COLUMN shop.customers.full_name IS 'Display name';
CREATE TABLE shop.products (id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY, sku text NOT NULL, name text NOT NULL, price numeric(10,2) NOT NULL, attrs jsonb, tags text[]);
CREATE INDEX idx_products_sku ON shop.products (lower(sku));
CREATE TABLE shop.orders (id bigserial PRIMARY KEY, customer_id int NOT NULL REFERENCES shop.customers(id) ON DELETE CASCADE, status shop.order_status NOT NULL DEFAULT 'new', placed_at timestamp, uid uuid DEFAULT gen_random_uuid());
CREATE TABLE shop.order_items (order_id bigint REFERENCES shop.orders(id), product_id int REFERENCES shop.products(id), qty int NOT NULL DEFAULT 1, PRIMARY KEY (order_id, product_id));
CREATE VIEW shop.customer_orders AS SELECT c.full_name, o.id AS order_id, o.status FROM shop.customers c JOIN shop.orders o ON o.customer_id = c.id;
CREATE FUNCTION shop.order_total(oid bigint) RETURNS numeric AS $$ SELECT COALESCE(sum(p.price * i.qty),0) FROM shop.order_items i JOIN shop.products p ON p.id = i.product_id WHERE i.order_id = oid; $$ LANGUAGE sql;
CREATE FUNCTION shop.order_total(oid bigint, discount numeric) RETURNS numeric AS $$ SELECT shop.order_total(oid) * (1 - discount); $$ LANGUAGE sql;
INSERT INTO shop.customers (email, full_name, avatar) VALUES ('ada@example.com','Ada Lovelace','\x89504e47'),('alan@example.com','Alan Turing',NULL),('grace@example.com','Grace Hopper',NULL);
INSERT INTO shop.products (sku,name,price,attrs,tags) VALUES ('KB-1','Keyboard',49.99,'{"color":"black","wireless":false,"dims":{"w":44,"h":13,"d":3}}','{input,usb}'),('MS-1','Mouse',19.50,'{"color":"white","dims":{"w":6,"h":4},"ports":[{"type":"usb","version":"2.0"}]}',NULL);
INSERT INTO shop.orders (customer_id,status,placed_at) VALUES (1,'paid','2026-10-01 10:00'),(2,'new',NULL);
INSERT INTO shop.order_items VALUES (1,1,1),(1,2,2),(2,2,1);
