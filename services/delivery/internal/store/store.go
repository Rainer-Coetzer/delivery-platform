// Package store persists drivers and deliveries, including the PostGIS
// nearest-neighbor query used to assign the closest available driver.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

var ErrNotFound = errors.New("not found")

type Driver struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Vehicle string  `json:"vehicle"`
	Status  string  `json:"status"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type Delivery struct {
	ID           string  `json:"id"`
	OrderID      string  `json:"orderId"`
	DriverID     string  `json:"driverId"`
	RestaurantID string  `json:"restaurantId"`
	PickupLat    float64 `json:"pickupLat"`
	PickupLng    float64 `json:"pickupLng"`
	DropoffLat   float64 `json:"dropoffLat"`
	DropoffLng   float64 `json:"dropoffLng"`
	Status       string  `json:"status"`
	Simulated    bool    `json:"simulated"`
	EtaMinutes   int     `json:"etaMinutes"`
}

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) RegisterDriver(ctx context.Context, d *Driver) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO drivers (id, name, vehicle, status, lat, lng, location)
		VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($6,$5), 4326))`,
		d.ID, d.Name, d.Vehicle, d.Status, d.Lat, d.Lng)
	if err != nil {
		return fmt.Errorf("insert driver: %w", err)
	}
	return nil
}

// ErrDriverBusy: a driver on a delivery can't toggle themselves AVAILABLE
// (they'd be dispatched a second order) or OFFLINE (the delivery would be
// orphaned). BUSY ends only via completion or cancellation.
var ErrDriverBusy = errors.New("driver is on a delivery")

func (s *Store) SetDriverStatus(ctx context.Context, id, status string) error {
	var current string
	err := s.pool.QueryRow(ctx, `
		UPDATE drivers d SET status = CASE WHEN d.status = 'BUSY' THEN d.status ELSE $2 END, updated_at = now()
		FROM drivers old WHERE d.id = $1 AND old.id = d.id
		RETURNING old.status`, id, status).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("set driver %s status: %w", id, err)
	}
	if current == "BUSY" {
		return ErrDriverBusy
	}
	return nil
}

