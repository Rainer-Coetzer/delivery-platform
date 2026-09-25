// Package store persists users, addresses and the job-history read model.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

var ErrNotFound = errors.New("not found")
var ErrDuplicateEmail = errors.New("email already registered")

type User struct {
	ID           string
	Name         string
	Email        string
	PasswordHash string
	Role         string
	RefID        string
}

type Store struct {
	pool   *db.Pool
	tenant string
}

// New is a store for tenant: every account it creates belongs to it
// (config/brand.yaml tenant_id; docs/MULTI_TENANCY.md).
func New(pool *db.Pool, tenant string) *Store { return &Store{pool: pool, tenant: tenant} }

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash, role, ref_id, tenant_id) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		u.ID, u.Name, u.Email, u.PasswordHash, u.Role, nullStr(u.RefID), s.tenant)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateEmail
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, name, email, password_hash, role, coalesce(ref_id,'') FROM users WHERE email=$1`, email)
	return scanUser(row)
}

func (s *Store) Get(ctx context.Context, id string) (*User, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, name, email, password_hash, role, coalesce(ref_id,'') FROM users WHERE id=$1`, id)
	return scanUser(row)
}

func scanUser(row interface{ Scan(dest ...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.RefID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

type Address struct {
	ID     string  `json:"id"`
	UserID string  `json:"userId"`
	Label  string  `json:"label"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

func (s *Store) AddAddress(ctx context.Context, a *Address) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO addresses (id, user_id, label, lat, lng) VALUES ($1,$2,$3,$4,$5)`,
		a.ID, a.UserID, a.Label, a.Lat, a.Lng)
	if err != nil {
		return fmt.Errorf("insert address: %w", err)
	}
	return nil
}

func (s *Store) ListAddresses(ctx context.Context, userID string) ([]*Address, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, label, lat, lng FROM addresses WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	defer rows.Close()
	var out []*Address
	for rows.Next() {
		a := &Address{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.Lat, &a.Lng); err != nil {
			return nil, fmt.Errorf("scan address: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type JobHistoryEntry struct {
	JobID        string `json:"jobId"`
	CustomerID   string `json:"customerId"`
	MerchantName string `json:"merchantName"`
	Status       string `json:"status"`
	TotalCents   int64  `json:"totalCents"`
}

func UpsertJobCreated(ctx context.Context, q db.Queryer, e *JobHistoryEntry, createdAt any) error {
	_, err := q.Exec(ctx, `
		INSERT INTO job_history (job_id, customer_id, merchant_name, status, total_cents, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (job_id) DO NOTHING`,
		e.JobID, e.CustomerID, e.MerchantName, e.Status, e.TotalCents, createdAt)
	if err != nil {
		return fmt.Errorf("upsert job history: %w", err)
	}
	return nil
}

func UpdateJobStatus(ctx context.Context, q db.Queryer, jobID, status string) error {
	_, err := q.Exec(ctx, `UPDATE job_history SET status=$2, updated_at=now() WHERE job_id=$1`, jobID, status)
	if err != nil {
		return fmt.Errorf("update job history status: %w", err)
	}
	return nil
}

func (s *Store) ListJobHistory(ctx context.Context, customerID string, limit int) ([]*JobHistoryEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT job_id, customer_id, merchant_name, status, total_cents FROM job_history WHERE customer_id=$1 ORDER BY created_at DESC LIMIT $2`,
		customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list job history: %w", err)
	}
	defer rows.Close()
	var out []*JobHistoryEntry
	for rows.Next() {
		e := &JobHistoryEntry{}
		if err := rows.Scan(&e.JobID, &e.CustomerID, &e.MerchantName, &e.Status, &e.TotalCents); err != nil {
			return nil, fmt.Errorf("scan job history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
