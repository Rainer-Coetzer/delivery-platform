// Package store persists the Job aggregate to Postgres. It is a plain
// pgx repository (no code generator) kept intentionally small: every query
// this service needs, in one file, easy to read alongside domain.Job.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
)

var ErrNotFound = errors.New("job not found")

type Store struct {
	pool *db.Pool
}

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// Create inserts a brand-new job and its line items inside tx.
func Create(ctx context.Context, tx pgx.Tx, o *domain.Job) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO jobs (
			id, customer_id, customer_name, merchant_id, merchant_name, status,
			paid, merchant_accepted, subtotal_cents, delivery_fee_cents, surge_multiplier,
			total_cents, payment_method, pickup_lat, pickup_lng, pickup_label,
			dropoff_lat, dropoff_lng, dropoff_label
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		o.ID, o.CustomerID, o.CustomerName, o.MerchantID, o.MerchantName, o.Status,
		o.Paid, o.MerchantAccepted, o.SubtotalCents, o.DeliveryFeeCents, o.SurgeMultiplier,
		o.TotalCents, o.PaymentMethod, o.Pickup.Lat, o.Pickup.Lng, nullStr(o.Pickup.Label),
		o.Dropoff.Lat, o.Dropoff.Lng, nullStr(o.Dropoff.Label))
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	for _, item := range o.Items {
		_, err := tx.Exec(ctx, `
			INSERT INTO job_lines (job_id, catalogue_item_id, name, quantity, unit_price_cents)
			VALUES ($1,$2,$3,$4,$5)`,
			o.ID, item.CatalogueItemID, item.Name, item.Quantity, item.UnitPriceCents)
		if err != nil {
			return fmt.Errorf("insert job item: %w", err)
		}
	}
	return recordHistory(ctx, tx, o.ID, "", o.Status, "")
}

func recordHistory(ctx context.Context, tx pgx.Tx, jobID, from, to, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO job_events (job_id, from_status, to_status, reason)
		VALUES ($1,$2,$3,$4)`, jobID, from, to, nullStr(reason))
	if err != nil {
		return fmt.Errorf("insert job status history: %w", err)
	}
	return nil
}

// GetForUpdate loads a job and locks its row for the duration of tx, so
// two consumers handling events for the same order (e.g. payments.completed
// and merchant.job-accepted arriving close together) serialize instead
// of racing on MaybeConfirm.
func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*domain.Job, error) {
	return scanJob(tx.QueryRow(ctx, baseSelect+` WHERE id = $1 FOR UPDATE`, id))
}

func (s *Store) Get(ctx context.Context, id string) (*domain.Job, error) {
	o, err := scanJob(s.pool.QueryRow(ctx, baseSelect+` WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	items, err := s.items(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	o.Items = items
	return o, nil
}

func (s *Store) ListByCustomer(ctx context.Context, customerID string, limit int) ([]*domain.Job, error) {
	return s.list(ctx, baseSelect+` WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2`, customerID, limit)
}

func (s *Store) ListByMerchant(ctx context.Context, merchantID string, limit int) ([]*domain.Job, error) {
	return s.list(ctx, baseSelect+` WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT $2`, merchantID, limit)
}

func (s *Store) list(ctx context.Context, query string, args ...any) ([]*domain.Job, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []*domain.Job
	for rows.Next() {
		o, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) items(ctx context.Context, q queryer, jobID string) ([]domain.Item, error) {
	rows, err := q.Query(ctx, `SELECT catalogue_item_id, name, quantity, unit_price_cents FROM job_lines WHERE job_id = $1`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list job items: %w", err)
	}
	defer rows.Close()
	var out []domain.Item
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.CatalogueItemID, &it.Name, &it.Quantity, &it.UnitPriceCents); err != nil {
			return nil, fmt.Errorf("scan job item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ApplyTransition persists a new status (and paid/accepted/agent flags) and
// appends a history row, inside tx.
func ApplyTransition(ctx context.Context, tx pgx.Tx, o *domain.Job, from string) error {
	_, err := tx.Exec(ctx, `
		UPDATE jobs SET status=$2, paid=$3, merchant_accepted=$4, agent_id=$5,
			cancel_reason=$6, updated_at=now()
		WHERE id=$1`,
		o.ID, o.Status, o.Paid, o.MerchantAccepted, nullStr(o.AgentID), nullStr(o.CancelReason))
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return recordHistory(ctx, tx, o.ID, from, o.Status, o.CancelReason)
}

// UpdateFlags persists paid/accepted/agent changes without a status
// transition (e.g. recording payment before the merchant has responded).
func UpdateFlags(ctx context.Context, tx pgx.Tx, o *domain.Job) error {
	_, err := tx.Exec(ctx, `UPDATE jobs SET paid=$2, merchant_accepted=$3, agent_id=$4, updated_at=now() WHERE id=$1`,
		o.ID, o.Paid, o.MerchantAccepted, nullStr(o.AgentID))
	if err != nil {
		return fmt.Errorf("update job flags: %w", err)
	}
	return nil
}

// CountOpen returns how many jobs for merchantID are not yet DELIVERED
// or CANCELLED — used for the surge multiplier.
func (s *Store) CountOpen(ctx context.Context, merchantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE merchant_id=$1 AND status NOT IN ('DELIVERED','CANCELLED')`,
		merchantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count open jobs: %w", err)
	}
	return n, nil
}

const baseSelect = `SELECT id, customer_id, customer_name, merchant_id, merchant_name, status,
	paid, merchant_accepted, subtotal_cents, delivery_fee_cents, surge_multiplier, total_cents,
	payment_method, pickup_lat, pickup_lng, coalesce(pickup_label,''), dropoff_lat, dropoff_lng,
	coalesce(dropoff_label,''), coalesce(agent_id,''), coalesce(cancel_reason,''), created_at, updated_at
	FROM jobs`

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*domain.Job, error) {
	o := &domain.Job{}
	err := row.Scan(&o.ID, &o.CustomerID, &o.CustomerName, &o.MerchantID, &o.MerchantName, &o.Status,
		&o.Paid, &o.MerchantAccepted, &o.SubtotalCents, &o.DeliveryFeeCents, &o.SurgeMultiplier, &o.TotalCents,
		&o.PaymentMethod, &o.Pickup.Lat, &o.Pickup.Lng, &o.Pickup.Label, &o.Dropoff.Lat, &o.Dropoff.Lng,
		&o.Dropoff.Label, &o.AgentID, &o.CancelReason, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan job: %w", err)
	}
	return o, nil
}

func scanJobRow(rows pgx.Rows) (*domain.Job, error) { return scanJob(rows) }

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
