package main

import (
	"log/slog"

	"ant/server/internal/domain"
	"ant/server/internal/quota"
	"ant/server/internal/store"
	"ant/server/internal/supply"
	"ant/server/internal/supply/quotasource"
)

// newQuotaPage wires the page's accounts. In demo mode the page shows sample
// accounts and nothing is read. Otherwise the supply engine discovers and
// reads every account through the quota source (design 11 phase 2) and
// records them in namespace ns; the returned engine must be started and
// waited for alongside the server.
func newQuotaPage(demo bool, st store.Store, clock supply.Clock, ns domain.ID, logger *slog.Logger) (*quota.Service, *supply.Engine) {
	local := quota.DefaultLocal()
	if demo {
		return quota.NewService(local, quota.NewStore(""), quota.Options{Demo: true}), nil // additions kept in memory
	}
	accounts := quota.NewStore(local.AccountsFile())
	src := quotasource.New(local, accounts, quota.Providers())
	engine := supply.NewEngine(ns, st, src, clock, supply.DefaultPolicy(), logger.With("component", "supply"))
	return quota.NewService(local, accounts, quota.Options{Reader: quotasource.Reader{Source: src, Engine: engine}}), engine
}
