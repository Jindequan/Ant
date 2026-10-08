package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func (w *writerTx) ApplyConfigProjection(projection store.ConfigProjection) error {
	for _, account := range projection.Accounts {
		if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO accounts(account_id,provider_owner_ref,enabled,version,definition_json)
VALUES(?,?,?,?,CAST(? AS TEXT)) ON CONFLICT(account_id) DO UPDATE SET
 provider_owner_ref=excluded.provider_owner_ref, enabled=excluded.enabled,
 version=accounts.version+CASE WHEN accounts.provider_owner_ref<>excluded.provider_owner_ref OR accounts.enabled<>excluded.enabled OR accounts.definition_json<>excluded.definition_json THEN 1 ELSE 0 END,
 definition_json=excluded.definition_json`, string(account.AccountID), string(account.ProviderOwnerRef), boolInt(account.Enabled), 1, jsonParam(account.DefinitionJSON)); err != nil {
			return err
		}
	}
	if err := disableMissing(w, "accounts", "account_id", accountIDs(projection.Accounts)); err != nil {
		return err
	}
	for _, binding := range projection.Bindings {
		if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO bindings(binding_id,account_id,adapter_ref,domain,endpoint,credential_ref,credential_revision,enabled,version,definition_json)
VALUES(?,?,?,?,?,?,?,?,1,CAST(? AS TEXT)) ON CONFLICT(binding_id) DO UPDATE SET
 account_id=excluded.account_id, adapter_ref=excluded.adapter_ref, domain=excluded.domain, endpoint=excluded.endpoint,
 credential_ref=excluded.credential_ref, credential_revision=excluded.credential_revision, enabled=excluded.enabled,
 version=bindings.version+CASE WHEN bindings.account_id<>excluded.account_id OR bindings.adapter_ref<>excluded.adapter_ref OR bindings.domain<>excluded.domain OR bindings.endpoint<>excluded.endpoint OR bindings.credential_ref<>excluded.credential_ref OR bindings.credential_revision<>excluded.credential_revision OR bindings.enabled<>excluded.enabled OR bindings.definition_json<>excluded.definition_json THEN 1 ELSE 0 END,
 definition_json=excluded.definition_json`, string(binding.BindingID), string(binding.AccountID), string(binding.AdapterRef), binding.Domain, binding.Endpoint, string(binding.CredentialRef), string(binding.CredentialRevision), boolInt(binding.Enabled), jsonParam(binding.DefinitionJSON)); err != nil {
			return err
		}
	}
	if err := disableMissing(w, "bindings", "binding_id", bindingIDs(projection.Bindings)); err != nil {
		return err
	}
	for _, resource := range projection.Resources {
		definition := resource.Definition
		var ownerKind, ownerID, metric, unit, kind, semantics, semanticsRevision string
		var oldDefinitionJSON []byte
		var quantum int
		err := w.tx.QueryRowContext(w.ctx, `SELECT owner_kind,owner_id,metric_id,unit,quantum_exp,kind,semantics_ref,semantics_revision,definition_json FROM resources WHERE resource_id=?`, string(definition.ResourceID)).Scan(&ownerKind, &ownerID, &metric, &unit, &quantum, &kind, &semantics, &semanticsRevision, &oldDefinitionJSON)
		if err == nil {
			if ownerKind != definition.OwnerKind || ownerID != string(definition.OwnerID) || metric != string(definition.MetricID) || unit != definition.Unit || quantum != definition.QuantumExp || kind != definition.Kind || semantics != string(definition.SemanticsRef) || semanticsRevision != string(definition.SemanticsRevision) {
				return store.ErrProjectionConflict
			}
			var oldDefinition domain.ResourceDefinition
			if err := json.Unmarshal(oldDefinitionJSON, &oldDefinition); err != nil || oldDefinition.PhysicalKey != definition.PhysicalKey || oldDefinition.CommercialRole != definition.CommercialRole || oldDefinition.ProviderFactID != definition.ProviderFactID || oldDefinition.Window != definition.Window {
				return store.ErrProjectionConflict
			}
			if _, err := w.tx.ExecContext(w.ctx, `UPDATE resources SET definition_json=CAST(? AS TEXT) WHERE resource_id=?`, jsonParam(resource.DefinitionJSON), string(definition.ResourceID)); err != nil {
				return err
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO resources(resource_id,owner_kind,owner_id,metric_id,unit,quantum_exp,kind,semantics_ref,semantics_revision,definition_json)
VALUES(?,?,?,?,?,?,?,?,?,CAST(? AS TEXT))`, string(definition.ResourceID), definition.OwnerKind, string(definition.OwnerID), string(definition.MetricID), definition.Unit, definition.QuantumExp, definition.Kind, string(definition.SemanticsRef), string(definition.SemanticsRevision), jsonParam(resource.DefinitionJSON)); err != nil {
				return err
			}
		} else {
			return err
		}
		if resource.InitialState != nil {
			state := resource.InitialState
			var startMS, endMS any
			if resource.StartMS != nil && resource.EndMS != nil {
				startMS, endMS = *resource.StartMS, *resource.EndMS
			}
			_, err := w.tx.ExecContext(w.ctx, `INSERT INTO resource_states(resource_id,window_key,start_ms,end_ms,capacity_units,committed_units,version)
SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM resource_states WHERE resource_id=?)`, string(state.ResourceID), string(state.WindowKey), startMS, endMS, int64(state.Capacity), int64(state.Committed), maxInt64(1, state.Version), string(state.ResourceID))
			if err != nil {
				return err
			}
		}
	}
	if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM binding_resources`); err != nil {
		return err
	}
	for _, binding := range projection.Bindings {
		for _, resourceID := range binding.ResourceRefs {
			if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO binding_resources(binding_id,resource_id) VALUES(?,?)`, string(binding.BindingID), string(resourceID)); err != nil {
				return err
			}
		}
	}
	for _, offer := range projection.Offers {
		row := offer.Offer
		if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO resource_offers(offer_id,binding_id,domain,capability_profile,enabled,version,definition_json)
VALUES(?,?,?,?,?,1,CAST(? AS TEXT)) ON CONFLICT(offer_id) DO UPDATE SET
 binding_id=excluded.binding_id, domain=excluded.domain, capability_profile=excluded.capability_profile, enabled=excluded.enabled,
 version=resource_offers.version+CASE WHEN resource_offers.binding_id<>excluded.binding_id OR resource_offers.domain<>excluded.domain OR resource_offers.capability_profile<>excluded.capability_profile OR resource_offers.enabled<>excluded.enabled OR resource_offers.definition_json<>excluded.definition_json THEN 1 ELSE 0 END,
 definition_json=excluded.definition_json`, string(row.OfferID), string(row.BindingID), row.Domain, string(row.CapabilityProfile), boolInt(row.Enabled), jsonParam(offer.DefinitionJSON)); err != nil {
			return err
		}
	}
	if err := disableMissing(w, "resource_offers", "offer_id", offerIDs(projection.Offers)); err != nil {
		return err
	}
	if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM offer_resources`); err != nil {
		return err
	}
	for _, offer := range projection.Offers {
		for _, resourceID := range offer.ResourceRefs {
			if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO offer_resources(offer_id,resource_id) VALUES(?,?)`, string(offer.Offer.OfferID), string(resourceID)); err != nil {
				return err
			}
		}
	}
	activePoolIDs := make([]string, 0, len(projection.Pools))
	for _, projected := range projection.Pools {
		pool := projected.Pool
		activePoolIDs = append(activePoolIDs, string(pool.PoolID))
		current, found, err := getPool(w.ctx, w.tx, pool.PoolID)
		if err != nil {
			return err
		}
		currentMembers := []store.PoolMemberRecord{}
		if found {
			currentMembers, err = listPoolMembers(w.ctx, w.tx, pool.PoolID)
			if err != nil {
				return err
			}
		}
		membersChanged := !samePoolMembers(currentMembers, projected.Members)
		changed := !found || current.Name != pool.Name || current.Enabled != pool.Enabled || membersChanged
		if found {
			pool.Version = current.Version
			if changed {
				if current.Version == math.MaxInt64 {
					return fmt.Errorf("pool version exhausted")
				}
				pool.Version++
			}
		} else {
			pool.Version = 1
		}
		if _, err := w.tx.ExecContext(w.ctx, `INSERT INTO resource_pools(pool_id,namespace,name,enabled,version,definition_json)
VALUES(?,?,?,?,?,CAST(? AS TEXT)) ON CONFLICT(pool_id) DO UPDATE SET namespace=excluded.namespace,name=excluded.name,enabled=excluded.enabled,version=excluded.version,definition_json=excluded.definition_json`, string(pool.PoolID), string(pool.Namespace), pool.Name, boolInt(pool.Enabled), pool.Version, jsonParam(projected.DefinitionJSON)); err != nil {
			return err
		}
		if membersChanged {
			members := make([]store.PoolMemberRecord, len(projected.Members))
			oldByID := make(map[domain.ID]store.PoolMemberRecord, len(currentMembers))
			for _, member := range currentMembers {
				oldByID[member.OfferID] = member
			}
			for i, member := range projected.Members {
				member.Version = 1
				if old, ok := oldByID[member.OfferID]; ok {
					member.Version = old.Version
					if old.Priority != member.Priority || old.Enabled != member.Enabled {
						if old.Version == math.MaxInt64 {
							return fmt.Errorf("pool member version exhausted")
						}
						member.Version++
					}
				}
				member.PoolID = pool.PoolID
				members[i] = member
			}
			if err := rewriteMembers(w.ctx, w.tx, pool.PoolID, members); err != nil {
				return err
			}
		}
	}
	if err := disableMissing(w, "resource_pools", "pool_id", activePoolIDs); err != nil {
		return err
	}
	return nil
}

