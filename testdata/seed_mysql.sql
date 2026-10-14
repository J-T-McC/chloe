CREATE DATABASE IF NOT EXISTS shop;
USE shop;
DROP TABLE IF EXISTS order_items, orders, products, customers;
CREATE TABLE customers (
  id INT AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(255) NOT NULL UNIQUE,
  full_name VARCHAR(120) NOT NULL COMMENT 'Display name',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  avatar BLOB NULL
) COMMENT='Registered customers';
CREATE TABLE products (
  id INT AUTO_INCREMENT PRIMARY KEY,
  sku VARCHAR(32) NOT NULL,
  name VARCHAR(200) NOT NULL,
  price DECIMAL(10,2) NOT NULL,
  attrs JSON NULL,
  KEY idx_sku (sku)
);
CREATE TABLE orders (
  id INT AUTO_INCREMENT PRIMARY KEY,
  customer_id INT NOT NULL,
  status ENUM('new','paid','shipped') NOT NULL DEFAULT 'new',
  placed_at DATETIME NULL,
  CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE CASCADE
);
CREATE TABLE order_items (
  order_id INT NOT NULL,
  product_id INT NOT NULL,
  qty INT NOT NULL DEFAULT 1,
  PRIMARY KEY (order_id, product_id),
  CONSTRAINT fk_items_order FOREIGN KEY (order_id) REFERENCES orders(id),
  CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products(id)
);
CREATE OR REPLACE VIEW customer_orders AS SELECT c.full_name, o.id AS order_id, o.status FROM customers c JOIN orders o ON o.customer_id = c.id;
INSERT INTO customers (email, full_name, avatar) VALUES ('ada@example.com','Ada Lovelace', X'89504E47'), ('alan@example.com','Alan Turing', NULL), ('grace@example.com','Grace Hopper', NULL);
INSERT INTO products (sku, name, price, attrs) VALUES ('KB-1','Keyboard',49.99,'{"color":"black"}'),('MS-1','Mouse',19.50,NULL);
INSERT INTO orders (customer_id, status, placed_at) VALUES (1,'paid','2026-10-01 10:00:00'),(2,'new',NULL);
INSERT INTO order_items VALUES (1,1,1),(1,2,2),(2,2,1);
DROP PROCEDURE IF EXISTS add_order;
DELIMITER $$
CREATE PROCEDURE add_order(IN cust INT)
BEGIN
  INSERT INTO orders (customer_id) VALUES (cust);
  SELECT LAST_INSERT_ID();
END$$
DELIMITER ;
