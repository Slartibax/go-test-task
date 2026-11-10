package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var schemaFiles embed.FS

const schemaAdvisoryLockKey int64 = 0x5A11E7

func Apply(ctx context.Context, pool *pgxpool.Pool) (err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}

	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaAdvisoryLockKey); lockErr != nil {
		conn.Release()
		return fmt.Errorf("lock migrations: %w", lockErr)
	}
	defer func() {
		unlockErr := releaseAdvisoryLock(ctx, conn)
		if unlockErr != nil {
			err = errors.Join(err, unlockErr)
		}
	}()

	return applyEmbeddedMigrations(ctx, conn)
}

func applyEmbeddedMigrations(ctx context.Context, conn *pgxpool.Conn) error {
	entries, err := schemaFiles.ReadDir("sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	migrationFilenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		migrationFilenames = append(migrationFilenames, entry.Name())
	}
	slices.Sort(migrationFilenames)

	for _, filename := range migrationFilenames {
		schemaSQL, err := schemaFiles.ReadFile(path.Join("sql", filename))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", filename, err)
		}
		if _, err = conn.Exec(ctx, string(schemaSQL)); err != nil {
			return fmt.Errorf("apply migration %s: %w", filename, err)
		}
	}
	return nil
}

func releaseAdvisoryLock(ctx context.Context, conn *pgxpool.Conn) error {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	_, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", schemaAdvisoryLockKey)
	if err != nil {
		hijackedConn := conn.Hijack()
		closeErr := hijackedConn.Close(context.WithoutCancel(ctx))
		return errors.Join(fmt.Errorf("unlock migrations: %w", err), closeErr)
	}
	conn.Release()
	return nil
}
