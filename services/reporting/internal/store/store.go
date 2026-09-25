// Package store maintains the jobs_projection read model and serves the
// aggregate queries behind every admin report.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

// --- Projection writers (called from consumers) ---

func UpsertCreated(ctx context.Context, q db.Queryer, jobID, merchantID, merchantName, customerID string, totalCents int64, createdAt time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO jobs_projection (job_id, merchant_id, merchant_name, customer_id, status, total_cents, created_at)
		VALUES ($1,$2,$3,$4,'CREATED',$5,$6)
		ON CONFLICT (job_id) DO NOTHING`,
		jobID, merchantID, merchantName, customerID, totalCents, createdAt)
	if err != nil {
		return fmt.Errorf("upsert job projection: %w", err)
	}
	return nil
}

func UpdateStatus(ctx context.Context, q db.Queryer, jobID, status string, completedAt *time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE jobs_projection SET status=$2, completed_at=coalesce($3, completed_at), updated_at=now() WHERE job_id=$1`,
		jobID, status, completedAt)
	if err != nil {
		return fmt.Errorf("update job projection status: %w", err)
	}
	return nil
}

func SetAgent(ctx context.Context, q db.Queryer, jobID, agentID string) error {
	_, err := q.Exec(ctx, `UPDATE jobs_projection SET agent_id=$2, updated_at=now() WHERE job_id=$1`, jobID, agentID)
	if err != nil {
		return fmt.Errorf("set job projection agent: %w", err)
	}
	return nil
}

// --- Reports ---

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

type MerchantStats struct {
	MerchantID           string  `json:"merchantId"`
	JobsCount            int64   `json:"jobsCount"`
	RevenueCents         int64   `json:"revenueCents"`
	AvgCompletionMinutes float64 `json:"avgCompletionMinutes"`
}

func (s *Store) MerchantStats(ctx context.Context, merchantID string) (*MerchantStats, error) {
	r := &MerchantStats{MerchantID: merchantID}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(total_cents) FILTER (WHERE status != 'CANCELLED'), 0),
		       coalesce(avg(extract(epoch from completed_at - created_at)) FILTER (WHERE completed_at IS NOT NULL), 0) / 60
		FROM jobs_projection WHERE merchant_id=$1`,
		merchantID).Scan(&r.JobsCount, &r.RevenueCents, &r.AvgCompletionMinutes)
	if err != nil {
		return nil, fmt.Errorf("merchant stats: %w", err)
	}
	return r, nil
}

type AgentStats struct {
	AgentID              string  `json:"agentId"`
	AssignmentsCount     int64   `json:"assignmentsCount"`
	AvgCompletionMinutes float64 `json:"avgCompletionMinutes"`
}

func (s *Store) AgentStats(ctx context.Context, agentID string) (*AgentStats, error) {
	d := &AgentStats{AgentID: agentID}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='DELIVERED'),
		       coalesce(avg(extract(epoch from completed_at - created_at)) FILTER (WHERE completed_at IS NOT NULL), 0) / 60
		FROM jobs_projection WHERE agent_id=$1`,
		agentID).Scan(&d.AssignmentsCount, &d.AvgCompletionMinutes)
	if err != nil {
		return nil, fmt.Errorf("agent stats: %w", err)
	}
	return d, nil
}

type Overview struct {
	TotalJobs            int64            `json:"totalJobs"`
	TotalRevenueCents    int64            `json:"totalRevenueCents"`
	AvgCompletionMinutes float64          `json:"avgCompletionMinutes"`
	JobsByStatus         map[string]int64 `json:"jobsByStatus"`
}

func (s *Store) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{JobsByStatus: map[string]int64{}}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(total_cents) FILTER (WHERE status != 'CANCELLED'), 0),
		       coalesce(avg(extract(epoch from completed_at - created_at)) FILTER (WHERE completed_at IS NOT NULL), 0) / 60
		FROM jobs_projection`).Scan(&o.TotalJobs, &o.TotalRevenueCents, &o.AvgCompletionMinutes)
	if err != nil {
		return nil, fmt.Errorf("overview: %w", err)
	}

	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM jobs_projection GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("jobs by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		o.JobsByStatus[status] = count
	}
	return o, rows.Err()
}
