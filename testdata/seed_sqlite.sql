-- The shop sample database for SQLite, matching seed_mysql.sql / seed_postgres.sql.
-- Rebuild: rm -f testdata/shop.db && sqlite3 testdata/shop.db < testdata/seed_sqlite.sql
PRAGMA foreign_keys = ON;
DROP VIEW IF EXISTS customer_orders;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS audit_log;

CREATE TABLE customers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email VARCHAR(255) NOT NULL UNIQUE COLLATE NOCASE,
  full_name VARCHAR(120) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  avatar BLOB,
  CONSTRAINT email_has_at CHECK (email LIKE '%@%')
);
CREATE TABLE products (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sku VARCHAR(32) NOT NULL,
  name VARCHAR(200) NOT NULL,
  price DECIMAL(10, 2) NOT NULL CHECK (price >= 0),
  attrs JSON,
  price_with_tax REAL GENERATED ALWAYS AS (round(price * 1.2, 2)) VIRTUAL
);
CREATE INDEX idx_sku ON products (sku);
CREATE TABLE orders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  customer_id INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'paid', 'shipped')),
  placed_at DATETIME,
  CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id) ON DELETE CASCADE
);
CREATE INDEX idx_orders_open ON orders (customer_id) WHERE status <> 'shipped';
CREATE TABLE order_items (
  order_id INTEGER NOT NULL,
  product_id INTEGER NOT NULL,
  qty INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (order_id, product_id),
  CONSTRAINT fk_items_order FOREIGN KEY (order_id) REFERENCES orders (id),
  CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) WITHOUT ROWID;
CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  happened_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  message TEXT NOT NULL
);
CREATE TRIGGER orders_status_audit AFTER UPDATE OF status ON orders FOR EACH ROW WHEN NEW.status <> OLD.status
BEGIN
  INSERT INTO audit_log (message) VALUES ('order ' || NEW.id || ': ' || OLD.status || ' -> ' || NEW.status);
END;
CREATE VIEW customer_orders AS SELECT c.full_name, o.id AS order_id, o.status FROM customers c JOIN orders o ON o.customer_id = c.id;

INSERT INTO customers (email, full_name, avatar) VALUES ('ada@example.com', 'Ada Lovelace', X'89504E47'), ('alan@example.com', 'Alan Turing', NULL), ('grace@example.com', 'Grace Hopper', NULL);
INSERT INTO products (sku, name, price, attrs) VALUES ('KB-1', 'Keyboard', 49.99, '{"color":"black","wireless":false,"dims":{"w":44,"h":13,"d":3}}'), ('MS-1', 'Mouse', 19.50, '{"color":"white","dims":{"w":6,"h":4},"ports":[{"type":"usb","version":"2.0"}]}');
INSERT INTO orders (customer_id, status, placed_at) VALUES (1, 'paid', '2026-10-01 10:00:00'), (2, 'new', NULL);
INSERT INTO order_items VALUES (1, 1, 1), (1, 2, 2), (2, 2, 1);
UPDATE orders SET status = 'shipped' WHERE id = 1;
