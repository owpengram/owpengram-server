// Package editionmigrate carries an install's data between the two
// editions' PostgreSQL servers (see internal/config.Config.Edition), so
// switching edition keeps the accounts instead of silently pointing the
// server at an unrelated database.
//
// Everything here goes over the wire with pgx, against two DSNs. The
// obvious alternative -- pg_dump piped into pg_restore -- is not available:
// the portable edition's embedded PostgreSQL ships only the server itself
// (initdb/pg_ctl/postgres), with no psql, pg_dump or pg_restore in its
// runtime directory, and reaching it with the Docker container's copies
// would mean re-binding it off loopback for the duration.
//
// Not dumping the schema is what makes this simple rather than a
// reimplementation of pg_dump: both editions run the identical embedded
// migration set from this same repository, so the target's own migrator
// produces a byte-identical schema and only table *rows* have to move.
package editionmigrate

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"telesrv/internal/domain"
	"telesrv/internal/store/postgres"
)

// Snapshot is what one edition's database holds, as far as deciding whether
// a migration should overwrite it goes.
type Snapshot struct {
	// HasSchema is false when the database has never been migrated.
	HasSchema bool
	// Accounts counts human accounts: users minus the service user and the
	// seeded bots (@BotFather, @Stickers, ...), which every freshly
	// migrated install has regardless of whether anyone ever signed in.
	//
	// Deliberately not a count of auth_keys, which looks like the same
	// signal and is not: a client retrying against a database it has no
	// account on completes a key exchange on every attempt, so a never-used
	// install that something merely pointed a client at still accumulates
	// auth keys.
	Accounts int
}

const accountProbeSQL = `select case when to_regclass('public.users') is null then -1 ` +
	`else (select count(*) from public.users where not is_bot and id <> $1) end`

// Probe reports what the database behind dsn holds.
func Probe(ctx context.Context, dsn string) (Snapshot, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return Snapshot{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var accounts int
	if err := conn.QueryRow(ctx, accountProbeSQL, domain.OfficialSystemUserID).Scan(&accounts); err != nil {
		return Snapshot{}, fmt.Errorf("probe accounts: %w", err)
	}
	if accounts < 0 {
		return Snapshot{HasSchema: false}, nil
	}
	return Snapshot{HasSchema: true, Accounts: accounts}, nil
}

// Copy replaces everything in the database behind dstDSN with the contents
// of the one behind srcDSN. The source is only read from.
func Copy(ctx context.Context, srcDSN, dstDSN string, progress func(string)) error {
	// The target may never have been migrated (switching to an edition this
	// install has not used yet), and even when it has, it must be on the
	// exact same schema version as the source for a binary COPY to line up.
	emit(progress, "Preparing the target database schema...\n")
	if err := postgres.Migrate(dstDSN); err != nil {
		return fmt.Errorf("migrate target database: %w", err)
	}

	src, err := pgx.Connect(ctx, srcDSN)
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer src.Close(ctx)

	dst, err := pgx.Connect(ctx, dstDSN)
	if err != nil {
		return fmt.Errorf("connect target: %w", err)
	}
	defer dst.Close(ctx)

	tables, err := dataTables(ctx, src)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return fmt.Errorf("source database has no tables to copy")
	}

	// Disables foreign-key triggers for this session, which is what makes
	// the per-table loop below safe to run in an arbitrary order instead of
	// having to topologically sort every reference in the schema.
	if _, err := dst.Exec(ctx, "set session_replication_role = replica"); err != nil {
		return fmt.Errorf("disable target constraint triggers: %w", err)
	}

	// One statement for every table rather than a TRUNCATE per table: a
	// table another one references cannot be truncated on its own, and
	// truncating them together is exactly the case PostgreSQL allows.
	emit(progress, fmt.Sprintf("Clearing %d tables in the target database...\n", len(tables)))
	if _, err := dst.Exec(ctx, "truncate table "+strings.Join(quoteAll(tables), ", ")+" cascade"); err != nil {
		return fmt.Errorf("clear target tables: %w", err)
	}

	for i, table := range tables {
		rows, err := copyTable(ctx, src, dst, table)
		if err != nil {
			return fmt.Errorf("copy table %s: %w", table, err)
		}
		if rows > 0 {
			emit(progress, fmt.Sprintf("  [%d/%d] %s: %d rows\n", i+1, len(tables), table, rows))
		}
	}

	if err := copySequences(ctx, src, dst, progress); err != nil {
		return err
	}

	emit(progress, "All data copied.\n")
	return nil
}

// copyTable streams one table's rows across with COPY in binary format,
// which is safe here precisely because both schemas came from the same
// migration set -- column order and types match by construction, so no
// text parsing or type inference is involved.
func copyTable(ctx context.Context, src, dst *pgx.Conn, table string) (int64, error) {
	reader, writer := io.Pipe()

	type result struct {
		rows int64
		err  error
	}
	done := make(chan result, 1)
	go func() {
		tag, err := src.PgConn().CopyTo(ctx, writer, "copy "+quote(table)+" to stdout (format binary)")
		// Closing with the error is what makes the reader below fail
		// instead of hanging or, worse, succeeding on a truncated stream.
		_ = writer.CloseWithError(err)
		done <- result{rows: tag.RowsAffected(), err: err}
	}()

	_, copyErr := dst.PgConn().CopyFrom(ctx, reader, "copy "+quote(table)+" from stdin (format binary)")
	_ = reader.Close()
	out := <-done

	if out.err != nil {
		return 0, fmt.Errorf("read: %w", out.err)
	}
	if copyErr != nil {
		return 0, fmt.Errorf("write: %w", copyErr)
	}
	return out.rows, nil
}

// copySequences carries every sequence's position across. Without this the
// target's sequences stay where its own migrations left them, and the first
// insert after a migration collides with a copied row.
func copySequences(ctx context.Context, src, dst *pgx.Conn, progress func(string)) error {
	rows, err := src.Query(ctx,
		`select schemaname || '.' || sequencename, last_value
		   from pg_sequences where schemaname = 'public' and last_value is not null`)
	if err != nil {
		return fmt.Errorf("list source sequences: %w", err)
	}
	type sequence struct {
		name  string
		value int64
	}
	var sequences []sequence
	for rows.Next() {
		var s sequence
		if err := rows.Scan(&s.name, &s.value); err != nil {
			rows.Close()
			return fmt.Errorf("read source sequences: %w", err)
		}
		sequences = append(sequences, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read source sequences: %w", err)
	}

	for _, s := range sequences {
		if _, err := dst.Exec(ctx, "select setval($1, $2, true)", s.name, s.value); err != nil {
			return fmt.Errorf("set sequence %s: %w", s.name, err)
		}
	}
	emit(progress, fmt.Sprintf("Carried over %d sequence positions.\n", len(sequences)))
	return nil
}

// dataTables lists the ordinary tables holding this install's data.
//
// schema_migrations is excluded on purpose: it is the migrator's own
// bookkeeping, both sides were brought to the same version independently
// just above, and overwriting the target's copy could only ever desynchronise
// it from the schema actually present there.
func dataTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx,
		`select tablename from pg_tables
		  where schemaname = 'public' and tablename <> 'schema_migrations'
		  order by tablename`)
	if err != nil {
		return nil, fmt.Errorf("list source tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read source tables: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func quote(table string) string {
	return pgx.Identifier{"public", table}.Sanitize()
}

func quoteAll(tables []string) []string {
	out := make([]string, len(tables))
	for i, t := range tables {
		out[i] = quote(t)
	}
	return out
}

func emit(progress func(string), text string) {
	if progress != nil {
		progress(text)
	}
}
