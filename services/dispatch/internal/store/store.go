// Package store persists agents and assignments, including the PostGIS
// nearest-neighbour query that lists the free agents a job could go to.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

var ErrNotFound = errors.New("not found")

type Agent struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Vehicle string  `json:"vehicle"`
	Status  string  `json:"status"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	// Capacity module: what the vehicle carries (0 = not declared) and
	// what it can do (cold_chain, tail_lift, ...).
	CapacityGrams int64    `json:"capacityGrams,omitempty"`
	CapacityCm3   int64    `json:"capacityCm3,omitempty"`
	Capabilities  []string `json:"capabilities"`
}

type Assignment struct {
	ID         string  `json:"id"`
	JobID      string  `json:"jobId"`
	AgentID    string  `json:"agentId"`
	MerchantID string  `json:"merchantId,omitempty"`
	CustomerID string  `json:"customerId,omitempty"`
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
	// Next is the first stop not done yet (nil once all are). The agent
	// goes there; the simulation moves toward it.
	Next *AssignmentStop `json:"next,omitempty"`
	// Stops is every stop in order, loaded only where the caller needs it.
	Stops []AssignmentStop `json:"stops,omitempty"`
}

// AssignmentStop is one stop on an assignment's route.
type AssignmentStop struct {
	Seq         int        `json:"seq"`
	Kind        string     `json:"kind"`
	Lat         float64    `json:"lat"`
	Lng         float64    `json:"lng"`
	Label       string     `json:"label,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	// Proof is what completing this stop needs ("" for nothing).
	Proof       string `json:"proof,omitempty"`
	PinHash     string `json:"-"` // never sent: a 4-digit PIN falls out of its hash
	PinAttempts int    `json:"pinAttempts,omitempty"`
}

// Proof is what an agent captured at a stop.
type Proof struct {
	ID           string          `json:"id"`
	AssignmentID string          `json:"assignmentId"`
	Seq          int             `json:"seq"`
	Kind         string          `json:"kind"`
	BlobKey      string          `json:"-"`
	ContentType  string          `json:"contentType,omitempty"`
	SizeBytes    int64           `json:"sizeBytes,omitempty"`
	Details      json.RawMessage `json:"details,omitempty"`
	SubmittedBy  string          `json:"submittedBy"`
	CreatedAt    time.Time       `json:"createdAt"`
}

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) RegisterAgent(ctx context.Context, d *Agent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, name, vehicle, status, lat, lng, location, capacity_g, capacity_cm3, capabilities)
		VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($6,$5), 4326), $7, $8, $9)`,
		d.ID, d.Name, d.Vehicle, d.Status, d.Lat, d.Lng, nullInt(d.CapacityGrams), nullInt(d.CapacityCm3), capabilities(d.Capabilities))
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
	rows, err := s.pool.Query(ctx, `SELECT id, name, vehicle, status, lat, lng, coalesce(capacity_g,0), coalesce(capacity_cm3,0), capabilities FROM agents ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		d := &Agent{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng, &d.CapacityGrams, &d.CapacityCm3, &d.Capabilities); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Candidate is a free agent with what a dispatch strategy decides on.
type Candidate struct {
	Agent
	DistanceMeters   float64
	AssignmentsToday int
}

// AvailableCandidates lists up to limit AVAILABLE agents, nearest to the
// route's pickup first (the PostGIS <-> operator on the GiST index of
// agents.location). With capacity on, only agents whose declared vehicle
// fits the job and has every required capability: filtered in SQL, so a
// fitting truck behind twenty unfitting vans is still found. Nothing is
// locked: the strategy picks, then LockAvailable claims the pick, which
// may have gone meanwhile.
func AvailableCandidates(ctx context.Context, tx pgx.Tx, r *Route, capacity bool, limit int) ([]Candidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.name, a.vehicle, a.status, a.lat, a.lng,
		       coalesce(a.capacity_g,0), coalesce(a.capacity_cm3,0), a.capabilities,
		       ST_Distance(a.location, ST_SetSRID(ST_MakePoint($1,$2), 4326)::geography),
		       (SELECT count(*) FROM assignments s WHERE s.agent_id = a.id AND s.created_at >= date_trunc('day', now()))
		FROM agents a
		WHERE a.status = 'AVAILABLE'
		  AND (NOT $4 OR (a.capacity_g >= $5 AND a.capacity_cm3 >= $6 AND a.capabilities @> $7))
		ORDER BY a.location <-> ST_SetSRID(ST_MakePoint($1,$2), 4326)
		LIMIT $3`,
		r.PickupLng, r.PickupLat, limit, capacity, r.WeightGrams, r.VolumeCm3, capabilities(r.Requires))
	if err != nil {
		return nil, fmt.Errorf("list available agents: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ID, &c.Name, &c.Vehicle, &c.Status, &c.Lat, &c.Lng, &c.CapacityGrams, &c.CapacityCm3, &c.Capabilities,
			&c.DistanceMeters, &c.AssignmentsToday); err != nil {
			return nil, fmt.Errorf("scan available agent: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LockAvailable locks agent id if it is still AVAILABLE and nobody else
// holds it, so two concurrent assignments can't claim the same agent.
// ErrNotFound means it was taken in the meantime.
func LockAvailable(ctx context.Context, tx pgx.Tx, id string) error {
	var got string
	err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id = $1 AND status = 'AVAILABLE' FOR UPDATE SKIP LOCKED`, id).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock agent %s: %w", id, err)
	}
	return nil
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

