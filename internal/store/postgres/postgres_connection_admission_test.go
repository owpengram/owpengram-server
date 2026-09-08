package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCalculatePostgresConnectionCapacity(t *testing.T) {
	tests := []struct {
		name                            string
		max, superuser, reserved, slots int
		wantErr                         bool
	}{
		{name: "postgres17-default-shape", max: 100, superuser: 3, reserved: 0, slots: 89},
		{name: "server-reserved", max: 100, superuser: 3, reserved: 2, slots: 87},
		{name: "minimum-operator-reserve", max: 20, superuser: 3, reserved: 0, slots: 9},
		{name: "percentage-operator-reserve", max: 1000, superuser: 3, reserved: 0, slots: 947},
		{name: "no-application-capacity", max: 5, superuser: 3, reserved: 0, wantErr: true},
		{name: "invalid-settings", max: 100, superuser: -1, reserved: 0, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := calculatePostgresConnectionCapacity(test.max, test.superuser, test.reserved)
			if test.wantErr {
				if err == nil {
					t.Fatalf("capacity = %+v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("calculate capacity: %v", err)
			}
			if got.applicationSlots != test.slots || got.maxConnections != test.max ||
				got.superuserReserved != test.superuser || got.serverReserved != test.reserved {
				t.Fatalf("capacity = %+v, want slots=%d", got, test.slots)
			}
		})
	}
}

func TestBoundPostgresAdmissionPoolIdle(t *testing.T) {
	tests := []struct {
		name       string
		idle       time.Duration
		health     time.Duration
		wantIdle   time.Duration
		wantHealth time.Duration
	}{
		{
			name: "bound defaults",
			idle: 30 * time.Minute, health: time.Minute,
			wantIdle: postgresAdmissionMaxConnIdleTime, wantHealth: postgresAdmissionHealthCheckPeriod,
		},
		{
			name:     "replace invalid zero values",
			wantIdle: postgresAdmissionMaxConnIdleTime, wantHealth: postgresAdmissionHealthCheckPeriod,
		},
		{
			name: "preserve tighter settings",
			idle: time.Second, health: 500 * time.Millisecond,
			wantIdle: time.Second, wantHealth: 500 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &pgxpool.Config{MaxConnIdleTime: test.idle, HealthCheckPeriod: test.health}
			boundPostgresAdmissionPoolIdle(cfg)
			if cfg.MaxConnIdleTime != test.wantIdle || cfg.HealthCheckPeriod != test.wantHealth {
				t.Fatalf("idle/health = %s/%s, want %s/%s", cfg.MaxConnIdleTime, cfg.HealthCheckPeriod, test.wantIdle, test.wantHealth)
			}
		})
	}
}

func TestPostgresAdmissionReservesCapacityBeforeOpeningBackend(t *testing.T) {
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TELESRV_TEST_POSTGRES_DSN to run postgres integration test")
	}
	ctx := context.Background()
	controller, err := postgresAdmissionControllerForDSN(ctx, dsn)
	if err != nil {
		t.Fatalf("open admission controller: %v", err)
	}
	observer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("open observer: %v", err)
	}
	defer observer.Close(ctx)

	controller.mu.Lock()
	slots := controller.capacity.applicationSlots
	controller.mu.Unlock()
	available := slots - 1 // The controller's own backend is an admitted owner.
	var beforeBackends int
	if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()`).Scan(&beforeBackends); err != nil {
		t.Fatalf("count initial backends: %v", err)
	}
	tokens := make([]string, 0, available)
	defer func() {
		for _, token := range tokens {
			controller.abort(token)
		}
	}()
	for len(tokens) < available {
		token, err := controller.reserve(ctx, time.Minute)
		if err != nil {
			t.Fatalf("reserve slot %d/%d: %v", len(tokens)+1, available, err)
		}
		tokens = append(tokens, token)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if token, err := controller.reserve(waitCtx, time.Minute); err == nil {
		controller.abort(token)
		t.Fatal("capacity-exhausted reservation unexpectedly succeeded")
	}
	var afterBackends, reservations, owners int
	if err := observer.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
  (SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $1::oid AND granted),
  (SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = $2::oid AND granted)`,
		uint32(postgresAdmissionReservationLockClass), uint32(postgresAdmissionLockClass),
	).Scan(&afterBackends, &reservations, &owners); err != nil {
		t.Fatalf("inspect reserved capacity: %v", err)
	}
	if afterBackends != beforeBackends {
		t.Fatalf("capacity wait opened PostgreSQL backends: before=%d after=%d", beforeBackends, afterBackends)
	}
	if reservations != available || owners != 1 {
		t.Fatalf("reservation/owner locks = %d/%d, want %d/1", reservations, owners, available)
	}
}

