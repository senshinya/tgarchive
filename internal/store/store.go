package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
	// ro is the same store over a pool of read-only connections, so that the reads the WebUI
	// makes (conversations, pages, search, stats, the media wall, favorites) neither wait behind
	// writes on db's single connection nor hold it up. A read method starts with s = s.reader().
	// In WAL mode a read sees every write committed before it starts.
	ro *Store
}

// connPragmas are the per-connection settings of every connection, writer and readers alike.
const connPragmas = "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"

// readConns bounds the read-only pool.
const readConns = 4

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&" + connPragmas
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite has a single writer anyway, and this rules out SQLITE_BUSY.
	// Consequence: never issue a query while holding *sql.Rows, and use only tx inside withTx.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background(), 0); err != nil {
		db.Close()
		return nil, err
	}
	// An in-memory database is private to its connection: there the reads share the writer.
	if path != ":memory:" && !strings.Contains(path, "mode=memory") {
		rdb, err := sql.Open("sqlite", "file:"+path+"?"+connPragmas+"&_pragma=query_only(1)")
		if err == nil {
			err = rdb.Ping()
		}
		if err != nil {
			db.Close()
			return nil, err
		}
		rdb.SetMaxOpenConns(readConns)
		s.ro = &Store{db: rdb}
	}
	return s, nil
}

// reader returns the store to read through: the read-only pool when there is one.
func (s *Store) reader() *Store {
	if s.ro != nil {
		return s.ro
	}
	return s
}

func (s *Store) Close() error {
	err := s.db.Close()
	if s.ro != nil {
		err = errors.Join(err, s.ro.db.Close())
	}
	return err
}

// migrate applies pending migrations, up to and including number limit (0 = all; tests stop early
// to build an older database).
func (s *Store) migrate(ctx context.Context, limit int) error {
	var ver int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if ver > len(names) {
		// Written by a newer build: this one does not know its schema, and running against it
		// could corrupt it.
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", ver, len(names))
	}
	for i, name := range names {
		n := i + 1
		if !strings.HasPrefix(name, fmt.Sprintf("%04d_", n)) {
			return fmt.Errorf("migration %s out of sequence", name)
		}
		if n <= ver {
			continue
		}
		if limit > 0 && n > limit {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(ctx, name, string(body), n); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration with foreign keys off, so a migration can rebuild a table
// (copy, drop, rename) without the drop cascading into the tables that reference it, and checks
// every foreign key before committing. The pragma is a no-op inside a transaction, hence outside;
// the store's single connection makes it apply to the transaction's connection.
func (s *Store) applyMigration(ctx context.Context, name, body string, n int) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, body); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		bad := rows.Next()
		rows.Close()
		if bad {
			return fmt.Errorf("migration %s: foreign key check failed", name)
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n))
		return err
	})
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