func disableMissing(w *writerTx, table, column string, active []string) error {
	allowed := map[string]string{"accounts": "account_id", "bindings": "binding_id", "resource_offers": "offer_id", "resource_pools": "pool_id"}
	if allowed[table] != column {
		return errors.New("sqlite: invalid projection table")
	}
	query := `UPDATE ` + table + ` SET enabled=0,version=version+1 WHERE enabled=1`
	args := []any{}
	if len(active) > 0 {
		query += ` AND ` + column + ` NOT IN (` + placeholders(len(active)) + `)`
		for _, id := range active {
			args = append(args, id)
		}
	}
	_, err := w.tx.ExecContext(w.ctx, query, args...)
	return err
}

func samePoolMembers(current, projected []store.PoolMemberRecord) bool {
	if len(current) != len(projected) {
		return false
	}
	byID := make(map[domain.ID]store.PoolMemberRecord, len(current))
	for _, member := range current {
		byID[member.OfferID] = member
	}
	for _, member := range projected {
		old, ok := byID[member.OfferID]
		if !ok || old.Priority != member.Priority || old.Enabled != member.Enabled {
			return false
		}
	}
	return true
}

func accountIDs(records []store.AccountProjectionRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, string(record.AccountID))
	}
	return ids
}
func bindingIDs(records []store.BindingProjectionRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, string(record.BindingID))
	}
	return ids
}
func offerIDs(records []store.OfferProjectionRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, string(record.Offer.OfferID))
	}
	return ids
}
func placeholders(n int) string {
	query := "?"
	for i := 1; i < n; i++ {
		query += ",?"
	}
	return query
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
