// Package store persists the Order aggregate to Postgres. It is a plain
// pgx repository (no code generator) kept intentionally small: every query
// this service needs, in one file, easy to read alongside domain.Order.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/services/order/internal/domain"
)

var ErrNotFound = errors.New("order not found")

type Store struct {
	pool *db.Pool
}

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// Create inserts a brand-new order and its line items inside tx.
func Create(ctx context.Context, tx pgx.Tx, o *domain.Order) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (
			id, customer_id, customer_name, restaurant_id, restaurant_name, status,
			paid, restaurant_accepted, subtotal_cents, delivery_fee_cents, surge_multiplier,
			total_cents, payment_method, pickup_lat, pickup_lng, pickup_label,
			dropoff_lat, dropoff_lng, dropoff_label
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		o.ID, o.CustomerID, o.CustomerName, o.RestaurantID, o.RestaurantName, o.Status,
		o.Paid, o.RestaurantAccepted, o.SubtotalCents, o.DeliveryFeeCents, o.SurgeMultiplier,
		o.TotalCents, o.PaymentMethod, o.Pickup.Lat, o.Pickup.Lng, nullStr(o.Pickup.Label),
		o.Dropoff.Lat, o.Dropoff.Lng, nullStr(o.Dropoff.Label))
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	for _, item := range o.Items {
		_, err := tx.Exec(ctx, `
			INSERT INTO order_items (order_id, menu_item_id, name, quantity, unit_price_cents)
			VALUES ($1,$2,$3,$4,$5)`,
			o.ID, item.MenuItemID, item.Name, item.Quantity, item.UnitPriceCents)
		if err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}
	return recordHistory(ctx, tx, o.ID, "", o.Status, "")
}

func recordHistory(ctx context.Context, tx pgx.Tx, orderID, from, to, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO order_status_history (order_id, from_status, to_status, reason)
		VALUES ($1,$2,$3,$4)`, orderID, from, to, nullStr(reason))
	if err != nil {
		return fmt.Errorf("insert order status history: %w", err)
	}
	return nil
}

// GetForUpdate loads an order and locks its row for the duration of tx, so
// two consumers handling events for the same order (e.g. payments.completed
// and restaurant.order-accepted arriving close together) serialize instead
// of racing on MaybeConfirm.
func GetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*domain.Order, error) {
	return scanOrder(tx.QueryRow(ctx, baseSelect+` WHERE id = $1 FOR UPDATE`, id))
}

func (s *Store) Get(ctx context.Context, id string) (*domain.Order, error) {
	o, err := scanOrder(s.pool.QueryRow(ctx, baseSelect+` WHERE id = $1`, id))
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

func (s *Store) ListByCustomer(ctx context.Context, customerID string, limit int) ([]*domain.Order, error) {
	return s.list(ctx, baseSelect+` WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2`, customerID, limit)
}

func (s *Store) ListByRestaurant(ctx context.Context, restaurantID string, limit int) ([]*domain.Order, error) {
	return s.list(ctx, baseSelect+` WHERE restaurant_id = $1 ORDER BY created_at DESC LIMIT $2`, restaurantID, limit)
}

func (s *Store) list(ctx context.Context, query string, args ...any) ([]*domain.Order, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrderRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) items(ctx context.Context, q queryer, orderID string) ([]domain.Item, error) {
	rows, err := q.Query(ctx, `SELECT menu_item_id, name, quantity, unit_price_cents FROM order_items WHERE order_id = $1`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order items: %w", err)
	}
	defer rows.Close()
	var out []domain.Item
	for rows.Next() {
		var it domain.Item
		if err := rows.Scan(&it.MenuItemID, &it.Name, &it.Quantity, &it.UnitPriceCents); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ApplyTransition persists a new status (and paid/accepted/driver flags) and
// appends a history row, inside tx.
func ApplyTransition(ctx context.Context, tx pgx.Tx, o *domain.Order, from string) error {
	_, err := tx.Exec(ctx, `
		UPDATE orders SET status=$2, paid=$3, restaurant_accepted=$4, driver_id=$5,
			cancel_reason=$6, updated_at=now()
		WHERE id=$1`,
		o.ID, o.Status, o.Paid, o.RestaurantAccepted, nullStr(o.DriverID), nullStr(o.CancelReason))
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	return recordHistory(ctx, tx, o.ID, from, o.Status, o.CancelReason)
}

// UpdateFlags persists paid/accepted/driver changes without a status
// transition (e.g. recording payment before the restaurant has responded).
func UpdateFlags(ctx context.Context, tx pgx.Tx, o *domain.Order) error {
	_, err := tx.Exec(ctx, `UPDATE orders SET paid=$2, restaurant_accepted=$3, driver_id=$4, updated_at=now() WHERE id=$1`,
		o.ID, o.Paid, o.RestaurantAccepted, nullStr(o.DriverID))
	if err != nil {
		return fmt.Errorf("update order flags: %w", err)
	}
	return nil
}

// CountOpen returns how many orders for restaurantID are not yet DELIVERED
// or CANCELLED — used for the surge multiplier.
func (s *Store) CountOpen(ctx context.Context, restaurantID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE restaurant_id=$1 AND status NOT IN ('DELIVERED','CANCELLED')`,
		restaurantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count open orders: %w", err)
	}
	return n, nil
}

const baseSelect = `SELECT id, customer_id, customer_name, restaurant_id, restaurant_name, status,
	paid, restaurant_accepted, subtotal_cents, delivery_fee_cents, surge_multiplier, total_cents,
	payment_method, pickup_lat, pickup_lng, coalesce(pickup_label,''), dropoff_lat, dropoff_lng,
	coalesce(dropoff_label,''), coalesce(driver_id,''), coalesce(cancel_reason,''), created_at, updated_at
	FROM orders`

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (*domain.Order, error) {
	o := &domain.Order{}
	err := row.Scan(&o.ID, &o.CustomerID, &o.CustomerName, &o.RestaurantID, &o.RestaurantName, &o.Status,
		&o.Paid, &o.RestaurantAccepted, &o.SubtotalCents, &o.DeliveryFeeCents, &o.SurgeMultiplier, &o.TotalCents,
		&o.PaymentMethod, &o.Pickup.Lat, &o.Pickup.Lng, &o.Pickup.Label, &o.Dropoff.Lat, &o.Dropoff.Lng,
		&o.Dropoff.Label, &o.DriverID, &o.CancelReason, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan order: %w", err)
	}
	return o, nil
}

func scanOrderRow(rows pgx.Rows) (*domain.Order, error) { return scanOrder(rows) }

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
