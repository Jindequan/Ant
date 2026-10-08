// ant-server composition root. It wires local bootstrap, optional authenticated
// configuration routes, health/readiness, the supply engine and the page, and
// graceful shutdown. Startup makes no execution or billable provider call; the
// supply engine's read-only usage/balance reads of found and added accounts
// start with it (ruling R4; none in -demo). This binary must never outlive its
// bounded goroutines.
package main

import (
	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/hooks"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/providers/chatcompletions"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/quota"
	"ant/server/internal/runtime"
	"ant/server/internal/security"
	"ant/server/internal/security/sqlitetoken"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/transport"
	"ant/server/internal/webui"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ant-server exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr                  string
		supplyNamespace       string
		dsn                   string
		bootstrapAdmin        bool
		bootstrapNamespace    string
		enableLocalTestConfig bool
		enableResourceConfig  bool
		secretMasterKeyFile   string
		generateSecretKey     bool
		demo                  bool
		backupTo              string
		restoreFrom           string
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	flag.StringVar(&supplyNamespace, "supply-namespace", "local", "namespace the supply engine records this machine's accounts in")
	flag.StringVar(&dsn, "db", "", "sqlite database path (required)")
	flag.BoolVar(&bootstrapAdmin, "bootstrap-admin", false, "create the one-time local administrator token, print once, and exit")
	flag.StringVar(&bootstrapNamespace, "bootstrap-namespace", "", "namespace granted to the one-time bootstrap administrator")
	flag.BoolVar(&enableResourceConfig, "enable-resource-config", false, "enable resource/account drafts and read-only projections without credentials or provider execution")
	flag.BoolVar(&enableLocalTestConfig, "enable-localtest-config", false, "enable provider configuration, the localtest fixture, and the exact-URL-allowlisted DeepSeek adapter")
	flag.StringVar(&secretMasterKeyFile, "secret-master-key-file", "", "path to a 32-byte master key file with mode 0600 (enables secret routes)")
	flag.BoolVar(&generateSecretKey, "generate-secret-master-key", false, "create a new 0600 secret master key at -secret-master-key-file, then exit")
	flag.BoolVar(&demo, "demo", false, "show sample accounts instead of reading this machine's sign-ins")
	flag.StringVar(&backupTo, "backup-to", "", "write a verified SQLite snapshot to this new path, then exit")
	flag.StringVar(&restoreFrom, "restore-from", "", "install a verified SQLite snapshot with dispatch closed and audited, then exit")
	flag.Parse()
	if strings.TrimSpace(supplyNamespace) == "" {
		return errors.New("-supply-namespace must not be empty")
	}
	if generateSecretKey {
		if secretMasterKeyFile == "" {
			return errors.New("-secret-master-key-file is required with -generate-secret-master-key")
		}
		return generateMasterKey(secretMasterKeyFile)
	}
	if enableLocalTestConfig && secretMasterKeyFile == "" {
		return errors.New("-secret-master-key-file is required with -enable-localtest-config; create one with -generate-secret-master-key")
	}
	if secretMasterKeyFile != "" && !enableLocalTestConfig {
		return errors.New("-secret-master-key-file requires -enable-localtest-config")
	}
	if dsn == "" {
		return errors.New("-db is required")
	}
	if backupTo != "" && (bootstrapAdmin || enableResourceConfig || enableLocalTestConfig || secretMasterKeyFile != "" || demo) {
		return errors.New("-backup-to cannot be combined with bootstrap or server configuration flags")
	}
	if restoreFrom != "" && (backupTo != "" || bootstrapAdmin || enableResourceConfig || enableLocalTestConfig || secretMasterKeyFile != "" || demo) {
		return errors.New("-restore-from cannot be combined with backup, bootstrap, or server configuration flags")
	}
	if backupTo != "" {
		sourcePath, err := backupSourcePath(dsn)
		if err != nil {
			return err
		}
		info, err := os.Stat(sourcePath)
		if err != nil {
			return fmt.Errorf("inspect backup source database: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup source database must be a regular file")
		}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	clock := runtime.NewClock()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if restoreFrom != "" {
		if err := sqlite.RestoreBackup(ctx, restoreFrom, dsn, domain.Millis(time.Now().UnixMilli())); err != nil {
			return err
		}
		logger.Info("database restore complete; external dispatch remains closed", "source", restoreFrom, "destination", dsn)
		return nil
	}

	st, err := sqlite.Open(dsn, sqlite.Config{Clock: clock})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	if backupTo != "" {
		concrete, ok := st.(*sqlite.Store)
		if !ok {
			_ = st.Close(context.Background())
			return errors.New("backup requires the SQLite store")
		}
		backupErr := concrete.Backup(ctx, backupTo)
		closeErr := st.Close(context.Background())
		if backupErr != nil {
			return backupErr
		}
		if closeErr != nil {
			return fmt.Errorf("close store after backup: %w", closeErr)
		}
		logger.Info("database backup complete", "path", backupTo)
		return nil
	}
	if err := sqlite.Migrate(ctx, st); err != nil {
		_ = st.Close(ctx)
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("store ready", "dsn", dsn)
	tokenStore, err := sqlitetoken.New(st, clock)
	if err != nil {
		_ = st.Close(context.Background())
		return fmt.Errorf("initialize auth store: %w", err)
	}
	if bootstrapAdmin {
		if strings.TrimSpace(bootstrapNamespace) == "" {
			_ = st.Close(context.Background())
			return errors.New("-bootstrap-namespace is required with -bootstrap-admin")
		}
		token, err := security.BootstrapAdmin(tokenStore, "admin", domain.ID(strings.TrimSpace(bootstrapNamespace)), []domain.Permission{
			domain.PermExecute, domain.PermRead, domain.PermCancel, domain.PermManage, domain.PermConfigure,
		})
		if closeErr := st.Close(context.Background()); closeErr != nil {
			logger.Error("store close after bootstrap", "err", closeErr)
		}
		if err != nil {
			return fmt.Errorf("bootstrap administrator: %w", err)
		}
		// The bearer value is deliberately written once to stdout, never to
		// slog, the database, a file, or an HTTP response.
		_, _ = fmt.Fprintln(os.Stdout, token)
		return nil
	}

	var businessHandler http.Handler
	var localTestProviderHandler http.Handler
	var executionHost *runtime.ExecutionHost
	var supervisor *runtime.Supervisor
	authProvider := security.NewAuthProvider(tokenStore)
	if enableResourceConfig && !enableLocalTestConfig {
		businessHandler, err = resourceConfigurationHandler(st, clock, authProvider, logger)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize resource configuration: %w", err)
		}
	}
	if enableLocalTestConfig {
		providers, fixtureCatalog, err := builtInProviders(clock)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("assemble providers: %w", err)
		}
		catalog := providers.Catalog()
		configService, err := application.NewConfigService(st, catalog, clock)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize config service: %w", err)
		}
		poolQueries, err := application.NewPoolQueries(st, catalog, clock)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize pool queries: %w", err)
		}
		operationQueries, err := application.NewOperationQueries(st)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize operation queries: %w", err)
		}
		runtimeID, err := newRuntimeID()
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize runtime identity: %w", err)
		}
		preparation := commands.NewPreparationService(st, runtimeID, clock)
		operationControls, err := application.NewOperationControls(preparation)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize operation controls: %w", err)
		}
		reconciliationCommands, err := application.NewOperationReconciliations(commands.NewReconciliationService(st, clock))
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize operation reconciliation: %w", err)
		}
		adminCommands, err := application.NewAdminOperations(commands.NewAdminService(st, clock))
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize resource admin commands: %w", err)
		}
		restoreGuard, err := application.NewRestoreGuardService(st, clock)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize restore guard: %w", err)
		}
		key, err := loadMasterKey(secretMasterKeyFile)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("load secret master key: %w", err)
		}
		cipher, err := security.NewSecretCipher(key)
		for i := range key {
			key[i] = 0
		}
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize secret cipher: %w", err)
		}
		secrets, err := application.NewSecretCommands(st, cipher, clock, "local-master-key-v1")
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize secret service: %w", err)
		}
		balanceHTTP, err := transport.NewHTTPClientWithAllowedEndpoints(deepseek.BalanceEndpoint)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize DeepSeek balance endpoint restrictions: %w", err)
		}
		balanceClient, err := transport.NewBearerJSONGET(balanceHTTP, deepseek.BalanceEndpoint, 1<<20)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize DeepSeek balance client: %w", err)
		}
		balanceVerification, err := application.NewDeepSeekBalanceVerification(st, secrets, balanceClient, clock)
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize DeepSeek balance verification: %w", err)
		}
		executionHost, err = runtime.NewExecutionHost(ctx, runtime.HostConfig{OnTaskPanic: func() {
			logger.Error("operation task panicked; durable recovery will resolve the operation")
		}})
		if err != nil {
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize execution host: %w", err)
		}
		planner, err := application.NewCandidatePlannerWithClock(st, catalog, clock, providers.Policies()...)
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize localtest candidate planner: %w", err)
		}
		output := commands.NewOutputService(st, runtimeID, clock)
		settlement := commands.NewSettlementService(st, runtimeID, clock, commands.SettlementPolicy{}, preparation)
		executionCommands := executionLedger{preparation: preparation, output: output, settlement: settlement, clock: clock}
		_, localTestPort, err := net.SplitHostPort(addr)
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("localtest execution requires an explicit TCP listen port: %w", err)
		}
		httpClient, err := transport.NewLocalTestHTTPClientWithAllowedEndpoints(localTestPort, deepseek.APIEndpoint)
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize execution endpoint restrictions: %w", err)
		}
		controlledTransport, err := transport.NewControlledHTTPTransportWithClient(runtimeID, secrets, func(callCtx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
			return preparation.ConsumeDispatchPermit(callCtx, auth, owner, permit, digest, clock.Now())
		}, httpClient)
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize controlled provider transport: %w", err)
		}
		hookRegistry := hooks.NewRegistry()
		if err := hookRegistry.Seal(); err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("seal lifecycle hooks: %w", err)
		}
		executor, err := application.NewCandidateOperationExecutor(application.ExecutorConfig{
			Planner: planner, Ledger: executionCommands,
			Adapters: providers, Transport: controlledTransport,
			Hooks: hookRegistry, HookTimeout: 250 * time.Millisecond,
		})
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize operation executor: %w", err)
		}
		router, err := application.NewRouter(st, func(callCtx context.Context, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string, policy ledger.ClaimPolicy) (ledger.ClaimReply, error) {
			return commands.Claim(callCtx, st, auth, input, fingerprint, policy)
		}, executionHost, clock, executor, application.OperationRouterConfig{
			RuntimeID: runtimeID, LeaseMillis: 30_000, MaxRuntimeAttempts: 3,
			ValidateRequest: chatcompletions.ValidateRequest,
		})
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize operation router: %w", err)
		}
		localTestProviderHandler = api.LocalTestChatCompletionsHandler()
		businessMux := http.NewServeMux()
		businessMux.Handle("/v1/catalog/providers", api.ProviderCatalogHandler(catalog))
		businessMux.Handle("/v1/catalog/scopes", api.ProviderScopeHandler(fixtureCatalog))
		businessMux.Handle("/v1/pools", api.PoolsHandler(poolQueries, configService, router))
		businessMux.Handle("/v1/pools/", api.PoolsHandler(poolQueries, configService, router))
		businessMux.Handle("/v1/operations", api.OperationQueryHandler(operationQueries, operationControls))
		businessMux.Handle("/v1/operations/", api.OperationQueryHandler(operationQueries, operationControls))
		businessMux.Handle("/v1/attempts/", api.OperationReconciliationHandler(reconciliationCommands))
		businessMux.Handle("/v1/resources/", api.AdminHandler(adminCommands))
		businessMux.Handle("/v1/holds/", api.AdminHandler(adminCommands))
		businessMux.Handle("/v1/runtime/restore-guard", api.RestoreGuardHandler(restoreGuard))
		businessMux.Handle("GET /v1/operations/{operationId}/events", api.OperationEventHandler(operationQueries, authProvider))
		businessMux.Handle("/", api.ConfigHandler(configService))
		supervisor, err = runtime.NewSupervisor(ctx, executionHost, preparation, clock, runtime.SupervisorConfig{
			HeartbeatInterval: 10 * time.Second,
			LeaseMillis:       30_000,
			SweepInterval:     5 * time.Second,
			CommandTimeout:    5 * time.Second,
			RecoveryAuth: domain.AuthContext{
				PrincipalID: domain.ID("runtime-" + string(runtimeID)),
				Permissions: []domain.Permission{domain.PermManage},
			},
			OnError: func(err error) { logger.Error("execution supervisor command failed", "err", err) },
		})
		if err != nil {
			_ = executionHost.Close(context.Background())
			_ = st.Close(context.Background())
			return fmt.Errorf("initialize execution supervisor: %w", err)
		}
		businessMux.Handle("/v1/secrets", api.SecretHandler(secrets))
		businessMux.Handle("/v1/secrets/", api.SecretHandler(secrets))
		businessMux.Handle("/v1/providers/deepseek/balance-observations", api.DeepSeekBalanceHandler(balanceVerification))
		businessHandler = api.AuthMiddleware(authProvider, api.LogRequest(logger, businessMux))
	}

	mux := http.NewServeMux()
	api.RegisterSessionRoutes(mux, authProvider, tokenStore, clock)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		// T1 skeleton: ready once the store is open; business readiness is
		// decided by later stages (no executable config ⇒ still ready to
		// configure, per 01-ARCHITECTURE §4).
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	if businessHandler != nil {
		mux.Handle("/v1/", businessHandler)
	}
	if localTestProviderHandler != nil {
		mux.Handle("/v1/localtest/chat/completions", localTestProviderHandler)
	}
	// The page and the supply engine behind it: every account read from the
	// sign-ins on this machine. The page answers only on loopback.
	quotaService, supplyEngine := newQuotaPage(demo, st, clock, domain.ID(supplyNamespace), logger)
	mux.Handle("/api/", quotaService.Handler())
	mux.Handle("/", quota.LocalOnly(api.PageHandler(webui.Files())))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout intentionally unset: SSE streams must not be cut by a
		// global write deadline (08-API §4).
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if executionHost != nil {
			_ = executionHost.Close(context.Background())
		}
		_ = st.Close(ctx)
		return fmt.Errorf("listen: %w", err)
	}
	if supervisor != nil {
		supervisor.Start()
	}
	quotaService.Start(ctx)
	if supplyEngine != nil {
		supplyEngine.Start(ctx)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logger.Info("http ready", "addr", addr)

	select {
	case err := <-serveErr:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("http shutdown after serve failure", "err", shutdownErr)
			_ = srv.Close()
		}
		stop()
		quotaService.Wait()
		if supplyEngine != nil {
			supplyEngine.Wait()
		}
		if supervisor != nil {
			if closeErr := supervisor.Close(shutdownCtx); closeErr != nil {
				logger.Error("execution supervisor shutdown", "err", closeErr)
			}
		}
		if executionHost != nil {
			if closeErr := executionHost.Close(shutdownCtx); closeErr != nil {
				logger.Error("execution host shutdown", "err", closeErr)
			}
		}
		if closeErr := st.Close(shutdownCtx); closeErr != nil {
			logger.Error("store close", "err", closeErr)
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "err", err)
		_ = srv.Close()
	}
	quotaService.Wait()
	if supplyEngine != nil {
		supplyEngine.Wait()
	}
	if supervisor != nil {
		if err := supervisor.Close(shutdownCtx); err != nil {
			logger.Error("execution supervisor shutdown", "err", err)
		}
	}
	if executionHost != nil {
		if err := executionHost.Close(shutdownCtx); err != nil {
			logger.Error("execution host shutdown", "err", err)
		}
	}
	if err := st.Close(shutdownCtx); err != nil {
		logger.Error("store close", "err", err)
	}
	logger.Info("ant-server stopped")
	return nil
}

func newRuntimeID() (domain.ID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return domain.ID("runtime-" + hex.EncodeToString(raw[:])), nil
}
