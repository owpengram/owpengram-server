package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// A constructor first owns the reservation key, then the new PostgreSQL
	// backend takes the owner key before the reservation is released. No
	// backend is therefore opened unless one application slot is already
	// fenced for it. The owner lock is session scoped and is released by
	// PostgreSQL on clean close, process crash, or network loss.
	postgresAdmissionLockClass            int32 = 0x54454c45 // "TELE": live owner
	postgresAdmissionReservationLockClass int32 = 0x54454c52 // "TELR": pre-connect reservation
	minimumOperatorConnections                  = 8
	operatorConnectionReserveRatio              = 0.05

	postgresAdmissionConnectTimeout        = 10 * time.Second
	postgresAdmissionReservationSlack      = 5 * time.Second
	postgresAdmissionProbeBatch            = 8
	postgresAdmissionProbeInterval         = 25 * time.Millisecond
	postgresAdmissionApplicationNamePrefix = "telesrv-admission/"

	// Admission slots are shared by every process that targets the same
	// PostgreSQL server. Pools may retain their configured minimum, but burst
	// connections must return idle slots promptly so another process can make
	// progress without a per-role connection budget.
	postgresAdmissionMaxConnIdleTime   = 5 * time.Second
	postgresAdmissionHealthCheckPeriod = time.Second
)

type postgresConnectionCapacity struct {
	maxConnections    int
	superuserReserved int
	serverReserved    int
	operatorReserved  int
	applicationSlots  int
}

func calculatePostgresConnectionCapacity(maxConnections, superuserReserved, serverReserved int) (postgresConnectionCapacity, error) {
	if maxConnections <= 0 || superuserReserved < 0 || serverReserved < 0 {
		return postgresConnectionCapacity{}, errors.New("invalid PostgreSQL connection settings")
	}
	operatorReserved := max(minimumOperatorConnections, int(math.Ceil(float64(maxConnections)*operatorConnectionReserveRatio)))
	applicationSlots := maxConnections - superuserReserved - serverReserved - operatorReserved
	if applicationSlots <= 0 {
		return postgresConnectionCapacity{}, fmt.Errorf(
			"PostgreSQL has no application connection capacity: max=%d superuser_reserved=%d reserved=%d operator_reserved=%d",
			maxConnections, superuserReserved, serverReserved, operatorReserved,
		)
	}
	return postgresConnectionCapacity{
		maxConnections: maxConnections, superuserReserved: superuserReserved,
		serverReserved: serverReserved, operatorReserved: operatorReserved,
		applicationSlots: applicationSlots,
	}, nil
}

type postgresAdmissionReservation struct {
	token      string
	slot       int32
	generation uint64
	timer      *time.Timer
}

type postgresAdmissionController struct {
	dsn string

	mu         sync.Mutex
	conn       *pgx.Conn
	generation uint64
	capacity   postgresConnectionCapacity
	ownerSlot  int32
	nextSlot   int
	pending    map[string]*postgresAdmissionReservation
	reserved   map[int32]string
	sequence   atomic.Uint64
}

type postgresAdmissionControllerInit struct {
	ready      chan struct{}
	controller *postgresAdmissionController
	err        error
}

var postgresAdmissionControllers sync.Map

