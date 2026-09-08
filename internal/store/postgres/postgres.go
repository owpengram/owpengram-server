// Package postgres 用 PostgreSQL 实现持久化存储接口（第一阶段：AuthKeyStore）。
//
// 查询代码由 sqlc 生成于 ./sqlcgen（见 telesrv/sqlc.yaml）；本包在其上实现 store 接口。
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // 注册 pgx5:// migrate driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"telesrv/deploy"
	"telesrv/internal/store/postgres/sqlcgen"
)

const defaultMinConns = 16

const (
	phoneIdentityPredecessorVersion = uint(181)
	phoneIdentityMigrationVersion   = uint(182)
	// "phone182" as a signed PostgreSQL advisory-lock key. The lock spans the
	// two-stage 0181 -> identity audit -> 0182 transition across new binaries.
	phoneIdentityMigrationLockKey = int64(0x70686f6e65313832)
)

// MigrationStatus 是启动迁移后的 schema 状态。
type MigrationStatus struct {
	Version uint
	Dirty   bool
	Empty   bool
}

// PoolOption 调整 pgxpool 连接池配置。
type PoolOption func(*pgxpool.Config)

// WithMaxConns 设置连接池最大连接数；<=0 时保持 pgx 默认。
// 同时把 MinConns 预热到 min(maxConns, 16)，降低 TDesktop 启动风暴下的冷连接尾延迟突刺。
func WithMaxConns(n int) PoolOption {
	return func(cfg *pgxpool.Config) {
		if n <= 0 {
			return
		}
		cfg.MaxConns = int32(n)
		minConns := int32(defaultMinConns)
		if int32(n) < minConns {
			minConns = int32(n)
		}
		cfg.MinConns = minConns
	}
}

// WithMinConns 设置启动时预热的最小连接数；<=0 保持既有配置。
func WithMinConns(n int) PoolOption {
	return func(cfg *pgxpool.Config) {
		if n <= 0 {
			return
		}
		minConns := int32(n)
		if cfg.MaxConns > 0 && minConns > cfg.MaxConns {
			minConns = cfg.MaxConns
		}
		cfg.MinConns = minConns
	}
}

// Open 建立 pgxpool 连接池并 ping 验证。
func Open(ctx context.Context, dsn string, opts ...PoolOption) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool parse config: %w", err)
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	cfg.ConnConfig.Tracer = queryStatsTracer{}
	if err := installPostgresConnectionAdmission(ctx, cfg, dsn); err != nil {
		return nil, fmt.Errorf("initialize PostgreSQL connection admission: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool new: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	if err := warmMinConns(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func warmMinConns(ctx context.Context, pool *pgxpool.Pool) error {
	target := pool.Config().MinConns
	if target <= 0 {
		return nil
	}
	conns := make([]*pgxpool.Conn, 0, target)
	defer func() {
		for _, conn := range conns {
			conn.Release()
		}
	}()
	for int32(len(conns)) < target {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("prewarm pg connection %d/%d: %w", len(conns)+1, target, err)
		}
		conns = append(conns, conn)
	}
	return nil
}

func withTx(ctx context.Context, db sqlcgen.DBTX, op string, fn func(pgx.Tx) error) error {
	beginner, ok := db.(txBeginner)
	if !ok {
		return fmt.Errorf("%s: db does not support transactions", op)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", op, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", op, err)
	}
	committed = true
	return nil
}

// Migrate 用嵌入的迁移脚本将数据库迁移到最新版本。幂等：已最新时返回 nil。
func Migrate(dsn string) error {
	_, err := MigrateAndStatus(dsn)
	return err
}

// MigrateAndStatus 用嵌入迁移脚本迁移数据库，并返回迁移后的 schema 版本。
func MigrateAndStatus(dsn string) (MigrationStatus, error) {
	ctx := context.Background()
	lockConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("connect migration lock: %w", err)
	}
	defer lockConn.Close(ctx)
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, phoneIdentityMigrationLockKey); err != nil {
		return MigrationStatus{}, fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		_, _ = lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, phoneIdentityMigrationLockKey)
	}()

	src, err := iofs.New(deploy.Migrations, "migrations")
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("iofs source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5DSN(dsn))
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("migrate new: %w", err)
	}
	defer m.Close()
	status, err := migrationStatus(m)
	if err != nil {
		return MigrationStatus{}, err
	}
	if status.Dirty {
		return MigrationStatus{}, fmt.Errorf("migrate version %d is dirty", status.Version)
	}
	if status.Empty || status.Version < phoneIdentityPredecessorVersion {
		if err := m.Migrate(phoneIdentityPredecessorVersion); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return MigrationStatus{}, fmt.Errorf("migrate to phone identity predecessor: %w", err)
		}
		status, err = migrationStatus(m)
		if err != nil {
			return MigrationStatus{}, err
		}
		if status.Dirty || status.Empty || status.Version != phoneIdentityPredecessorVersion {
			return MigrationStatus{}, fmt.Errorf("phone identity predecessor status = %+v", status)
		}
	}
	if status.Version < phoneIdentityMigrationVersion {
		if err := canonicalizeStoredPhoneIdentities(ctx, lockConn); err != nil {
			return MigrationStatus{}, fmt.Errorf("migrate phone identities: %w", err)
		}
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return MigrationStatus{}, fmt.Errorf("migrate up: %w", err)
	}
	return migrationStatus(m)
}

func migrationStatus(m *migrate.Migrate) (MigrationStatus, error) {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return MigrationStatus{Empty: true}, nil
	}
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("migrate version: %w", err)
	}
	return MigrationStatus{Version: version, Dirty: dirty}, nil
}

// toPgx5DSN 把 pgxpool 用的 postgres:// DSN 转成 golang-migrate pgx5 driver 所需的 pgx5:// scheme。
func toPgx5DSN(dsn string) string {
	if s, ok := strings.CutPrefix(dsn, "postgres://"); ok {
		return "pgx5://" + s
	}
	if s, ok := strings.CutPrefix(dsn, "postgresql://"); ok {
		return "pgx5://" + s
	}
	return dsn
}
