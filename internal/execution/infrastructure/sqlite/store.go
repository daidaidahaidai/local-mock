package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "state.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, fmt.Errorf("database schema %d is newer than this binary supports", version)
	}
	if version == 0 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, scenario TEXT NOT NULL, project TEXT NOT NULL UNIQUE, directory TEXT NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL, error TEXT NOT NULL); PRAGMA user_version=1;`)
		if err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Insert(ctx context.Context, r domain.Run) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(id,scenario,project,directory,state,created_at,error) VALUES(?,?,?,?,?,?,?)`, r.ID, r.Scenario, r.Project, r.Directory, r.State, r.CreatedAt, r.Error)
	return err
}
func (s *Store) Save(ctx context.Context, r domain.Run) error {
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET state=?,error=? WHERE id=?`, r.State, r.Error, r.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("run not found: %s", r.ID)
	}
	return err
}
func (s *Store) Get(ctx context.Context, id string) (domain.Run, error) {
	var r domain.Run
	err := s.db.QueryRowContext(ctx, `SELECT id,scenario,project,directory,state,created_at,error FROM runs WHERE id=?`, id).Scan(&r.ID, &r.Scenario, &r.Project, &r.Directory, &r.State, &r.CreatedAt, &r.Error)
	return r, err
}
func (s *Store) List(ctx context.Context) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,scenario,project,directory,state,created_at,error FROM runs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []domain.Run{}
	for rows.Next() {
		var r domain.Run
		if err = rows.Scan(&r.ID, &r.Scenario, &r.Project, &r.Directory, &r.State, &r.CreatedAt, &r.Error); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
