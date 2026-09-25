// Package store persists the Job aggregate to Postgres. It is a plain
// pgx repository (no code generator) kept intentionally small: every query
// this module needs, in one file, easy to read alongside domain.Job.
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
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
	"github.com/rainercoetzer/delivery-platform/services/jobs/internal/domain"
)

var ErrNotFound = errors.New("job not found")

type Store struct {
	pool *db.Pool
}

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// Create inserts a brand-new job with its lines and stops inside tx, and
// records how it got to its current state: the initial state, then any
// steps it settled through on creation.
func Create(ctx context.Context, tx pgx.Tx, j *domain.Job, initial string, settled []lifecycle.Step) error {
	facts, err := factsJSON(j.Facts)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO jobs (
			id, pack, customer_id, customer_name, merchant_id, merchant_name, status, milestone, facts,
			subtotal_cents, delivery_fee_cents, surge_multiplier, total_cents, payment_method
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		j.ID, j.Pack, j.CustomerID, j.CustomerName, nullStr(j.MerchantID), nullStr(j.MerchantName), j.Status, j.Milestone, facts,
		j.SubtotalCents, j.DeliveryFeeCents, j.SurgeMultiplier, j.TotalCents, j.PaymentMethod)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	for _, item := range j.Items {
		_, err := tx.Exec(ctx, `
			INSERT INTO job_lines (job_id, kind, catalogue_item_id, name, quantity, unit_price_cents,
				weight_g, length_mm, width_mm, height_mm, declared_value_cents)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			j.ID, item.Kind, nullStr(item.CatalogueItemID), item.Name, item.Quantity, item.UnitPriceCents,
			nullInt(item.WeightGrams), nullInt(int64(item.LengthMm)), nullInt(int64(item.WidthMm)), nullInt(int64(item.HeightMm)),
			nullInt(item.DeclaredValueCents))
		if err != nil {
			return fmt.Errorf("insert job line: %w", err)
		}
	}
	for _, s := range j.Stops {
		_, err := tx.Exec(ctx, `
			INSERT INTO job_stops (job_id, seq, kind, lat, lng, label, merchant_id, window_start, window_end)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			j.ID, s.Seq, s.Kind, s.Location.Lat, s.Location.Lng, nullStr(s.Location.Label), nullStr(s.MerchantID),
			windowStart(s.Window), windowEnd(s.Window))
		if err != nil {
			return fmt.Errorf("insert job stop %d: %w", s.Seq, err)
		}
	}
	if err := recordHistory(ctx, tx, j.ID, "", initial, ""); err != nil {
		return err
	}
	for _, s := range settled {
		if err := recordHistory(ctx, tx, j.ID, s.From, s.To, ""); err != nil {
			return err
		}
	}
	return nil
}

func recordHistory(ctx context.Context, tx pgx.Tx, jobID, from, to, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO job_events (job_id, from_status, to_status, reason)
		VALUES ($1,$2,$3,$4)`, jobID, from, to, nullStr(reason))
	if err != nil {
		return fmt.Errorf("insert job history: %w", err)
	}
	return nil
}

// GetForUpdate loads a job with its lines and stops, and locks its row for
// the duration of tx, so two consumers handling events for the same job
// (e.g. payments.completed and merchant.job-accepted arriving close
// together) serialize instead of racing on the facts.
func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*domain.Job, error) {
	j, err := scanJob(tx.QueryRow(ctx, baseSelect+` WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	return j, loadChildren(ctx, tx, j)
}

func (s *Store) Get(ctx context.Context, id string) (*domain.Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx, baseSelect+` WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return j, loadChildren(ctx, s.pool, j)
}

func loadChildren(ctx context.Context, q queryer, j *domain.Job) error {
	var err error
	if j.Items, err = items(ctx, q, j.ID); err != nil {
		return err
	}
	j.Stops, err = stops(ctx, q, j.ID)
	return err
}

// Save persists the job after lifecycle steps (state, milestone, facts,
// agent, cancel reason) and appends one history row per step, inside tx.
func Save(ctx context.Context, tx pgx.Tx, j *domain.Job, steps []lifecycle.Step) error {
	facts, err := factsJSON(j.Facts)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE jobs SET status=$2, milestone=$3, facts=$4, agent_id=$5, cancel_reason=$6, updated_at=now()
		WHERE id=$1`,
		j.ID, j.Status, j.Milestone, facts, nullStr(j.AgentID), nullStr(j.CancelReason))
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	for _, s := range steps {
		reason := ""
		if s.Milestone == lifecycle.Cancelled {
			reason = j.CancelReason
		}
		if err := recordHistory(ctx, tx, j.ID, s.From, s.To, reason); err != nil {
			return err
		}
	}
	return nil
}

