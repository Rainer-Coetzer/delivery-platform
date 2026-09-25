// Package store persists notifications.
package store

import (
	"context"
	"fmt"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
)

type Notification struct {
	ID        string `json:"id"`
	Recipient string `json:"recipient"`
	JobID     string `json:"jobId,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Channel   string `json:"channel"`
}

func Insert(ctx context.Context, q db.Queryer, n *Notification) error {
	_, err := q.Exec(ctx, `
		INSERT INTO notifications (id, recipient, job_id, title, body, channel)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		n.ID, n.Recipient, nullStr(n.JobID), n.Title, n.Body, n.Channel)
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func (s *Store) ListFor(ctx context.Context, recipient string, limit int) ([]*Notification, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, recipient, coalesce(job_id,''), title, body, channel FROM notifications
		WHERE recipient=$1 ORDER BY created_at DESC LIMIT $2`, recipient, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		n := &Notification{}
		if err := rows.Scan(&n.ID, &n.Recipient, &n.JobID, &n.Title, &n.Body, &n.Channel); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Recipient returns the inbox a notification belongs to, for ownership checks.
func (s *Store) Recipient(ctx context.Context, id string) (string, error) {
	var r string
	if err := s.pool.QueryRow(ctx, `SELECT recipient FROM notifications WHERE id=$1`, id).Scan(&r); err != nil {
		return "", fmt.Errorf("load notification %s: %w", id, err)
	}
	return r, nil
}

func (s *Store) MarkRead(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
