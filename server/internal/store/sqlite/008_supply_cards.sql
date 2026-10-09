-- Cards (design 15): each credential's last read as the page shows it, kept
-- with the reading, so Ant shows what it knew the moment it starts and reads
-- again behind it. Presentation only: nothing routes on a card. Sole writer:
-- the supply engine; a retired credential's card is removed.
CREATE TABLE supply_cards (
  namespace TEXT NOT NULL, credential_id TEXT NOT NULL,
  card_json TEXT NOT NULL CHECK(json_valid(card_json)),
  read_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, credential_id),
  FOREIGN KEY(namespace, credential_id) REFERENCES supply_credentials(namespace, credential_id)
) STRICT;
