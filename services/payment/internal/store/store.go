// Package store persists payments and refunds to Postgres.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

var ErrNotFound = errors.New("payment not found")

type Payment struct {
	ID             string `json:"id"`
	OrderID        string `json:"orderId"`
	IdempotencyKey string `json:"-"`
	CustomerID     string `json:"customerId"`
	AmountCents    int64  `json:"amountCents"`
	Method         string `json:"method"`
	Status         string `json:"status"`
	FailureReason  string `json:"failureReason,omitempty"`
}

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// InsertIfAbsent inserts p unless a payment with the same idempotency key
// already exists, in which case it returns the existing row and existed=true.
// This is the guard against charging the same order twice.
func InsertIfAbsent(ctx context.Context, tx pgx.Tx, p *Payment) (existed bool, err error) {
	_, err = tx.Exec(ctx, `
		INSERT INTO payments (id, order_id, idempotency_key, customer_id, amount_cents, method, status, failure_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		p.ID, p.OrderID, p.IdempotencyKey, p.CustomerID, p.AmountCents, p.Method, p.Status, nullStr(p.FailureReason))
	if err != nil {
		return false, fmt.Errorf("insert payment: %w", err)
	}
	existing, err := GetByOrderForUpdate(ctx, tx, p.OrderID)
	if err != nil {
		return false, err
	}
	if existing.ID != p.ID {
		*p = *existing
		return true, nil
	}
	return false, nil
}

func GetByOrderForUpdate(ctx context.Context, tx pgx.Tx, orderID string) (*Payment, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, order_id, idempotency_key, customer_id, amount_cents, method, status, coalesce(failure_reason,'')
		FROM payments WHERE order_id=$1 FOR UPDATE`, orderID)
	return scan(row)
}

func (s *Store) GetByOrder(ctx context.Context, orderID string) (*Payment, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, order_id, idempotency_key, customer_id, amount_cents, method, status, coalesce(failure_reason,'')
		FROM payments WHERE order_id=$1`, orderID)
	return scan(row)
}

func scan(row interface{ Scan(dest ...any) error }) (*Payment, error) {
	p := &Payment{}
	err := row.Scan(&p.ID, &p.OrderID, &p.IdempotencyKey, &p.CustomerID, &p.AmountCents, &p.Method, &p.Status, &p.FailureReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan payment: %w", err)
	}
	return p, nil
}

func MarkRefunded(ctx context.Context, tx pgx.Tx, paymentID string) error {
	_, err := tx.Exec(ctx, `UPDATE payments SET status='REFUNDED' WHERE id=$1`, paymentID)
	if err != nil {
		return fmt.Errorf("mark refunded: %w", err)
	}
	return nil
}

func InsertRefund(ctx context.Context, tx pgx.Tx, id, paymentID, orderID string, amountCents int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO refunds (id, payment_id, order_id, amount_cents) VALUES ($1,$2,$3,$4)`,
		id, paymentID, orderID, amountCents)
	if err != nil {
		return fmt.Errorf("insert refund: %w", err)
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
