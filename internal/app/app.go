package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"rest-api/internal/auth"
	"rest-api/internal/browser"
	browserplaywright "rest-api/internal/browser/playwright"
	"rest-api/internal/cache"
	"rest-api/internal/config"
	"rest-api/internal/database"
	"rest-api/internal/downloader"
	"rest-api/internal/downloader/providers"
	apphttp "rest-api/internal/http"
	"rest-api/internal/metrics"
	"rest-api/internal/plans"
	"rest-api/internal/quota"
	"rest-api/internal/ratelimit"
	"rest-api/internal/youtube"
)

const bootstrapTimeout = 30 * time.Second

var browserLauncherFactory = func() browser.Launcher {
	return browserplaywright.NewLauncher()
}

var _ downloader.BrowserRuntime = (*browser.Runtime)(nil)

type Application struct {
	cfg    *config.Config
	logger *slog.Logger

	downloader *downloader.Service
	youtube    *youtube.Service
	browser    browser.Manager

	db      *database.DB
	redis   *cache.Redis
	metrics *metrics.Service

	server *apphttp.Server
}

func New(cfg *config.Config, logger *slog.Logger) (*Application, error) {
	if cfg == nil {
		return nil, fmt.Errorf("app: config is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	a := &Application{cfg: cfg, logger: logger}

	registry := downloader.NewRegistry()
	if err := providers.RegisterAll(registry); err != nil {
		return nil, fmt.Errorf("app: register providers: %w", err)
	}
	a.downloader = downloader.NewService(registry)
	a.youtube = youtube.New()

	if cfg.Browser.Enabled {
		mgr, err := browser.NewManager(cfg.Browser, browserLauncherFactory())
		if err != nil {
			return nil, fmt.Errorf("app: build browser manager: %w", err)
		}
		a.browser = mgr

		rt, err := browser.NewRuntime(mgr, cfg.Browser.NavigationTimeout)
		if err != nil {
			return nil, fmt.Errorf("app: build browser runtime: %w", err)
		}
		for _, p := range registry.All() {
			if bc, ok := p.(downloader.BrowserCapable); ok {
				bc.SetBrowserRuntime(rt)
			}
		}
	}

	initCtx, cancel := context.WithTimeout(context.Background(), bootstrapTimeout)
	defer cancel()
	if cfg.Database.Enabled() {
		db, err := database.Open(initCtx, cfg.Database)
		if err != nil {
			return nil, fmt.Errorf("app: open database: %w", err)
		}
		if err := database.Migrate(initCtx, db.Pool()); err != nil {
			db.Close()
			return nil, fmt.Errorf("app: migrate database: %w", err)
		}
		a.db = db
		logger.Info("database ready")
	}

	if cfg.Redis.Enabled() {
		r, err := cache.New(cfg.Redis)
		if err != nil {
			return nil, fmt.Errorf("app: build redis client: %w", err)
		}
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = r.Ping(pingCtx)
		pingCancel()
		if err != nil {
			_ = r.Close()
			logger.Warn("redis unavailable; continuing without cache", "error", err)
		} else {
			a.redis = r
			logger.Info("redis ready")
		}
	}

	var (
		repo metrics.Repository
		agg  metrics.Aggregator
	)
	if a.db != nil {
		repo = metrics.NewPostgresRepository(a.db.Pool())
	}
	if a.redis != nil {
		agg = metrics.NewRedisAggregator(a.redis.Client())
	}
	a.metrics = metrics.New(repo, agg, logger, cfg.MetricsQueueSize)

	policies := plans.Defaults()
	if a.db != nil {
		loaded, err := plans.LoadFromDB(initCtx, a.db.Pool())
		if err != nil {
			logger.Warn("could not load plan policies; using defaults", "error", err)
		} else {
			policies = loaded
		}
	}

	var authSvc auth.Authenticator
	var keySvc auth.KeyManager
	var quotaSvc quota.Service
	if a.db != nil {
		authRepo := auth.NewPostgresRepository(a.db.Pool())
		svc := auth.NewService(authRepo, nil)
		authSvc = svc
		keySvc = svc

		quotaRepo := quota.NewPostgresRepository(a.db.Pool())
		var quotaCounter quota.Counter
		if a.redis != nil {
			quotaCounter = quota.NewRedisCounter(a.redis.Client())
		}
		quotaSvc = quota.NewManager(quotaRepo, quotaCounter, policies, logger, nil)
	}

	var rateLimiter ratelimit.Limiter
	if a.redis != nil {
		rateLimiter = ratelimit.NewRedisLimiter(a.redis.Client())
	} else {
		rateLimiter = ratelimit.NewMemoryLimiter()
	}

	errHandler := apphttp.NewErrorHandler(logger)
	router := apphttp.NewRouter(apphttp.Dependencies{
		PrettyJSON:   cfg.PrettyJSON,
		Logger:       logger,
		Downloader:   a.downloader,
		Youtube:      a.youtube,
		Metrics:      a.metrics,
		Auth:         authSvc,
		Keys:         keySvc,
		RateLimiter:  rateLimiter,
		Quota:        quotaSvc,
		Plans:        policies,
		ErrorHandler: errHandler,
	})
	a.server = apphttp.NewServer(apphttp.ServerConfig{
		Addr:            cfg.Addr(),
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		IdleTimeout:     cfg.IdleTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, router, logger)

	return a, nil
}

func (a *Application) Run(ctx context.Context) error {

	if a.browser != nil {
		startCtx, cancel := context.WithTimeout(ctx, a.cfg.Browser.Timeout)
		err := a.browser.Start(startCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("app: start browser: %w", err)
		}
		a.logger.Info("browser ready", "driver", a.browser.Name())
	}

	initCtx, initCancel := context.WithTimeout(ctx, bootstrapTimeout)
	if err := a.downloader.Registry().Init(initCtx); err != nil {
		a.logger.Warn("some providers failed to initialize", "error", err)
	}
	initCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.ListenAndServe()
	}()

	select {
	case err := <-errCh:

		infraCtx, infraCancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
		a.closeInfrastructure(infraCtx)
		infraCancel()
		return err
	case <-ctx.Done():
	}

	a.logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.closeInfrastructure(shutdownCtx)
		return fmt.Errorf("app: http shutdown: %w", err)
	}

	infraCtx, infraCancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer infraCancel()
	a.closeInfrastructure(infraCtx)
	a.logger.Info("server stopped")
	return nil
}

func (a *Application) closeInfrastructure(ctx context.Context) {
	if a.metrics != nil {
		a.metrics.Close(ctx)
	}
	if a.redis != nil {
		if err := a.redis.Close(); err != nil {
			a.logger.Warn("redis close error", "error", err)
		}
	}
	if a.db != nil {
		a.db.Close()
	}
	if a.downloader != nil {
		if err := a.downloader.Registry().Shutdown(ctx); err != nil {
			a.logger.Warn("provider shutdown error", "error", err)
		}
	}
	a.shutdownBrowser()
}

func (a *Application) shutdownBrowser() {
	if a.browser == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Browser.CleanupTimeout)
	defer cancel()
	if err := a.browser.Shutdown(ctx); err != nil {
		a.logger.Warn("browser shutdown error", "error", err)
	} else {
		a.logger.Info("browser stopped")
	}
}