// MarkStopCompleted persists what domain.Job.CompleteStop decided.
func MarkStopCompleted(ctx context.Context, tx pgx.Tx, jobID string, seq int, at time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE job_stops SET completed_at=$3 WHERE job_id=$1 AND seq=$2 AND completed_at IS NULL`, jobID, seq, at)
	if err != nil {
		return fmt.Errorf("complete stop %d of job %s: %w", seq, jobID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete stop %d of job %s: no open stop to complete", seq, jobID)
	}
	return nil
}

func (s *Store) ListByCustomer(ctx context.Context, customerID string, limit int) ([]*domain.Job, error) {
	return s.list(ctx, baseSelect+` WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2`, customerID, limit)
}

func (s *Store) ListByMerchant(ctx context.Context, merchantID string, limit int) ([]*domain.Job, error) {
	return s.list(ctx, baseSelect+` WHERE merchant_id = $1 ORDER BY created_at DESC LIMIT $2`, merchantID, limit)
}

// list returns jobs without their lines and stops: list screens show
// status and totals, and a detail call loads the rest.
func (s *Store) list(ctx context.Context, query string, args ...any) ([]*domain.Job, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	var out []*domain.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func items(ctx context.Context, q queryer, jobID string) ([]domain.Item, error) {
	rows, err := q.Query(ctx, `
		SELECT kind, coalesce(catalogue_item_id,''), name, quantity, unit_price_cents, coalesce(weight_g,0),
			coalesce(length_mm,0), coalesce(width_mm,0), coalesce(height_mm,0), coalesce(declared_value_cents,0)
		FROM job_lines WHERE job_id = $1 ORDER BY id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list job lines: %w", err)
	}
	defer rows.Close()
	out := []domain.Item{}
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.Kind, &it.CatalogueItemID, &it.Name, &it.Quantity, &it.UnitPriceCents, &it.WeightGrams,
			&it.LengthMm, &it.WidthMm, &it.HeightMm, &it.DeclaredValueCents); err != nil {
			return nil, fmt.Errorf("scan job line: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func stops(ctx context.Context, q queryer, jobID string) ([]domain.Stop, error) {
	rows, err := q.Query(ctx, `
		SELECT seq, kind, lat, lng, coalesce(label,''), coalesce(merchant_id,''), completed_at, window_start, window_end
		FROM job_stops WHERE job_id = $1 ORDER BY seq`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list job stops: %w", err)
	}
	defer rows.Close()
	out := []domain.Stop{}
	for rows.Next() {
		var s domain.Stop
		var start, end *time.Time
		if err := rows.Scan(&s.Seq, &s.Kind, &s.Location.Lat, &s.Location.Lng, &s.Location.Label, &s.MerchantID, &s.CompletedAt, &start, &end); err != nil {
			return nil, fmt.Errorf("scan job stop: %w", err)
		}
		if start != nil && end != nil {
			s.Window = &events.TimeWindow{Start: start.UTC(), End: end.UTC()}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountOpen returns how many jobs haven't finished, for surge pricing: the
// merchant's own when there is one (a busy kitchen), all of them when
// there isn't (courier jobs share one pool of agents).
func (s *Store) CountOpen(ctx context.Context, merchantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE ($1 = '' OR merchant_id = $1) AND milestone NOT IN ('completed','cancelled')`,
		merchantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count open jobs: %w", err)
	}
	return n, nil
}

const baseSelect = `SELECT id, pack, customer_id, customer_name, coalesce(merchant_id,''), coalesce(merchant_name,''),
	status, milestone, facts, subtotal_cents, delivery_fee_cents, surge_multiplier, total_cents,
	payment_method, coalesce(agent_id,''), coalesce(cancel_reason,''), created_at, updated_at
	FROM jobs`

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*domain.Job, error) {
	j := &domain.Job{}
	var facts []byte
	err := row.Scan(&j.ID, &j.Pack, &j.CustomerID, &j.CustomerName, &j.MerchantID, &j.MerchantName,
		&j.Status, &j.Milestone, &facts, &j.SubtotalCents, &j.DeliveryFeeCents, &j.SurgeMultiplier, &j.TotalCents,
		&j.PaymentMethod, &j.AgentID, &j.CancelReason, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan job: %w", err)
	}
	if err := json.Unmarshal(facts, &j.Facts); err != nil {
		return nil, fmt.Errorf("job %s: facts column is not a JSON object of booleans: %w", j.ID, err)
	}
	return j, nil
}

func factsJSON(f map[string]bool) ([]byte, error) {
	if f == nil {
		f = map[string]bool{}
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encode facts: %w", err)
	}
	return b, nil
}

func windowStart(w *events.TimeWindow) any {
	if w == nil {
		return nil
	}
	return w.Start
}

func windowEnd(w *events.TimeWindow) any {
	if w == nil {
		return nil
	}
	return w.End
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
