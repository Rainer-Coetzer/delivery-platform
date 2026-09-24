// Package store persists restaurants, menu items and the local order cache
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
	"github.com/rainercoetzer/delivery-platform/services/restaurant/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateRestaurant(ctx context.Context, r *domain.Restaurant) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO restaurants (id, name, lat, lng, opens_at, closes_at, is_open)
		VALUES ($1,$2,$3,$4, make_interval(mins => $5), make_interval(mins => $6), $7)`,
		r.ID, r.Name, r.Lat, r.Lng, r.OpensAt.Minutes(), r.ClosesAt.Minutes(), r.IsOpen)
	if err != nil {
		return fmt.Errorf("insert restaurant: %w", err)
	}
	return nil
}

func (s *Store) ListRestaurants(ctx context.Context) ([]*domain.Restaurant, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, is_open FROM restaurants ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list restaurants: %w", err)
	}
	defer rows.Close()
	var out []*domain.Restaurant
	for rows.Next() {
		r, err := scanRestaurant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRestaurant(ctx context.Context, id string) (*domain.Restaurant, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, name, lat, lng, extract(epoch from opens_at)/60, extract(epoch from closes_at)/60, is_open FROM restaurants WHERE id=$1`, id)
	return scanRestaurant(row)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanRestaurant(row rowScanner) (*domain.Restaurant, error) {
	r := &domain.Restaurant{}
	var opensMin, closesMin float64
	err := row.Scan(&r.ID, &r.Name, &r.Lat, &r.Lng, &opensMin, &closesMin, &r.IsOpen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan restaurant: %w", err)
	}
	r.OpensAt = minutesToDuration(opensMin)
	r.ClosesAt = minutesToDuration(closesMin)
	return r, nil
}

// minutesToDuration converts minutes-since-midnight (as stored in Postgres
// via extract(epoch from ...)/60) into the time.Duration domain.Restaurant
// uses for the same value.
func minutesToDuration(m float64) time.Duration {
	return time.Duration(m) * time.Minute
}

// --- Menu items ---

func (s *Store) CreateMenuItem(ctx context.Context, item *domain.MenuItem) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO menu_items (id, restaurant_id, name, price_cents, stock_qty, available)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		item.ID, item.RestaurantID, item.Name, item.PriceCents, item.StockQty, item.Available)
	if err != nil {
		return fmt.Errorf("insert menu item: %w", err)
	}
	return nil
}

