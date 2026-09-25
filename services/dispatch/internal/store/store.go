// Package store persists agents and assignments, including the PostGIS
// nearest-neighbor query used to assign the closest available agent.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

var ErrNotFound = errors.New("not found")

type Agent struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Vehicle string  `json:"vehicle"`
	Status  string  `json:"status"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type Assignment struct {
	ID         string  `json:"id"`
	JobID      string  `json:"jobId"`
	AgentID    string  `json:"agentId"`
	MerchantID string  `json:"merchantId,omitempty"`
	PickupLat  float64 `json:"pickupLat"`
	PickupLng  float64 `json:"pickupLng"`
	DropoffLat float64 `json:"dropoffLat"`
	DropoffLng float64 `json:"dropoffLng"`
	// The job's stop numbers for the pickup and the dropoff, which is what
	// dispatch.stop-completed reports back to the jobs module.
	PickupSeq  int    `json:"pickupSeq"`
	DropoffSeq int    `json:"dropoffSeq"`
	Status     string `json:"status"`
	Simulated  bool   `json:"simulated"`
	EtaMinutes int    `json:"etaMinutes"`
}

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) RegisterAgent(ctx context.Context, d *Agent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, name, vehicle, status, lat, lng, location)
		VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($6,$5), 4326))`,
		d.ID, d.Name, d.Vehicle, d.Status, d.Lat, d.Lng)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	return nil
}

// ErrAgentBusy: an agent on an assignment can't toggle themselves AVAILABLE
// (they'd be dispatched a second job) or OFFLINE (the assignment would be
// orphaned). BUSY ends only via completion or cancellation.
var ErrAgentBusy = errors.New("agent is on an assignment")

func (s *Store) SetAgentStatus(ctx context.Context, id, status string) error {
	var current string
	err := s.pool.QueryRow(ctx, `
		UPDATE agents d SET status = CASE WHEN d.status = 'BUSY' THEN d.status ELSE $2 END, updated_at = now()
		FROM agents old WHERE d.id = $1 AND old.id = d.id
		RETURNING old.status`, id, status).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("set agent %s status: %w", id, err)
	}
	if current == "BUSY" {
		return ErrAgentBusy
	}
	return nil
}

func (s *Store) ListAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, vehicle, status, lat, lng FROM agents ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		d := &Agent{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindNearestAvailableForUpdate uses the PostGIS <-> distance operator
// against the GiST index on agents.location to pick the closest AVAILABLE
// agent to (lat, lng), locking that row so two concurrent assignments
// cannot pick the same agent.
func FindNearestAvailableForUpdate(ctx context.Context, tx pgx.Tx, lat, lng float64) (*Agent, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, name, vehicle, status, lat, lng
		FROM agents
		WHERE status = 'AVAILABLE'
		ORDER BY location <-> ST_SetSRID(ST_MakePoint($1,$2), 4326)
		LIMIT 1
		FOR UPDATE SKIP LOCKED`,
		lng, lat)
	d := &Agent{}
	err := row.Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find nearest agent: %w", err)
	}
	return d, nil
}

func MarkAgentBusy(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE agents SET status='BUSY', updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark agent busy: %w", err)
	}
	return nil
}

func MarkAgentAvailable(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE agents SET status='AVAILABLE', updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark agent available: %w", err)
	}
	return nil
}

func UpdateAgentLocation(ctx context.Context, q db.Queryer, id string, lat, lng float64) error {
	_, err := q.Exec(ctx, `
		UPDATE agents SET lat=$2, lng=$3, location=ST_SetSRID(ST_MakePoint($3,$2),4326), updated_at=now() WHERE id=$1`,
		id, lat, lng)
	if err != nil {
		return fmt.Errorf("update agent location: %w", err)
	}
	return nil
}

func InsertAssignment(ctx context.Context, tx pgx.Tx, d *Assignment) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO assignments (id, job_id, agent_id, merchant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
			pickup_seq, dropoff_seq, status, eta_minutes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		d.ID, d.JobID, d.AgentID, nullStr(d.MerchantID), d.PickupLat, d.PickupLng, d.DropoffLat, d.DropoffLng,
		d.PickupSeq, d.DropoffSeq, d.Status, d.EtaMinutes)
	if err != nil {
		return fmt.Errorf("insert assignment: %w", err)
	}
	return nil
}

func (s *Store) GetByJob(ctx context.Context, jobID string) (*Assignment, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE job_id=$1`, jobID)
	return scanAssignment(row)
}

