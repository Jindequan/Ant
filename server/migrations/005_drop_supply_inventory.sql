-- Retire the rejected supply-inventory direction (user rulings 2026-10-07;
-- ENG-03; ruling R7 2026-10-08: drop the data). One-way: rows are dropped.
-- Migration 004 stays in history so v3 databases upgrade through it.
DROP TABLE supply_inventory;
