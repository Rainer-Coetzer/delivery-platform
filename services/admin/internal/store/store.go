// Package store maintains the orders_projection read model and serves the
// aggregate queries behind every admin report.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

// --- Projection writers (called from consumers) ---

func UpsertCreated(ctx context.Context, q db.Queryer, orderID, restaurantID, restaurantName, customerID string, totalCents int64, createdAt time.Time) error {
	_, err := q.Exec(ctx, `
		INSERT INTO orders_projection (order_id, restaurant_id, restaurant_name, customer_id, status, total_cents, created_at)
		VALUES ($1,$2,$3,$4,'CREATED',$5,$6)
		ON CONFLICT (order_id) DO NOTHING`,
		orderID, restaurantID, restaurantName, customerID, totalCents, createdAt)
	if err != nil {
		return fmt.Errorf("upsert order projection: %w", err)
	}
	return nil
}

func UpdateStatus(ctx context.Context, q db.Queryer, orderID, status string, deliveredAt *time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE orders_projection SET status=$2, delivered_at=coalesce($3, delivered_at), updated_at=now() WHERE order_id=$1`,
		orderID, status, deliveredAt)
	if err != nil {
		return fmt.Errorf("update order projection status: %w", err)
	}
	return nil
}

func SetDriver(ctx context.Context, q db.Queryer, orderID, driverID string) error {
	_, err := q.Exec(ctx, `UPDATE orders_projection SET driver_id=$2, updated_at=now() WHERE order_id=$1`, orderID, driverID)
	if err != nil {
		return fmt.Errorf("set order projection driver: %w", err)
	}
	return nil
}

// --- Reports ---

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

type RestaurantStats struct {
	RestaurantID       string  `json:"restaurantId"`
	OrdersCount        int64   `json:"ordersCount"`
	RevenueCents       int64   `json:"revenueCents"`
	AvgDeliveryMinutes float64 `json:"avgDeliveryMinutes"`
}

func (s *Store) RestaurantStats(ctx context.Context, restaurantID string) (*RestaurantStats, error) {
	r := &RestaurantStats{RestaurantID: restaurantID}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(total_cents) FILTER (WHERE status != 'CANCELLED'), 0),
		       coalesce(avg(extract(epoch from delivered_at - created_at)) FILTER (WHERE delivered_at IS NOT NULL), 0) / 60
		FROM orders_projection WHERE restaurant_id=$1`,
		restaurantID).Scan(&r.OrdersCount, &r.RevenueCents, &r.AvgDeliveryMinutes)
	if err != nil {
		return nil, fmt.Errorf("restaurant stats: %w", err)
	}
	return r, nil
}

type DriverStats struct {
	DriverID           string  `json:"driverId"`
	DeliveriesCount    int64   `json:"deliveriesCount"`
	AvgDeliveryMinutes float64 `json:"avgDeliveryMinutes"`
}

func (s *Store) DriverStats(ctx context.Context, driverID string) (*DriverStats, error) {
	d := &DriverStats{DriverID: driverID}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='DELIVERED'),
		       coalesce(avg(extract(epoch from delivered_at - created_at)) FILTER (WHERE delivered_at IS NOT NULL), 0) / 60
		FROM orders_projection WHERE driver_id=$1`,
		driverID).Scan(&d.DeliveriesCount, &d.AvgDeliveryMinutes)
	if err != nil {
		return nil, fmt.Errorf("driver stats: %w", err)
	}
	return d, nil
}

type Overview struct {
	TotalOrders        int64            `json:"totalOrders"`
	TotalRevenueCents  int64            `json:"totalRevenueCents"`
	AvgDeliveryMinutes float64          `json:"avgDeliveryMinutes"`
	OrdersByStatus     map[string]int64 `json:"ordersByStatus"`
}

func (s *Store) Overview(ctx context.Context) (*Overview, error) {
	o := &Overview{OrdersByStatus: map[string]int64{}}
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(sum(total_cents) FILTER (WHERE status != 'CANCELLED'), 0),
		       coalesce(avg(extract(epoch from delivered_at - created_at)) FILTER (WHERE delivered_at IS NOT NULL), 0) / 60
		FROM orders_projection`).Scan(&o.TotalOrders, &o.TotalRevenueCents, &o.AvgDeliveryMinutes)
	if err != nil {
		return nil, fmt.Errorf("overview: %w", err)
	}

	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM orders_projection GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("orders by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status count: %w", err)
		}
		o.OrdersByStatus[status] = count
	}
	return o, rows.Err()
}