func (s *Store) ListDrivers(ctx context.Context) ([]*Driver, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, vehicle, status, lat, lng FROM drivers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	defer rows.Close()
	var out []*Driver
	for rows.Next() {
		d := &Driver{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng); err != nil {
			return nil, fmt.Errorf("scan driver: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindNearestAvailableForUpdate uses the PostGIS <-> distance operator
// against the GiST index on drivers.location to pick the closest AVAILABLE
// driver to (lat, lng), locking that row so two concurrent assignments
// cannot pick the same driver.
func FindNearestAvailableForUpdate(ctx context.Context, tx pgx.Tx, lat, lng float64) (*Driver, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, name, vehicle, status, lat, lng
		FROM drivers
		WHERE status = 'AVAILABLE'
		ORDER BY location <-> ST_SetSRID(ST_MakePoint($1,$2), 4326)
		LIMIT 1
		FOR UPDATE SKIP LOCKED`,
		lng, lat)
	d := &Driver{}
	err := row.Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find nearest driver: %w", err)
	}
	return d, nil
}

func MarkDriverBusy(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE drivers SET status='BUSY', updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark driver busy: %w", err)
	}
	return nil
}

func MarkDriverAvailable(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE drivers SET status='AVAILABLE', updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark driver available: %w", err)
	}
	return nil
}

func UpdateDriverLocation(ctx context.Context, q db.Queryer, id string, lat, lng float64) error {
	_, err := q.Exec(ctx, `
		UPDATE drivers SET lat=$2, lng=$3, location=ST_SetSRID(ST_MakePoint($3,$2),4326), updated_at=now() WHERE id=$1`,
		id, lat, lng)
	if err != nil {
		return fmt.Errorf("update driver location: %w", err)
	}
	return nil
}

func InsertDelivery(ctx context.Context, tx pgx.Tx, d *Delivery) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO deliveries (id, order_id, driver_id, restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, status, eta_minutes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		d.ID, d.OrderID, d.DriverID, d.RestaurantID, d.PickupLat, d.PickupLng, d.DropoffLat, d.DropoffLng, d.Status, d.EtaMinutes)
	if err != nil {
		return fmt.Errorf("insert delivery: %w", err)
	}
	return nil
}

func (s *Store) GetByOrder(ctx context.Context, orderID string) (*Delivery, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE order_id=$1`, orderID)
	return scanDelivery(row)
}

func (s *Store) Get(ctx context.Context, id string) (*Delivery, error) {
	row := s.pool.QueryRow(ctx, baseSelect+` WHERE id=$1`, id)
	return scanDelivery(row)
}

func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*Delivery, error) {
	row := tx.QueryRow(ctx, baseSelect+` WHERE id=$1 FOR UPDATE`, id)
	return scanDelivery(row)
}

// ListActive returns every simulated delivery still on the road, for the
// location simulation loop to advance. Listed by status rather than
// "not COMPLETED" so CANCELLED deliveries stop moving too.
func (s *Store) ListActive(ctx context.Context) ([]*Delivery, error) {
	rows, err := s.pool.Query(ctx, baseSelect+` WHERE status IN ('ASSIGNED','PICKED_UP') AND simulated = true`)
	if err != nil {
		return nil, fmt.Errorf("list active deliveries: %w", err)
	}
	defer rows.Close()
	var out []*Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AdvanceStatus sets the new status and its timestamp column, and returns
// the seconds since the delivery was created (i.e. since assignment).
func AdvanceStatus(ctx context.Context, tx pgx.Tx, id, status string) (elapsedSeconds int64, err error) {
	err = tx.QueryRow(ctx, `
		UPDATE deliveries SET status = $2, updated_at = now(),
			picked_up_at = CASE WHEN $2 = 'PICKED_UP' THEN now() ELSE picked_up_at END,
			completed_at = CASE WHEN $2 = 'COMPLETED' THEN now() ELSE completed_at END
		WHERE id = $1
		RETURNING extract(epoch FROM now() - created_at)::bigint`, id, status).Scan(&elapsedSeconds)
	if err != nil {
		return 0, fmt.Errorf("advance delivery %s to %s: %w", id, status, err)
	}
	return elapsedSeconds, nil
}

func GetByOrderForUpdate(ctx context.Context, tx pgx.Tx, orderID string) (*Delivery, error) {
	return scanDelivery(tx.QueryRow(ctx, baseSelect+` WHERE order_id=$1 FOR UPDATE`, orderID))
}

func (s *Store) GetDriver(ctx context.Context, id string) (*Driver, error) {
	d := &Driver{}
	err := s.pool.QueryRow(ctx, `SELECT id, name, vehicle, status, lat, lng FROM drivers WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Vehicle, &d.Status, &d.Lat, &d.Lng)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get driver %s: %w", id, err)
	}
	return d, nil
}

// LockOrder takes a transaction-scoped advisory lock on orderID. Assignment
// (restaurant.order-ready, the pending retrier) and cancellation
// (orders.cancelled) both take it first, so they serialize per order: without
// it, a cancel could commit its tombstone while an assignment that already
// checked the tombstone is still inserting a delivery, leaking a BUSY driver.
func LockOrder(ctx context.Context, tx pgx.Tx, orderID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orderID); err != nil {
		return fmt.Errorf("lock order %s: %w", orderID, err)
	}
	return nil
}

func MarkOrderCancelled(ctx context.Context, tx pgx.Tx, orderID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO cancelled_orders (order_id) VALUES ($1) ON CONFLICT DO NOTHING`, orderID); err != nil {
		return fmt.Errorf("tombstone order %s: %w", orderID, err)
	}
	return nil
}

func IsOrderCancelled(ctx context.Context, tx pgx.Tx, orderID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cancelled_orders WHERE order_id=$1)`, orderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tombstone for order %s: %w", orderID, err)
	}
	return exists, nil
}

func UpdateStatus(ctx context.Context, tx pgx.Tx, id, status string) error {
	_, err := tx.Exec(ctx, `UPDATE deliveries SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	if err != nil {
		return fmt.Errorf("update delivery status: %w", err)
	}
	return nil
}

func UpdateETA(ctx context.Context, q db.Queryer, id string, etaMinutes int) error {
	_, err := q.Exec(ctx, `UPDATE deliveries SET eta_minutes=$2, updated_at=now() WHERE id=$1`, id, etaMinutes)
	if err != nil {
		return fmt.Errorf("update delivery eta: %w", err)
	}
	return nil
}

const baseSelect = `SELECT id, order_id, coalesce(driver_id,''), restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, status, simulated, eta_minutes FROM deliveries`

type rowScanner interface{ Scan(dest ...any) error }

func scanDelivery(row rowScanner) (*Delivery, error) {
	d := &Delivery{}
	err := row.Scan(&d.ID, &d.OrderID, &d.DriverID, &d.RestaurantID, &d.PickupLat, &d.PickupLng,
		&d.DropoffLat, &d.DropoffLng, &d.Status, &d.Simulated, &d.EtaMinutes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan delivery: %w", err)
	}
	return d, nil
}

// --- Pending assignments (no driver was free at order-ready time) ---

type PendingAssignment struct {
	OrderID      string
	RestaurantID string
	PickupLat    float64
	PickupLng    float64
	DropoffLat   float64
	DropoffLng   float64
}

func InsertPending(ctx context.Context, tx pgx.Tx, p *PendingAssignment) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO pending_assignments (order_id, restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (order_id) DO NOTHING`,
		p.OrderID, p.RestaurantID, p.PickupLat, p.PickupLng, p.DropoffLat, p.DropoffLng)
	if err != nil {
		return fmt.Errorf("insert pending assignment: %w", err)
	}
	return nil
}

func DeletePending(ctx context.Context, tx pgx.Tx, orderID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM pending_assignments WHERE order_id=$1`, orderID)
	if err != nil {
		return fmt.Errorf("delete pending assignment: %w", err)
	}
	return nil
}

func (s *Store) ListPending(ctx context.Context) ([]*PendingAssignment, error) {
	rows, err := s.pool.Query(ctx, `SELECT order_id, restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng FROM pending_assignments ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending assignments: %w", err)
	}
	defer rows.Close()
	var out []*PendingAssignment
	for rows.Next() {
		p := &PendingAssignment{}
		if err := rows.Scan(&p.OrderID, &p.RestaurantID, &p.PickupLat, &p.PickupLng, &p.DropoffLat, &p.DropoffLng); err != nil {
			return nil, fmt.Errorf("scan pending assignment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