func postgresAdmissionControllerForDSN(ctx context.Context, dsn string) (*postgresAdmissionController, error) {
	key := strings.TrimSpace(dsn)
	if key == "" {
		return nil, errors.New("PostgreSQL DSN is required for connection admission")
	}
	created := &postgresAdmissionControllerInit{ready: make(chan struct{})}
	actual, loaded := postgresAdmissionControllers.LoadOrStore(key, created)
	entry := actual.(*postgresAdmissionControllerInit)
	if !loaded {
		entry.controller, entry.err = newPostgresAdmissionController(ctx, key)
		close(entry.ready)
		if entry.err != nil {
			postgresAdmissionControllers.CompareAndDelete(key, entry)
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-entry.ready:
	}
	if entry.err != nil {
		return nil, entry.err
	}
	return entry.controller, nil
}

func newPostgresAdmissionController(ctx context.Context, dsn string) (*postgresAdmissionController, error) {
	controller := &postgresAdmissionController{
		dsn: dsn, pending: make(map[string]*postgresAdmissionReservation), reserved: make(map[int32]string),
	}
	controller.mu.Lock()
	err := controller.connectLocked(ctx)
	controller.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return controller, nil
}

func (c *postgresAdmissionController) connectLocked(ctx context.Context) error {
	if c.conn != nil && !c.conn.IsClosed() {
		return nil
	}
	config, err := pgx.ParseConfig(c.dsn)
	if err != nil {
		return fmt.Errorf("parse PostgreSQL admission controller config: %w", err)
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = postgresAdmissionConnectTimeout
	}
	config.RuntimeParams["application_name"] = "telesrv-admission-controller"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("connect PostgreSQL admission controller: %w", err)
	}
	var maxConnections, superuserReserved, serverReserved int
	if err := conn.QueryRow(ctx, `
SELECT current_setting('max_connections')::integer,
       current_setting('superuser_reserved_connections')::integer,
       COALESCE(NULLIF(current_setting('reserved_connections', true), ''), '0')::integer`).Scan(
		&maxConnections, &superuserReserved, &serverReserved,
	); err != nil {
		_ = conn.Close(context.Background())
		return fmt.Errorf("read PostgreSQL connection capacity: %w", err)
	}
	capacity, err := calculatePostgresConnectionCapacity(maxConnections, superuserReserved, serverReserved)
	if err != nil {
		_ = conn.Close(context.Background())
		return err
	}
	c.conn = conn
	c.capacity = capacity
	if err := c.claimControllerSlotLocked(ctx); err != nil {
		_ = conn.Close(context.Background())
		c.conn = nil
		return err
	}
	c.generation++
	return nil
}

