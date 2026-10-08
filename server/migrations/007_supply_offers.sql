-- Offers and pause (design 12 §4, §6): which model each recorded credential
-- can call, at which endpoint, drawing on which meters; and whether the user
-- paused the account's part in routing. Sole writer: the supply engine
-- (offers) and the page (pause). An offer set is replaced whenever the
-- credential's models are read again.
CREATE TABLE supply_offers (
  namespace TEXT NOT NULL, offer_id TEXT NOT NULL, credential_id TEXT NOT NULL,
  endpoint TEXT NOT NULL CHECK(endpoint LIKE 'https://%'),
  protocol TEXT NOT NULL CHECK(length(protocol) BETWEEN 1 AND 64),
  upstream_model TEXT NOT NULL CHECK(length(upstream_model) BETWEEN 1 AND 256),
  model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 256), -- canonical: catalog or normalized upstream id
  draws_json TEXT NOT NULL CHECK(json_valid(draws_json)),            -- resource template refs a call draws on
  verified_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, offer_id),
  UNIQUE(namespace, credential_id, endpoint, upstream_model),
  FOREIGN KEY(namespace, credential_id) REFERENCES supply_credentials(namespace, credential_id)
) STRICT;
CREATE INDEX supply_offers_model ON supply_offers(namespace, model_id);
ALTER TABLE supply_credentials ADD COLUMN paused INTEGER NOT NULL DEFAULT 0 CHECK(paused IN(0,1));
