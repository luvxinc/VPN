package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"github.com/luvxinc/vpn/server/background"
	"github.com/luvxinc/vpn/server/i18n"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/geoip"
	"github.com/luvxinc/vpn/server/handlers"
	"github.com/luvxinc/vpn/server/middleware"
	"github.com/luvxinc/vpn/server/store"
)

func main() {
	cfg := config.MustLoad()

	// Resolve base dir relative to binary location
	exe, err := os.Executable()
	if err != nil {
		slog.Error("cannot determine executable path", "err", err)
		os.Exit(1)
	}
	baseDir := filepath.Dir(exe)
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(baseDir, p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	db := store.MustNewDB(ctx, cfg.Database.URL, cfg.Database.PoolSize)
	defer db.Close()

	// Redis
	rdb := store.MustNewRedis(ctx, cfg.Redis.URL)
	defer rdb.Close()

	// GeoIP
	geoip.Init(resolve(cfg.GeoIP.DBPath))

	// Resolve sing-box config path (must be absolute for atomic rename)
	cfg.SingBox.ConfigPath = resolve(cfg.SingBox.ConfigPath)

	// Resolve cert paths
	certPath := resolve(cfg.Certs.CertPath)
	keyPath := resolve(cfg.Certs.KeyPath)

	// Template engine
	templateDir := filepath.Join(baseDir, "go", "templates")
	engine := html.New(templateDir, ".html")
	engine.AddFunc("toMB", func(b int64) string {
		return fmt.Sprintf("%.1f", float64(b)/1048576)
	})
	engine.AddFunc("toGB", func(b int64) string {
		return fmt.Sprintf("%.2f", float64(b)/1073741824)
	})
	engine.AddFunc("toKB", func(b int64) string {
		return fmt.Sprintf("%.1f", float64(b)/1024)
	})
	engine.AddFunc("contains", strings.Contains)
	engine.AddFunc("add", func(a, b int64) int64 { return a + b })
	engine.AddFunc("not", func(b bool) bool { return !b })
	engine.AddFunc("t", i18n.T)
	engine.AddFunc("toGBPtr", func(p *int64) string {
		if p == nil {
			return ""
		}
		return fmt.Sprintf("%.1f", float64(*p)/1073741824)
	})
	engine.AddFunc("kbpsToMbps", func(p *int) string {
		if p == nil {
			return ""
		}
		return fmt.Sprintf("%.1f", float64(*p)/1000)
	})

	// Language middleware — reads lang cookie, sets i18n.Current before each request
	langMW := func(c *fiber.Ctx) error {
		i18n.SetLang(c.Cookies("lang", "en"))
		return c.Next()
	}

	// Fiber app
	app := fiber.New(fiber.Config{
		JSONEncoder:  sonic.Marshal,
		JSONDecoder:  sonic.Unmarshal,
		Views:        engine,
		ErrorHandler: customErrorHandler,
	})

	// Handler instances
	apiH := &handlers.APIHandler{DB: db, RDB: rdb, Cfg: cfg}
	adminH := &handlers.AdminHandler{DB: db, RDB: rdb, Cfg: cfg}

	// Middleware instances
	rateLimitMW := middleware.RateLimit(rdb)
	lanMW := middleware.RequireLAN(cfg)
	adminAuthMW := middleware.RequireAdminAuth(cfg)

	// Apply language middleware globally
	app.Use(langMW)

	// Public routes
	app.Get("/health", handlers.Health(VERSION))
	app.Get("/download/client", handlers.DownloadClient(resolve(cfg.Client.ClientZipPath)))

	// API routes
	app.Post("/connect", rateLimitMW, apiH.Connect)
	app.Post("/verify-device", rateLimitMW, apiH.VerifyDevice)
	app.Post("/disconnect", apiH.Disconnect)
	app.Post("/refresh", apiH.Refresh)
	app.Get("/status", apiH.Status)

	// Admin routes
	app.Get("/admin/login", lanMW, adminH.LoginPage)
	app.Post("/admin/login", lanMW, adminH.Login)
	app.Post("/admin/logout", adminH.Logout)
	app.Get("/admin/dashboard", adminAuthMW, adminH.Dashboard)
	app.Get("/admin/api/online-count", adminAuthMW, adminH.APIOnlineCount)
	app.Get("/admin/api/online", adminAuthMW, adminH.APIOnline)
	app.Get("/admin/users", adminAuthMW, adminH.UsersPage)
	app.Post("/admin/users", adminAuthMW, adminH.CreateUser)
	app.Post("/admin/users/:id/delete", adminAuthMW, adminH.DeleteUser)
	app.Post("/admin/users/:id/password", adminAuthMW, adminH.ChangePassword)
	app.Post("/admin/users/:id/toggle", adminAuthMW, adminH.ToggleUser)
	app.Post("/admin/users/:id/kick", adminAuthMW, adminH.KickUser)
	app.Get("/admin/users/:id/verif-code", adminAuthMW, adminH.GenerateVerifCode)
	app.Post("/admin/users/:id/limits", adminAuthMW, adminH.UpdateUserLimits)
	app.Get("/admin/logs", adminAuthMW, adminH.LogsPage)
	app.Get("/admin/stats", adminAuthMW, adminH.StatsPage)
	app.Get("/admin/lang", lanMW, adminH.SetLang)

	// Background goroutines
	poller := background.NewClashPoller(db, rdb, cfg.SingBox.ClashAPIURL)
	logMgr := background.NewLogManager(db, cfg.Log.RetentionDays, cfg.Log.MaxDomainsPerUserPerDay)
	go poller.Run(ctx)
	go logMgr.Run(ctx)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-quit
		slog.Info("shutting down...")
		cancel()
		app.ShutdownWithTimeout(10_000_000_000) // 10s in nanoseconds
	}()

	slog.Info("starting weiai vpn server", "version", VERSION, "addr", ":9443")
	if err := app.ListenTLS(":9443", certPath, keyPath); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return c.Status(code).JSON(fiber.Map{"detail": err.Error()})
}