// InsertAssignment stores the assignment and every stop on its route.
func InsertAssignment(ctx context.Context, tx pgx.Tx, d *Assignment, stops []events.Stop) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO assignments (id, job_id, agent_id, merchant_id, customer_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
			pickup_seq, dropoff_seq, status, eta_minutes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		d.ID, d.JobID, d.AgentID, nullStr(d.MerchantID), nullStr(d.CustomerID), d.PickupLat, d.PickupLng, d.DropoffLat, d.DropoffLng,
		d.PickupSeq, d.DropoffSeq, d.Status, d.EtaMinutes)
	if err != nil {
		return fmt.Errorf("insert assignment: %w", err)
	}
	for _, s := range stops {
		_, err := tx.Exec(ctx, `
			INSERT INTO assignment_stops (assignment_id, seq, kind, lat, lng, label, proof, pin_hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			d.ID, s.Seq, s.Kind, s.Location.Lat, s.Location.Lng, nullStr(s.Location.Label), nullStr(s.Proof), nullStr(s.PinHash))
		if err != nil {
			return fmt.Errorf("insert assignment %s stop %d: %w", d.ID, s.Seq, err)
		}
	}
	return nil
}

// Stops loads an assignment's route, in order.
func Stops(ctx context.Context, q db.Queryer, assignmentID string) ([]AssignmentStop, error) {
	rows, err := q.Query(ctx, `
		SELECT seq, kind, lat, lng, coalesce(label,''), completed_at, coalesce(proof,''), coalesce(pin_hash,''), pin_attempts
		FROM assignment_stops WHERE assignment_id = $1 ORDER BY seq`, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("list stops of assignment %s: %w", assignmentID, err)
	}
	defer rows.Close()
	var out []AssignmentStop
	for rows.Next() {
		var s AssignmentStop
		if err := rows.Scan(&s.Seq, &s.Kind, &s.Lat, &s.Lng, &s.Label, &s.CompletedAt, &s.Proof, &s.PinHash, &s.PinAttempts); err != nil {
			return nil, fmt.Errorf("scan assignment stop: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// InsertProof records what was captured at a stop. A second proof for the
// same stop is ErrProofExists.
func InsertProof(ctx context.Context, tx pgx.Tx, p *Proof) error {
	details := p.Details
	if details == nil {
		details = json.RawMessage("{}")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO stop_proofs (id, assignment_id, seq, kind, blob_key, content_type, size_bytes, details, submitted_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		p.ID, p.AssignmentID, p.Seq, p.Kind, nullStr(p.BlobKey), nullStr(p.ContentType), nullInt(p.SizeBytes), details, p.SubmittedBy)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrProofExists
	}
	if err != nil {
		return fmt.Errorf("insert proof for stop %d of assignment %s: %w", p.Seq, p.AssignmentID, err)
	}
	return nil
}

// ErrProofExists: the stop already has its proof.
var ErrProofExists = errors.New("stop already has proof")

// GetProof returns the proof captured at a stop, or ErrNotFound.
func GetProof(ctx context.Context, q db.Queryer, assignmentID string, seq int) (*Proof, error) {
	p := &Proof{}
	var blobKey, contentType *string
	var size *int64
	err := q.QueryRow(ctx, `
		SELECT id, assignment_id, seq, kind, blob_key, content_type, size_bytes, details, submitted_by, created_at
		FROM stop_proofs WHERE assignment_id = $1 AND seq = $2`, assignmentID, seq).
		Scan(&p.ID, &p.AssignmentID, &p.Seq, &p.Kind, &blobKey, &contentType, &size, &p.Details, &p.SubmittedBy, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get proof for stop %d of assignment %s: %w", seq, assignmentID, err)
	}
	if blobKey != nil {
		p.BlobKey = *blobKey
	}
	if contentType != nil {
		p.ContentType = *contentType
	}
	if size != nil {
		p.SizeBytes = *size
	}
	return p, nil
}

// LockPIN locks a stop's PIN row for the rest of tx and returns its hash
// and wrong tries so far, so concurrent guesses are counted one at a time.
func LockPIN(ctx context.Context, tx pgx.Tx, assignmentID string, seq int) (hash string, attempts int, err error) {
	err = tx.QueryRow(ctx, `SELECT coalesce(pin_hash,''), pin_attempts FROM assignment_stops
		WHERE assignment_id = $1 AND seq = $2 FOR UPDATE`, assignmentID, seq).Scan(&hash, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("lock PIN of stop %d on assignment %s: %w", seq, assignmentID, err)
	}
	return hash, attempts, nil
}

// CountWrongPIN records one more wrong PIN at a stop and returns the total
// so far. It commits with its own transaction's caller: a wrong guess must
// count even though the request fails.
func CountWrongPIN(ctx context.Context, tx pgx.Tx, assignmentID string, seq int) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `UPDATE assignment_stops SET pin_attempts = pin_attempts + 1
		WHERE assignment_id = $1 AND seq = $2 RETURNING pin_attempts`, assignmentID, seq).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count wrong PIN at stop %d of assignment %s: %w", seq, assignmentID, err)
	}
	return n, nil
}

// MarkStopDone records stop seq of an assignment as done now.
func MarkStopDone(ctx context.Context, tx pgx.Tx, assignmentID string, seq int) error {
	tag, err := tx.Exec(ctx, `UPDATE assignment_stops SET completed_at = now() WHERE assignment_id = $1 AND seq = $2 AND completed_at IS NULL`, assignmentID, seq)
	if err != nil {
		return fmt.Errorf("complete stop %d of assignment %s: %w", seq, assignmentID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete stop %d of assignment %s: no open stop to complete", seq, assignmentID)
	}
	return nil
}

func (s *Store) GetByJob(ctx context.Context, jobID string) (*Assignment, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE a.job_id=$1`, jobID)
	return scanAssignment(row)
}