func TestPostgresPoolConnectionsOwnDistinctAdmissionSlots(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	first, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire first pool connection: %v", err)
	}
	defer first.Release()
	second, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire second pool connection: %v", err)
	}
	defer second.Release()

	admissionSlot := func(label string, conn *pgxpool.Conn) int64 {
		t.Helper()
		var count int
		var slot int64
		if err := conn.QueryRow(ctx, `
SELECT count(*), min(objid::bigint)
FROM pg_locks
WHERE pid = pg_backend_pid()
  AND locktype = 'advisory'
  AND classid = $1::oid
  AND granted`, uint32(postgresAdmissionLockClass)).Scan(&count, &slot); err != nil {
			t.Fatalf("load %s admission slot: %v", label, err)
		}
		if count != 1 {
			t.Fatalf("%s connection owns %d admission slots, want 1", label, count)
		}
		return slot
	}
	firstSlot := admissionSlot("first", first)
	secondSlot := admissionSlot("second", second)
	if firstSlot <= 0 || secondSlot <= 0 || firstSlot == secondSlot {
		t.Fatalf("admission slots = %d/%d, want distinct positive slots", firstSlot, secondSlot)
	}
}

func TestPostgresAdmissionSlotReleasesWithPoolConnection(t *testing.T) {
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TELESRV_TEST_POSTGRES_DSN to run postgres integration test")
	}
	observer := testPool(t)
	ctx := context.Background()
	pool, err := Open(ctx, dsn, WithMaxConns(1), WithMinConns(1))
	if err != nil {
		t.Fatalf("open single-connection pool: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		t.Fatalf("acquire single-connection pool: %v", err)
	}
	var backendPID int
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		conn.Release()
		pool.Close()
		t.Fatalf("load admitted backend pid: %v", err)
	}
	conn.Release()
	pool.Close()

	var retained int
	if err := observer.QueryRow(ctx, `
SELECT count(*)
FROM pg_locks
WHERE pid = $1 AND locktype = 'advisory' AND classid = $2::oid`, backendPID, uint32(postgresAdmissionLockClass)).Scan(&retained); err != nil {
		t.Fatalf("inspect released admission slot: %v", err)
	}
	if retained != 0 {
		t.Fatalf("closed backend retained %d admission locks", retained)
	}
}

func TestPostgresPoolReturnsBurstAdmissionSlotsAfterIdle(t *testing.T) {
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TELESRV_TEST_POSTGRES_DSN to run postgres integration test")
	}
	ctx := context.Background()
	pool, err := Open(ctx, dsn, WithMaxConns(3), WithMinConns(1))
	if err != nil {
		t.Fatalf("open elastic pool: %v", err)
	}
	defer pool.Close()

	connections := make([]*pgxpool.Conn, 0, 3)
	for len(connections) < cap(connections) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire burst connection %d: %v", len(connections)+1, err)
		}
		connections = append(connections, conn)
	}
	for _, conn := range connections {
		conn.Release()
	}
	if got := pool.Stat().TotalConns(); got != 3 {
		t.Fatalf("burst pool total connections = %d, want 3", got)
	}

	deadline := time.Now().Add(postgresAdmissionMaxConnIdleTime + 3*postgresAdmissionHealthCheckPeriod)
	for time.Now().Before(deadline) {
		stat := pool.Stat()
		if stat.TotalConns() == 1 && stat.IdleConns() == 1 && stat.MaxIdleDestroyCount() >= 2 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	stat := pool.Stat()
	t.Fatalf(
		"burst connections were not returned: total=%d idle=%d idle_destroyed=%d",
		stat.TotalConns(), stat.IdleConns(), stat.MaxIdleDestroyCount(),
	)
}