func (c *postgresAdmissionController) claimControllerSlotLocked(ctx context.Context) error {
	for {
		for offset := 0; offset < c.capacity.applicationSlots; offset++ {
			slot := int32((c.nextSlot+offset)%c.capacity.applicationSlots + 1)
			var reserved bool
			if err := c.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, slot).Scan(&reserved); err != nil {
				return fmt.Errorf("reserve PostgreSQL admission controller slot %d: %w", slot, err)
			}
			if !reserved {
				continue
			}
			var acquired bool
			if err := c.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer, $2::integer)`, postgresAdmissionLockClass, slot).Scan(&acquired); err != nil {
				return fmt.Errorf("claim PostgreSQL admission controller slot %d: %w", slot, err)
			}
			if !acquired {
				var released bool
				if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, slot).Scan(&released); err != nil {
					return fmt.Errorf("release occupied PostgreSQL admission controller slot %d: %w", slot, err)
				}
				if !released {
					return fmt.Errorf("PostgreSQL admission controller slot %d reservation was not held", slot)
				}
				continue
			}
			var released bool
			if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, slot).Scan(&released); err != nil {
				return fmt.Errorf("release PostgreSQL admission controller slot %d reservation: %w", slot, err)
			}
			if !released {
				return fmt.Errorf("PostgreSQL admission controller slot %d reservation was not held", slot)
			}
			c.ownerSlot = slot
			c.nextSlot = int(slot) % c.capacity.applicationSlots
			return nil
		}
		if !sleepPostgresAdmission(ctx, postgresAdmissionProbeInterval) {
			return ctx.Err()
		}
	}
}

func (c *postgresAdmissionController) resetLocked() {
	if c.conn != nil {
		_ = c.conn.Close(context.Background())
		c.conn = nil
	}
	c.ownerSlot = 0
	c.generation++
	clear(c.reserved)
}

func (c *postgresAdmissionController) reserve(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = postgresAdmissionConnectTimeout + postgresAdmissionReservationSlack
	}
	for {
		c.mu.Lock()
		if err := c.connectLocked(ctx); err != nil {
			c.mu.Unlock()
			return "", err
		}
		probes := min(postgresAdmissionProbeBatch, c.capacity.applicationSlots)
		for probe := 0; probe < probes; probe++ {
			slot := int32(c.nextSlot%c.capacity.applicationSlots + 1)
			c.nextSlot = (c.nextSlot + 1) % c.capacity.applicationSlots
			if slot == c.ownerSlot || c.reserved[slot] != "" {
				continue
			}
			free, err := c.reserveSlotLocked(ctx, slot)
			if err != nil {
				c.resetLocked()
				c.mu.Unlock()
				return "", err
			}
			if !free {
				continue
			}
			token := fmt.Sprintf("%016x", c.sequence.Add(1))
			reservation := &postgresAdmissionReservation{token: token, slot: slot, generation: c.generation}
			c.pending[token] = reservation
			c.reserved[slot] = token
			reservation.timer = time.AfterFunc(ttl, func() { c.abort(token) })
			c.mu.Unlock()
			return token, nil
		}
		c.mu.Unlock()
		if !sleepPostgresAdmission(ctx, postgresAdmissionProbeInterval) {
			return "", ctx.Err()
		}
	}
}

func (c *postgresAdmissionController) reserveSlotLocked(ctx context.Context, slot int32) (bool, error) {
	var reserved bool
	if err := c.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, slot).Scan(&reserved); err != nil {
		return false, fmt.Errorf("reserve PostgreSQL connection slot %d: %w", slot, err)
	}
	if !reserved {
		return false, nil
	}
	var ownerFree bool
	if err := c.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer, $2::integer)`, postgresAdmissionLockClass, slot).Scan(&ownerFree); err != nil {
		return false, fmt.Errorf("probe PostgreSQL connection slot %d owner: %w", slot, err)
	}
	if ownerFree {
		var unlocked bool
		if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionLockClass, slot).Scan(&unlocked); err != nil {
			return false, fmt.Errorf("release PostgreSQL connection slot %d owner probe: %w", slot, err)
		}
		if !unlocked {
			return false, fmt.Errorf("PostgreSQL connection slot %d owner probe was not held", slot)
		}
		return true, nil
	}
	var released bool
	if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, slot).Scan(&released); err != nil {
		return false, fmt.Errorf("release occupied PostgreSQL connection slot %d reservation: %w", slot, err)
	}
	if !released {
		return false, fmt.Errorf("PostgreSQL connection slot %d reservation was not held", slot)
	}
	return false, nil
}

func (c *postgresAdmissionController) claim(ctx context.Context, token string, conn *pgx.Conn) error {
	reservation, err := c.take(token)
	if err != nil {
		return err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer, $2::integer)`, postgresAdmissionLockClass, reservation.slot).Scan(&acquired); err != nil {
		_ = c.release(reservation)
		return fmt.Errorf("claim PostgreSQL connection slot %d: %w", reservation.slot, err)
	}
	if !acquired {
		_ = c.release(reservation)
		return fmt.Errorf("claim PostgreSQL connection slot %d: reservation ownership was lost", reservation.slot)
	}
	if err := c.release(reservation); err != nil {
		var ignored bool
		_ = conn.QueryRow(context.Background(), `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionLockClass, reservation.slot).Scan(&ignored)
		return err
	}
	return nil
}

func (c *postgresAdmissionController) take(token string) (*postgresAdmissionReservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reservation := c.pending[token]
	if reservation == nil {
		return nil, fmt.Errorf("PostgreSQL connection reservation %q is absent or expired", token)
	}
	delete(c.pending, token)
	if reservation.timer != nil {
		reservation.timer.Stop()
	}
	return reservation, nil
}

