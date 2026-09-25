// Package store persists merchants, catalogue items and the local job cache
// to Postgres.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/services/merchant/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateMerchant(ctx context.Context, r *domain.Merchant) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO merchants (id, name, lat, lng, opens_at, closes_at, timezone, is_open)
		VALUES ($1,$2,$3,$4, make_interval(mins => $5), make_interval(mins => $6), $7, $8)`,
		r.ID, r.Name, r.Lat, r.Lng, r.OpensAt.Minutes(), r.ClosesAt.Minutes(), r.Timezone, r.IsOpen)
	if err != nil {
		return fmt.Errorf("insert merchant: %w", err)
	}
	return nil
}

func (s *Store) ListMerchants(ctx context.Context) ([]*domain.Merchant, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, timezone, is_open FROM merchants ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list merchants: %w", err)
	}
	defer rows.Close()
	var out []*domain.Merchant
	for rows.Next() {
		r, err := scanMerchant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetMerchant(ctx context.Context, id string) (*domain.Merchant, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, timezone, is_open FROM merchants WHERE id=$1`, id)
	return scanMerchant(row)
}

// GetMerchantTx is GetMerchant inside a transaction (the consumer's).
func GetMerchantTx(ctx context.Context, tx pgx.Tx, id string) (*domain.Merchant, error) {
	return scanMerchant(tx.QueryRow(ctx, `SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, timezone, is_open FROM merchants WHERE id=$1`, id))
}

type rowScanner interface{ Scan(dest ...any) error }

func scanMerchant(row rowScanner) (*domain.Merchant, error) {
	r := &domain.Merchant{}
	var opensMin, closesMin float64
	err := row.Scan(&r.ID, &r.Name, &r.Lat, &r.Lng, &opensMin, &closesMin, &r.Timezone, &r.IsOpen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan merchant: %w", err)
	}
	r.OpensAt = minutesToDuration(opensMin)
	r.ClosesAt = minutesToDuration(closesMin)
	return r, nil
}

// minutesToDuration converts minutes-since-midnight (as stored in Postgres
// via extract(epoch from ...)/60) into the time.Duration domain.Merchant
// uses for the same value.
func minutesToDuration(m float64) time.Duration {
	return time.Duration(m) * time.Minute
}

// --- Catalogue items ---

func (s *Store) CreateCatalogueItem(ctx context.Context, item *domain.CatalogueItem) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO catalogue_items (id, merchant_id, name, price_cents, stock_qty, available)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		item.ID, item.MerchantID, item.Name, item.PriceCents, item.StockQty, item.Available)
	if err != nil {
		return fmt.Errorf("insert catalogue item: %w", err)
	}
	return nil
}