func (s *Store) ListMenuItems(ctx context.Context, restaurantID string) ([]*domain.MenuItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, restaurant_id, name, price_cents, stock_qty, available FROM menu_items WHERE restaurant_id=$1 ORDER BY name`,
		restaurantID)
	if err != nil {
		return nil, fmt.Errorf("list menu items: %w", err)
	}
	defer rows.Close()
	var out []*domain.MenuItem
	for rows.Next() {
		it := &domain.MenuItem{}
		if err := rows.Scan(&it.ID, &it.RestaurantID, &it.Name, &it.PriceCents, &it.StockQty, &it.Available); err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMenuItem(ctx context.Context, item *domain.MenuItem) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE menu_items SET name=$2, price_cents=$3, stock_qty=$4, available=$5 WHERE id=$1 AND restaurant_id=$6`,
		item.ID, item.Name, item.PriceCents, item.StockQty, item.Available, item.RestaurantID)
	if err != nil {
		return fmt.Errorf("update menu item: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CatalogueFor loads the requested menu items (by id) for a stock check, as
// a map keyed by menu item id.
func CatalogueFor(ctx context.Context, tx pgx.Tx, ids []string) (map[string]domain.MenuItem, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, restaurant_id, name, price_cents, stock_qty, available FROM menu_items WHERE id = ANY($1) FOR UPDATE`,
		ids)
	if err != nil {
		return nil, fmt.Errorf("load catalogue: %w", err)
	}
	defer rows.Close()
	out := map[string]domain.MenuItem{}
	for rows.Next() {
		var it domain.MenuItem
		if err := rows.Scan(&it.ID, &it.RestaurantID, &it.Name, &it.PriceCents, &it.StockQty, &it.Available); err != nil {
			return nil, fmt.Errorf("scan catalogue item: %w", err)
		}
		out[it.ID] = it
	}
	return out, rows.Err()
}

// DecrementStock reduces stock_qty for each requested item inside tx.
func DecrementStock(ctx context.Context, tx pgx.Tx, requested []domain.RequestedItem) error {
	for _, req := range requested {
		if _, err := tx.Exec(ctx, `UPDATE menu_items SET stock_qty = stock_qty - $2 WHERE id = $1`, req.MenuItemID, req.Quantity); err != nil {
			return fmt.Errorf("decrement stock for %s: %w", req.MenuItemID, err)
		}
	}
	return nil
}

// RestoreStock adds quantities back, e.g. after a cancellation.
func RestoreStock(ctx context.Context, tx pgx.Tx, requested []domain.RequestedItem) error {
	for _, req := range requested {
		if _, err := tx.Exec(ctx, `UPDATE menu_items SET stock_qty = stock_qty + $2 WHERE id = $1`, req.MenuItemID, req.Quantity); err != nil {
			return fmt.Errorf("restore stock for %s: %w", req.MenuItemID, err)
		}
	}
	return nil
}

// --- Restaurant order cache ---

type RestaurantOrder struct {
	OrderID      string
	RestaurantID string
	Status       string
	Items        []events.OrderItem
	Pickup       events.Location
	Dropoff      events.Location
	RejectReason string
}

func InsertRestaurantOrder(ctx context.Context, tx pgx.Tx, o *RestaurantOrder) error {
	items, err := json.Marshal(o.Items)
	if err != nil {
		return fmt.Errorf("marshal items: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO restaurant_orders (order_id, restaurant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		o.OrderID, o.RestaurantID, o.Status, items, o.Pickup.Lat, o.Pickup.Lng, o.Dropoff.Lat, o.Dropoff.Lng)
	if err != nil {
		return fmt.Errorf("insert restaurant order: %w", err)
	}
	return nil
}

func GetRestaurantOrderForUpdate(ctx context.Context, tx pgx.Tx, orderID string) (*RestaurantOrder, error) {
	row := tx.QueryRow(ctx, `
		SELECT order_id, restaurant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, coalesce(reject_reason,'')
		FROM restaurant_orders WHERE order_id=$1 FOR UPDATE`, orderID)
	return scanRestaurantOrder(row)
}

func (s *Store) GetRestaurantOrder(ctx context.Context, orderID string) (*RestaurantOrder, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT order_id, restaurant_id, status, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng, coalesce(reject_reason,'')
		FROM restaurant_orders WHERE order_id=$1`, orderID)
	return scanRestaurantOrder(row)
}

func scanRestaurantOrder(row rowScanner) (*RestaurantOrder, error) {
	o := &RestaurantOrder{}
	var itemsRaw []byte
	err := row.Scan(&o.OrderID, &o.RestaurantID, &o.Status, &itemsRaw, &o.Pickup.Lat, &o.Pickup.Lng, &o.Dropoff.Lat, &o.Dropoff.Lng, &o.RejectReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan restaurant order: %w", err)
	}
	if err := json.Unmarshal(itemsRaw, &o.Items); err != nil {
		return nil, fmt.Errorf("unmarshal restaurant order items: %w", err)
	}
	return o, nil
}

func UpdateRestaurantOrderStatus(ctx context.Context, tx pgx.Tx, orderID, status, rejectReason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE restaurant_orders SET status=$2, reject_reason=$3, updated_at=now() WHERE order_id=$1`,
		orderID, status, nullStr(rejectReason))
	if err != nil {
		return fmt.Errorf("update restaurant order status: %w", err)
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Kitchen statuses for restaurant_orders. PREPARING is set when the Order
// service confirms the order (see consumers.handleStatusChanged).
const (
	StatusPending   = "PENDING"
	StatusAccepted  = "ACCEPTED"
	StatusRejected  = "REJECTED"
	StatusPreparing = "PREPARING"
	StatusReady     = "READY"
	StatusCancelled = "CANCELLED"
)

// MarkPreparing moves an ACCEPTED order to PREPARING. Any other current
// status is left alone (e.g. already cancelled), and reported as false.
func MarkPreparing(ctx context.Context, tx pgx.Tx, orderID string, since time.Time) (bool, error) {
	ct, err := tx.Exec(ctx, `
		UPDATE restaurant_orders SET status = 'PREPARING', preparing_since = $2, updated_at = now()
		WHERE order_id = $1 AND status = 'ACCEPTED'`, orderID, since)
	if err != nil {
		return false, fmt.Errorf("mark order %s preparing: %w", orderID, err)
	}
	return ct.RowsAffected() == 1, nil
}

type PreparingOrder struct {
	OrderID        string
	PreparingSince time.Time
}

func (s *Store) ListPreparing(ctx context.Context) ([]PreparingOrder, error) {
	rows, err := s.pool.Query(ctx, `SELECT order_id, preparing_since FROM restaurant_orders WHERE status = 'PREPARING' ORDER BY preparing_since`)
	if err != nil {
		return nil, fmt.Errorf("list preparing orders: %w", err)
	}
	defer rows.Close()
	var out []PreparingOrder
	for rows.Next() {
		var p PreparingOrder
		if err := rows.Scan(&p.OrderID, &p.PreparingSince); err != nil {
			return nil, fmt.Errorf("scan preparing order: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