func (s *Store) Get(ctx context.Context, id string) (*Assignment, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE a.id=$1`, id)
	return scanAssignment(row)
}

func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*Assignment, error) {
	row := tx.QueryRow(ctx, baseSelect+` WHERE a.id=$1 FOR UPDATE OF a`, id)
	return scanAssignment(row)
}

// ListActive returns every simulated assignment still on the road, for the
// location simulation loop to advance. Listed by status rather than
// "not COMPLETED" so CANCELLED assignments stop moving too.
func (s *Store) ListActive(ctx context.Context) ([]*Assignment, error) {
	rows, err := s.pool.Query(ctx, baseSelect+` WHERE a.status IN ('ASSIGNED','PICKED_UP') AND a.simulated = true`)
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
			picked_up_at = CASE WHEN $2 = 'PICKED_UP' THEN coalesce(picked_up_at, now()) ELSE picked_up_at END,
			completed_at = CASE WHEN $2 = 'COMPLETED' THEN now() ELSE completed_at END
		WHERE id = $1
		RETURNING extract(epoch FROM now() - created_at)::bigint`, id, status).Scan(&elapsedSeconds)
	if err != nil {
		return 0, fmt.Errorf("advance assignment %s to %s: %w", id, status, err)
	}
	return elapsedSeconds, nil
}

func GetByJobForUpdate(ctx context.Context, tx pgx.Tx, jobID string) (*Assignment, error) {
	return scanAssignment(tx.QueryRow(ctx, baseSelect+` WHERE a.job_id=$1 FOR UPDATE OF a`, jobID))
}