func (s *Store) Get(ctx context.Context, id string) (*Assignment, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE id=$1`, id)
	return scanAssignment(row)
}

func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*Assignment, error) {
	row := tx.QueryRow(ctx, baseSelect+` WHERE id=$1 FOR UPDATE`, id)
	return scanAssignment(row)
}

// ListActive returns every simulated assignment still on the road, for the
// location simulation loop to advance. Listed by status rather than
// "not COMPLETED" so CANCELLED assignments stop moving too.
func (s *Store) ListActive(ctx context.Context) ([]*Assignment, error) {
	rows, err := s.pool.Query(ctx, baseSelect+` WHERE status IN ('ASSIGNED','PICKED_UP') AND simulated = true`)
	if err != nil {
		return nil, fmt.Errorf("list active assignments: %w", err)
	}
	defer rows.Close()
	var out []*Assignment
	for rows.Next() {
		d, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AdvanceStatus sets the new status and its timestamp column, and returns
// the seconds since the assignment was created (i.e. since assignment).
func AdvanceStatus(ctx context.Context, tx pgx.Tx, id, status string) (elapsedSeconds int64, err error) {
	err = tx.QueryRow(ctx, `
		UPDATE assignments SET status = $2, updated_at = now(),
			picked_up_at = CASE WHEN $2 = 'PICKED_UP' THEN now() ELSE picked_up_at END,
			completed_at = CASE WHEN $2 = 'COMPLETED' THEN now() ELSE completed_at END
		WHERE id = $1
		RETURNING extract(epoch FROM now() - created_at)::bigint`, id, status).Scan(&elapsedSeconds)
	if err != nil {
		return 0, fmt.Errorf("advance assignment %s to %s: %w", id, status, err)
	}
	return elapsedSeconds, nil
}

func GetByJobForUpdate(ctx context.Context, tx pgx.Tx, jobID string) (*Assignment, error) {
	return scanAssignment(tx.QueryRow(ctx, baseSelect+` WHERE job_id=$1 FOR UPDATE`, jobID))
}

func (s *Store) GetAgent(ctx context.Context, id string) (*Agent, error) {
	d := &Agent{}
	err := s.pool.QueryRow(ctx, `SELECT id, name, vehicle, status, lat, lng FROM agents WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return d, nil
}

// LockJob takes a transaction-scoped advisory lock on jobID. Assignment
// (merchant.job-ready, the pending retrier) and cancellation
// (jobs.cancelled) both take it first, so they serialize per job: without
// it, a cancel could commit its tombstone while an assignment that already
// checked the tombstone is still inserting an assignment, leaking a BUSY agent.
func LockJob(ctx context.Context, tx pgx.Tx, jobID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, jobID); err != nil {
		return fmt.Errorf("lock job %s: %w", jobID, err)
	}
	return nil
}

func MarkJobCancelled(ctx context.Context, tx pgx.Tx, jobID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO cancelled_jobs (job_id) VALUES ($1) ON CONFLICT DO NOTHING`, jobID); err != nil {
		return fmt.Errorf("tombstone job %s: %w", jobID, err)
	}
	return nil
}

func IsJobCancelled(ctx context.Context, tx pgx.Tx, jobID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cancelled_jobs WHERE job_id=$1)`, jobID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tombstone for job %s: %w", jobID, err)
	}
	return exists, nil
}

func UpdateStatus(ctx context.Context, tx pgx.Tx, id, status string) error {
	_, err := tx.Exec(ctx, `UPDATE assignments SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	if err != nil {
		return fmt.Errorf("update assignment status: %w", err)
	}
	return nil
}

func UpdateETA(ctx context.Context, q db.Queryer, id string, etaMinutes int) error {
	_, err := q.Exec(ctx, `UPDATE assignments SET eta_minutes=$2, updated_at=now() WHERE id=$1`, id, etaMinutes)
	if err != nil {
		return fmt.Errorf("update assignment eta: %w", err)
	}
	return nil
}

const baseSelect = `SELECT id, job_id, coalesce(agent_id,''), coalesce(merchant_id,''), pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
	pickup_seq, dropoff_seq, status, simulated, eta_minutes FROM assignments`

type rowScanner interface{ Scan(dest ...any) error }

func scanAssignment(row rowScanner) (*Assignment, error) {
	d := &Assignment{}
	err := row.Scan(&d.ID, &d.JobID, &d.AgentID, &d.MerchantID, &d.PickupLat, &d.PickupLng,
		&d.DropoffLat, &d.DropoffLng, &d.PickupSeq, &d.DropoffSeq, &d.Status, &d.Simulated, &d.EtaMinutes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan assignment: %w", err)
	}
	return d, nil
}

// --- Routes and pending assignments ---

// Route is a job as dispatch sees it: where to pick up, where to drop off,
// and which of the job's stops those are. It is what gets assigned, and
// what waits in pending_assignments when no agent is free.
type Route struct {
	JobID      string
	MerchantID string // "" when the pickup isn't at a merchant
	PickupLat  float64
	PickupLng  float64
	DropoffLat float64
	DropoffLng float64
	PickupSeq  int
	DropoffSeq int
}

func InsertPending(ctx context.Context, tx pgx.Tx, p *Route) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO pending_assignments (job_id, merchant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, pickup_seq, dropoff_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (job_id) DO NOTHING`,
		p.JobID, nullStr(p.MerchantID), p.PickupLat, p.PickupLng, p.DropoffLat, p.DropoffLng, p.PickupSeq, p.DropoffSeq)
	if err != nil {
		return fmt.Errorf("insert pending assignment: %w", err)
	}
	return nil
}

func DeletePending(ctx context.Context, tx pgx.Tx, jobID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM pending_assignments WHERE job_id=$1`, jobID)
	if err != nil {
		return fmt.Errorf("delete pending assignment: %w", err)
	}
	return nil
}

func (s *Store) ListPending(ctx context.Context) ([]*Route, error) {
	rows, err := s.pool.Query(ctx, `SELECT job_id, coalesce(merchant_id,''), pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, pickup_seq, dropoff_seq
		FROM pending_assignments ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending assignments: %w", err)
	}
	defer rows.Close()
	var out []*Route
	for rows.Next() {
		p := &Route{}
		if err := rows.Scan(&p.JobID, &p.MerchantID, &p.PickupLat, &p.PickupLng, &p.DropoffLat, &p.DropoffLng, &p.PickupSeq, &p.DropoffSeq); err != nil {
			return nil, fmt.Errorf("scan pending assignment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