func (s *Store) ListCatalogueItems(ctx context.Context, merchantID string) ([]*domain.CatalogueItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, merchant_id, name, price_cents, stock_qty, available FROM catalogue_items WHERE merchant_id=$1 ORDER BY name`,
		merchantID)
	if err != nil {
		return nil, fmt.Errorf("list catalogue items: %w", err)
	}
	defer rows.Close()
	var out []*domain.CatalogueItem
	for rows.Next() {
		it := &domain.CatalogueItem{}
		if err := rows.Scan(&it.ID, &it.MerchantID, &it.Name, &it.PriceCents, &it.StockQty, &it.Available); err != nil {
			return nil, fmt.Errorf("scan catalogue item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) UpdateCatalogueItem(ctx context.Context, item *domain.CatalogueItem) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE catalogue_items SET name=$2, price_cents=$3, stock_qty=$4, available=$5 WHERE id=$1 AND merchant_id=$6`,
		item.ID, item.Name, item.PriceCents, item.StockQty, item.Available, item.MerchantID)
	if err != nil {
		return fmt.Errorf("update catalogue item: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CatalogueFor loads the requested catalogue items (by id) for a stock check, as
// a map keyed by catalogue item id.
func CatalogueFor(ctx context.Context, tx pgx.Tx, ids []string) (map[string]domain.CatalogueItem, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, merchant_id, name, price_cents, stock_qty, available FROM catalogue_items WHERE id = ANY($1) FOR UPDATE`,
		ids)
	if err != nil {
		return nil, fmt.Errorf("load catalogue: %w", err)
	}
	defer rows.Close()
	out := map[string]domain.CatalogueItem{}
	for rows.Next() {
		var it domain.CatalogueItem
		if err := rows.Scan(&it.ID, &it.MerchantID, &it.Name, &it.PriceCents, &it.StockQty, &it.Available); err != nil {
			return nil, fmt.Errorf("scan catalogue item: %w", err)
		}
		out[it.ID] = it
	}
	return out, rows.Err()
}

// DecrementStock reduces stock_qty for each requested item inside tx.
func DecrementStock(ctx context.Context, tx pgx.Tx, requested []domain.RequestedItem) error {
	for _, req := range requested {
		if _, err := tx.Exec(ctx, `UPDATE catalogue_items SET stock_qty = stock_qty - $2 WHERE id = $1`, req.CatalogueItemID, req.Quantity); err != nil {
			return fmt.Errorf("decrement stock for %s: %w", req.CatalogueItemID, err)
		}
	}
	return nil
}

// RestoreStock adds quantities back, e.g. after a cancellation.
func RestoreStock(ctx context.Context, tx pgx.Tx, requested []domain.RequestedItem) error {
	for _, req := range requested {
		if _, err := tx.Exec(ctx, `UPDATE catalogue_items SET stock_qty = stock_qty + $2 WHERE id = $1`, req.CatalogueItemID, req.Quantity); err != nil {
			return fmt.Errorf("restore stock for %s: %w", req.CatalogueItemID, err)
		}
	}
	return nil
}

// --- Merchant job cache ---

type MerchantJob struct {
	JobID        string
	MerchantID   string
	Status       string
	Items        []events.JobLine
	Pickup       events.Location
	Dropoff      events.Location
	RejectReason string
}

func InsertMerchantJob(ctx context.Context, tx pgx.Tx, o *MerchantJob) error {
	items, err := json.Marshal(o.Items)
	if err != nil {
		return fmt.Errorf("marshal items: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO merchant_jobs (job_id, merchant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		o.JobID, o.MerchantID, o.Status, items, o.Pickup.Lat, o.Pickup.Lng, o.Dropoff.Lat, o.Dropoff.Lng)
	if err != nil {
		return fmt.Errorf("insert merchant job: %w", err)
	}
	return nil
}

func GetMerchantJobForUpdate(ctx context.Context, tx pgx.Tx, jobID string) (*MerchantJob, error) {
	row := tx.QueryRow(ctx, `
		SELECT job_id, merchant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, coalesce(reject_reason,'')
		FROM merchant_jobs WHERE job_id=$1 FOR UPDATE`, jobID)
	return scanMerchantJob(row)
}

func (s *Store) GetMerchantJob(ctx context.Context, jobID string) (*MerchantJob, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT job_id, merchant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, coalesce(reject_reason,'')
		FROM merchant_jobs WHERE job_id=$1`, jobID)
	return scanMerchantJob(row)
}

func scanMerchantJob(row rowScanner) (*MerchantJob, error) {
	o := &MerchantJob{}
	var itemsRaw []byte
	err := row.Scan(&o.JobID, &o.MerchantID, &o.Status, &itemsRaw, &o.Pickup.Lat, &o.Pickup.Lng, &o.Dropoff.Lat, &o.Dropoff.Lng, &o.RejectReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan merchant job: %w", err)
	}
	if err := json.Unmarshal(itemsRaw, &o.Items); err != nil {
		return nil, fmt.Errorf("unmarshal merchant job items: %w", err)
	}
	return o, nil
}

func UpdateMerchantJobStatus(ctx context.Context, tx pgx.Tx, jobID, status, rejectReason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE merchant_jobs SET status=$2, reject_reason=$3, updated_at=now() WHERE job_id=$1`,
		jobID, status, nullStr(rejectReason))
	if err != nil {
		return fmt.Errorf("update merchant job status: %w", err)
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Prep statuses for merchant_jobs. PREPARING is set when the Job
// service confirms the job (see consumers.handleStatusChanged).
const (
	StatusPending   = "PENDING"
	StatusAccepted  = "ACCEPTED"
	StatusRejected  = "REJECTED"
	StatusPreparing = "PREPARING"
	StatusReady     = "READY"
	StatusCancelled = "CANCELLED"
)

// MarkPreparing moves an ACCEPTED job to PREPARING. Any other current
// status is left alone (e.g. already cancelled), and reported as false.
func MarkPreparing(ctx context.Context, tx pgx.Tx, jobID string, since time.Time) (bool, error) {
	ct, err := tx.Exec(ctx, `
		UPDATE merchant_jobs SET status = 'PREPARING', preparing_since = $2, updated_at = now()
		WHERE job_id = $1 AND status = 'ACCEPTED'`, jobID, since)
	if err != nil {
		return false, fmt.Errorf("mark job %s preparing: %w", jobID, err)
	}
	return ct.RowsAffected() == 1, nil
}

type PreparingJob struct {
	JobID          string
	PreparingSince time.Time
}

func (s *Store) ListPreparing(ctx context.Context) ([]PreparingJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT job_id, preparing_since FROM merchant_jobs WHERE status = 'PREPARING' ORDER BY preparing_since`)
	if err != nil {
		return nil, fmt.Errorf("list preparing jobs: %w", err)
	}
	defer rows.Close()
	var out []PreparingJob
	for rows.Next() {
		var p PreparingJob
		if err := rows.Scan(&p.JobID, &p.PreparingSince); err != nil {
			return nil, fmt.Errorf("scan preparing job: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
