-- User-controlled supply route order (design 17): one optimistic revision
-- and a stable ordered offer-ID list per canonical model. Offer IDs may
-- remain here while temporarily unavailable, so pause/rediscovery preserves
-- preference; route eligibility is always computed separately.
CREATE TABLE supply_route_orders (
  namespace TEXT NOT NULL,
  model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 256),
  revision INTEGER NOT NULL CHECK(revision > 0),
  offer_ids_json TEXT NOT NULL CHECK(json_valid(offer_ids_json)),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, model_id)
) STRICT;