func (c *postgresAdmissionController) abort(token string) {
	c.mu.Lock()
	reservation := c.pending[token]
	if reservation != nil {
		delete(c.pending, token)
	}
	c.mu.Unlock()
	if reservation != nil {
		_ = c.release(reservation)
	}
}

func (c *postgresAdmissionController) release(reservation *postgresAdmissionReservation) error {
	if reservation == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reserved[reservation.slot] == reservation.token {
		delete(c.reserved, reservation.slot)
	}
	if reservation.generation != c.generation || c.conn == nil || c.conn.IsClosed() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released bool
	if err := c.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer, $2::integer)`, postgresAdmissionReservationLockClass, reservation.slot).Scan(&released); err != nil {
		c.resetLocked()
		return fmt.Errorf("release PostgreSQL connection slot %d reservation: %w", reservation.slot, err)
	}
	if !released {
		return fmt.Errorf("PostgreSQL connection slot %d reservation was not held", reservation.slot)
	}
	return nil
}

func installPostgresConnectionAdmission(ctx context.Context, cfg *pgxpool.Config, dsn string) error {
	controller, err := postgresAdmissionControllerForDSN(ctx, dsn)
	if err != nil {
		return err
	}
	boundPostgresAdmissionPoolIdle(cfg)
	if cfg.ConnConfig.ConnectTimeout <= 0 {
		cfg.ConnConfig.ConnectTimeout = postgresAdmissionConnectTimeout
	}
	reservationTTL := cfg.ConnConfig.ConnectTimeout + postgresAdmissionReservationSlack
	previousBefore := cfg.BeforeConnect
	cfg.BeforeConnect = func(ctx context.Context, connConfig *pgx.ConnConfig) error {
		if previousBefore != nil {
			if err := previousBefore(ctx, connConfig); err != nil {
				return err
			}
		}
		token, err := controller.reserve(ctx, reservationTTL)
		if err != nil {
			return err
		}
		connConfig.RuntimeParams["application_name"] = postgresAdmissionApplicationNamePrefix + token
		return nil
	}
	previousAfter := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		applicationName := conn.Config().RuntimeParams["application_name"]
		token, ok := strings.CutPrefix(applicationName, postgresAdmissionApplicationNamePrefix)
		if !ok || token == "" {
			return fmt.Errorf("PostgreSQL connection has no admission reservation identity")
		}
		if err := controller.claim(ctx, token, conn); err != nil {
			return err
		}
		if previousAfter != nil {
			return previousAfter(ctx, conn)
		}
		return nil
	}
	return nil
}

func boundPostgresAdmissionPoolIdle(cfg *pgxpool.Config) {
	if cfg.MaxConnIdleTime <= 0 || cfg.MaxConnIdleTime > postgresAdmissionMaxConnIdleTime {
		cfg.MaxConnIdleTime = postgresAdmissionMaxConnIdleTime
	}
	if cfg.HealthCheckPeriod <= 0 || cfg.HealthCheckPeriod > postgresAdmissionHealthCheckPeriod {
		cfg.HealthCheckPeriod = postgresAdmissionHealthCheckPeriod
	}
}

func connectPostgresAdmitted(ctx context.Context, dsn string) (*pgx.Conn, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse admitted PostgreSQL connection config: %w", err)
	}
	controller, err := postgresAdmissionControllerForDSN(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = postgresAdmissionConnectTimeout
	}
	token, err := controller.reserve(ctx, config.ConnectTimeout+postgresAdmissionReservationSlack)
	if err != nil {
		return nil, err
	}
	config.RuntimeParams["application_name"] = postgresAdmissionApplicationNamePrefix + token
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		controller.abort(token)
		return nil, fmt.Errorf("connect admitted PostgreSQL session: %w", err)
	}
	if err := controller.claim(ctx, token, conn); err != nil {
		_ = conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}

func sleepPostgresAdmission(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