func (s *Store) GetAgent(ctx context.Context, id string) (*Agent, error) {
	d := &Agent{}
	err := s.pool.QueryRow(ctx, `SELECT id, name, vehicle, status, lat, lng, coalesce(capacity_g,0), coalesce(capacity_cm3,0), capabilities FROM agents WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng, &d.CapacityGrams, &d.CapacityCm3, &d.Capabilities)
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

// baseSelect joins each assignment's next open stop. Callers that lock
// must say FOR UPDATE OF a: the joined side can't be locked.
const baseSelect = `SELECT a.id, a.job_id, coalesce(a.agent_id,''), coalesce(a.merchant_id,''), coalesce(a.customer_id,''), a.pickup_lat, a.pickup_lng,
	a.dropoff_lat, a.dropoff_lng, a.pickup_seq, a.dropoff_seq, a.status, a.simulated, a.eta_minutes,
	n.seq, n.kind, n.lat, n.lng, n.label, n.proof
	FROM assignments a
	LEFT JOIN LATERAL (
		SELECT seq, kind, lat, lng, coalesce(label,'') AS label, coalesce(proof,'') AS proof FROM assignment_stops
		WHERE assignment_id = a.id AND completed_at IS NULL ORDER BY seq LIMIT 1
	) n ON true`

type rowScanner interface{ Scan(dest ...any) error }

func scanAssignment(row rowScanner) (*Assignment, error) {
	d := &Assignment{}
	var nSeq *int
	var nKind, nLabel, nProof *string
	var nLat, nLng *float64
	err := row.Scan(&d.ID, &d.JobID, &d.AgentID, &d.MerchantID, &d.CustomerID, &d.PickupLat, &d.PickupLng,
		&d.DropoffLat, &d.DropoffLng, &d.PickupSeq, &d.DropoffSeq, &d.Status, &d.Simulated, &d.EtaMinutes,
		&nSeq, &nKind, &nLat, &nLng, &nLabel, &nProof)
	if err == nil && nSeq != nil {
		d.Next = &AssignmentStop{Seq: *nSeq, Kind: *nKind, Lat: *nLat, Lng: *nLng, Label: *nLabel, Proof: *nProof}
	}
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
	// Stops is the whole route in order (multi-stop jobs have more than
	// the pickup and dropoff above).
	Stops []events.Stop
	// CustomerID is who may see the proof captured on the route.
	CustomerID string
	// Scheduling module: don't assign before this (the pickup window
	// opening, less the lead time). Nil: as soon as an agent is free.
	NotBefore *time.Time
	// open_offer: the job is out to agents until then.
	OfferedUntil *time.Time
	// Capacity module: the job's size and the capabilities it needs.
	WeightGrams int64
	VolumeCm3   int64
	Requires    []string
}

func InsertPending(ctx context.Context, tx pgx.Tx, p *Route) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO pending_assignments (job_id, merchant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, pickup_seq, dropoff_seq,
			weight_g, volume_cm3, requires, stops, not_before, customer_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (job_id) DO NOTHING`,
		p.JobID, nullStr(p.MerchantID), p.PickupLat, p.PickupLng, p.DropoffLat, p.DropoffLng, p.PickupSeq, p.DropoffSeq,
		p.WeightGrams, p.VolumeCm3, capabilities(p.Requires), stopsJSON(p.Stops), p.NotBefore, nullStr(p.CustomerID))
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

// ListPending returns the queued jobs that may be assigned now: booked
// ones wait until their not_before, offered ones until the offer expires.
func (s *Store) ListPending(ctx context.Context) ([]*Route, error) {
	return listPending(ctx, s.pool, `WHERE (not_before IS NULL OR not_before <= now())
		AND (offered_until IS NULL OR offered_until <= now()) ORDER BY created_at`)
}

// ListAllPending is the whole queue, due or not, for an admin choosing
// what to assign by hand.
func (s *Store) ListAllPending(ctx context.Context) ([]*Route, error) {
	return listPending(ctx, s.pool, "ORDER BY created_at")
}

// GetPendingForUpdate locks a queued job, for assigning it by hand or to
// an agent who accepted its offer.
func GetPendingForUpdate(ctx context.Context, tx pgx.Tx, jobID string) (*Route, error) {
	out, err := listPending(ctx, tx, `WHERE job_id = $1 FOR UPDATE`, jobID)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out[0], nil
}

// listPending reads queue rows; tail is everything after FROM (WHERE,
// ORDER BY, FOR UPDATE).
func listPending(ctx context.Context, q db.Queryer, tail string, args ...any) ([]*Route, error) {
	rows, err := q.Query(ctx, `SELECT job_id, coalesce(merchant_id,''), pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, pickup_seq, dropoff_seq,
		weight_g, volume_cm3, requires, stops, not_before, coalesce(customer_id,''), offered_until
		FROM pending_assignments `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("list pending assignments: %w", err)
	}
	defer rows.Close()
	var out []*Route
	for rows.Next() {
		p := &Route{}
		if err := rows.Scan(&p.JobID, &p.MerchantID, &p.PickupLat, &p.PickupLng, &p.DropoffLat, &p.DropoffLng, &p.PickupSeq, &p.DropoffSeq,
			&p.WeightGrams, &p.VolumeCm3, &p.Requires, &p.Stops, &p.NotBefore, &p.CustomerID, &p.OfferedUntil); err != nil {
			return nil, fmt.Errorf("scan pending assignment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Offer is an open offer of a queued job to an agent.
type Offer struct {
	JobID      string    `json:"jobId"`
	ExpiresAt  time.Time `json:"expiresAt"`
	PickupLat  float64   `json:"pickupLat"`
	PickupLng  float64   `json:"pickupLng"`
	DropoffLat float64   `json:"dropoffLat"`
	DropoffLng float64   `json:"dropoffLng"`
	Stops      int       `json:"stops"`
	WeightG    int64     `json:"weightGrams,omitempty"`
}

// SetOffers replaces a queued job's offers with one per agent, open until
// expires, and holds the job back from the retrier until then.
func SetOffers(ctx context.Context, tx pgx.Tx, jobID string, agentIDs []string, expires time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM assignment_offers WHERE job_id = $1`, jobID); err != nil {
		return fmt.Errorf("clear offers for job %s: %w", jobID, err)
	}
	for _, id := range agentIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO assignment_offers (job_id, agent_id, expires_at) VALUES ($1,$2,$3)`, jobID, id, expires); err != nil {
			return fmt.Errorf("offer job %s to agent %s: %w", jobID, id, err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE pending_assignments SET offered_until = $2 WHERE job_id = $1`, jobID, expires); err != nil {
		return fmt.Errorf("hold offered job %s: %w", jobID, err)
	}
	return nil
}

// HasOpenOffer reports whether agentID holds an unexpired offer of jobID.
func HasOpenOffer(ctx context.Context, tx pgx.Tx, jobID, agentID string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assignment_offers WHERE job_id = $1 AND agent_id = $2 AND expires_at > now())`,
		jobID, agentID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check offer of job %s to agent %s: %w", jobID, agentID, err)
	}
	return ok, nil
}

// OffersFor lists an agent's open offers, soonest to expire first.
func (s *Store) OffersFor(ctx context.Context, agentID string) ([]Offer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.job_id, o.expires_at, p.pickup_lat, p.pickup_lng, p.dropoff_lat, p.dropoff_lng, jsonb_array_length(p.stops), p.weight_g
		FROM assignment_offers o JOIN pending_assignments p ON p.job_id = o.job_id
		WHERE o.agent_id = $1 AND o.expires_at > now() ORDER BY o.expires_at`, agentID)
	if err != nil {
		return nil, fmt.Errorf("list offers for agent %s: %w", agentID, err)
	}
	defer rows.Close()
	out := []Offer{}
	for rows.Next() {
		var o Offer
		if err := rows.Scan(&o.JobID, &o.ExpiresAt, &o.PickupLat, &o.PickupLng, &o.DropoffLat, &o.DropoffLng, &o.Stops, &o.WeightG); err != nil {
			return nil, fmt.Errorf("scan offer: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetAgentTx loads an agent inside tx.
func GetAgentTx(ctx context.Context, tx pgx.Tx, id string) (*Agent, error) {
	d := &Agent{}
	err := tx.QueryRow(ctx, `SELECT id, name, vehicle, status, lat, lng, coalesce(capacity_g,0), coalesce(capacity_cm3,0), capabilities
		FROM agents WHERE id=$1`, id).Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng, &d.CapacityGrams, &d.CapacityCm3, &d.Capabilities)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get agent %s: %w", id, err)
	}
	return d, nil
}

func stopsJSON(s []events.Stop) []events.Stop {
	if s == nil {
		return []events.Stop{} // the column is NOT NULL; "[]" not "null"
	}
	return s
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// capabilities makes a nil list an empty array: Postgres' @> against NULL
// is NULL, which would silently match nothing.
func capabilities(c []string) []string {
	if c == nil {
		return []string{}
	}
	return c
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
