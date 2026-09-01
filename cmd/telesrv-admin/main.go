package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/internal/config"
	"telesrv/internal/hoststats"
)

// hostStatsPollInterval is how often the dashboard's CPU/RAM/disk snapshot
// refreshes. A few seconds is frequent enough for an operator glancing at
// the panel without polling disk/proc on every tick.
const hostStatsPollInterval = 5 * time.Second

const defaultAdminAPIAddr = "127.0.0.1:2599"

// bootID is a random value generated once per process start, exposed via
// GET /api/session -- see that handler's doc comment for why (the
// Restart/Update polling flow's way of detecting a genuinely new admin
// process, not just a slow-to-respond old one).
var bootID = newBootID()

func newBootID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is effectively unheard of on any real target
		// this binary runs on; falling back to the wall clock still gives a
		// value that changes across restarts, which is all this is for.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	hs := hoststats.NewPoller(cfg.DiskStatsPath)
	go hs.Run(ctx, hostStatsPollInterval)

	srv, err := newServer(cfg, newReadStore(pool), hs)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	log.Printf("telesrv-admin listening on %s", cfg.Addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type uiConfig struct {
	Addr          string
	PostgresDSN   string
	AdminAPIURL   string
	AdminAPIToken string
	Password      string
	Token         string
	SessionKey    []byte
	// DiskStatsPath points the dashboard host-disk sampler at the local path
	// that matters for the selected blob backend: permanent localfs storage or
	// the S3 upload spool.
	DiskStatsPath string
	// Permissions is the right set a panel session is issued with, from
	// TELESRV_ADMIN_UI_PERMISSIONS. The shipped default is the single wildcard
	// entry, so introducing the permission model never locks an operator out of a
	// panel that worked before.
	Permissions []string
	// HideThirdPartyVerification mirrors config.HideThirdPartyVerification
	// (TELESRV_HIDE_THIRD_PARTY_VERIFICATION, default true): while true, every
	// botverification.* route refuses with 404 regardless of session
	// permissions, and the session/login response tells the frontend to hide
	// the "Third-party marks" nav entry and its routes.
	HideThirdPartyVerification bool
	// IdentityDir mirrors config.IdentityDir -- must point at the same
	// directory owpengram-server reads, so an identity edit here is visible
	// over /owpengram/server-info immediately (see internal/identity).
	IdentityDir string
	// WelcomeMessagePhoneDefault/WelcomeMessageEmailDefault mirror
	// config.WelcomeMessage{Phone,Email}Template -- the env-var-resolved
	// fallback text (TELESRV_WELCOME_MESSAGE_*_TEMPLATE, itself defaulting
	// to the compiled-in copy) the running owpengram-server process falls
	// back to whenever the identity panel override is unset. Surfaced as
	// "the effective default" in the Server Settings login-notifications
	// panel, assuming both binaries share the same .env.
	WelcomeMessagePhoneDefault string
	WelcomeMessageEmailDefault string
	// LoginCodeMessageDefault mirrors config.LoginCodeMessageTemplate -- the
	// env-var-resolved fallback text (TELESRV_LOGIN_CODE_MESSAGE_TEMPLATE,
	// itself defaulting to the compiled-in copy) the running owpengram-server
	// process falls back to whenever the identity panel override is unset.
	// Same "effective default" contract as WelcomeMessage{Phone,Email}Default.
	LoginCodeMessageDefault string
	// RepoRoot is where Server Settings' Restart/Update/.env-editing (see
	// internal/procctl) operate: bin/, logs/, .env, .env.example and
	// .server_panel.json are all expected directly under it, exactly as
	// tui-panel/server-panel.py expects. Defaults to the process's current
	// working directory, which is correct whenever this binary is launched
	// from (or by something that cd'd into) the repo root -- true both for a
	// manual run and for how the TUI itself launches it.
	RepoRoot string
}

// loadConfig 通过 internal/config.Load() 加载 .env 配置文件与环境变量，
// 并转换为 telesrv-admin 需要的 uiConfig。环境变量优先级高于 .env 文件。
func loadConfig() (uiConfig, error) {
	appCfg, err := config.Load()
	if err != nil {
		return uiConfig{}, fmt.Errorf("load config: %w", err)
	}

	adminAPIAddr := appCfg.AdminAPIAddr
	if strings.TrimSpace(adminAPIAddr) == "" {
		adminAPIAddr = defaultAdminAPIAddr
	}

	if appCfg.AdminUIPassword == "" && appCfg.AdminUIToken == "" {
		return uiConfig{}, fmt.Errorf("TELESRV_ADMIN_UI_PASSWORD or TELESRV_ADMIN_UI_TOKEN is required")
	}
	if strings.TrimSpace(appCfg.AdminAPIToken) == "" {
		return uiConfig{}, fmt.Errorf("TELESRV_ADMIN_API_TOKEN is required for admin write actions")
	}
	if appCfg.AdminSessionKey == "" {
		return uiConfig{}, fmt.Errorf("TELESRV_ADMIN_SESSION_KEY is required")
	}
	sum := sha256.Sum256([]byte(appCfg.AdminSessionKey))

	repoRoot, err := os.Getwd()
	if err != nil {
		return uiConfig{}, fmt.Errorf("resolve repo root: %w", err)
	}

	return uiConfig{
		Addr:                       appCfg.AdminUIAddr,
		PostgresDSN:                appCfg.PostgresDSN,
		AdminAPIURL:                adminAPIURL(adminAPIAddr),
		AdminAPIToken:              appCfg.AdminAPIToken,
		Password:                   appCfg.AdminUIPassword,
		Token:                      appCfg.AdminUIToken,
		SessionKey:                 sum[:],
		DiskStatsPath:              dashboardDiskPath(appCfg),
		Permissions:                appCfg.AdminUIPermissions,
		HideThirdPartyVerification: appCfg.HideThirdPartyVerification,
		IdentityDir:                appCfg.IdentityDir,
		WelcomeMessagePhoneDefault: appCfg.WelcomeMessagePhoneTemplate,
		WelcomeMessageEmailDefault: appCfg.WelcomeMessageEmailTemplate,
		LoginCodeMessageDefault:    appCfg.LoginCodeMessageTemplate,
		RepoRoot:                   repoRoot,
	}, nil
}

func dashboardDiskPath(cfg config.Config) string {
	if strings.EqualFold(strings.TrimSpace(cfg.BlobBackendKind), "s3") && strings.TrimSpace(cfg.BlobStagingDir) != "" {
		return cfg.BlobStagingDir
	}
	return cfg.BlobDir
}

func adminAPIURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultAdminAPIAddr
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func newCommandID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(b[:])
}
