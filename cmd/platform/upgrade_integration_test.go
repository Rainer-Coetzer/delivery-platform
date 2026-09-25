//go:build integration

package main

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/db/dbtest"
	"github.com/rainercoetzer/delivery-platform/services/dispatch"
	"github.com/rainercoetzer/delivery-platform/services/identity"
	"github.com/rainercoetzer/delivery-platform/services/jobs"
	"github.com/rainercoetzer/delivery-platform/services/merchant"
	"github.com/rainercoetzer/delivery-platform/services/notification"
	"github.com/rainercoetzer/delivery-platform/services/payment"
	"github.com/rainercoetzer/delivery-platform/services/reporting"
)

// An install from before the Phase C rename (schemas customer, restaurant,
// orders, delivery, admin; tables orders, drivers, ...) must come up on the
// new binary with its data intact. Each case migrates a module to its last
// pre-rename version under its old schema name, writes old-shaped rows,
// boots the new migrations, and reads the rows back under the new names.
func TestUpgradeFromPreRenameSchemas(t *testing.T) {
	cases := []struct {
		def       app.Definition
		oldSchema string
		before    int // first migration version that is part of the rename
		seed      string
		check     string // must return exactly the string want
		want      string
	}{
		{
			def: identity.Definition, oldSchema: "customer", before: 2,
			seed: `INSERT INTO users (id, name, email, password_hash, role, ref_id) VALUES
			         ('u1', 'D', 'd@example.com', 'x', 'driver', 'a1'), ('u2', 'R', 'r@example.com', 'x', 'restaurant', 'm1');
			       INSERT INTO order_history (order_id, customer_id, restaurant_name, status, total_cents, created_at)
			         VALUES ('j1', 'c1', 'Kapana', 'DELIVERED', 900, now());`,
			check: `SELECT string_agg(role, ',' ORDER BY id) || '|' || (SELECT merchant_name FROM job_history WHERE job_id = 'j1') FROM users`,
			want:  "agent,merchant|Kapana",
		},
		{
			def: merchant.Definition, oldSchema: "restaurant", before: 4,
			seed: `INSERT INTO restaurants (id, name, lat, lng) VALUES ('m1', 'Kapana', -22.5, 17.0);
			       INSERT INTO menu_items (id, restaurant_id, name, price_cents, stock_qty) VALUES ('i1', 'm1', 'Platter', 6500, 3);
			       INSERT INTO restaurant_orders (order_id, restaurant_id, items, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
			         VALUES ('j1', 'm1', '[{"menuItemId":"i1","name":"Platter","quantity":2,"unitPriceCents":6500}]', 0, 0, 0, 0);`,
			check: `SELECT (SELECT merchant_id FROM catalogue_items WHERE id = 'i1') || '|' || (items -> 0 ->> 'catalogueItemId') || '|' || ((items -> 0) ? 'menuItemId')::text
			        FROM merchant_jobs WHERE job_id = 'j1' AND merchant_id = 'm1'`,
			want: "m1|i1|false",
		},
		{
			def: jobs.Definition, oldSchema: "orders", before: 2,
			// An order already on the road: its pickup is done, its dropoff isn't.
			seed: `INSERT INTO orders (id, customer_id, customer_name, restaurant_id, restaurant_name, status, paid, restaurant_accepted, subtotal_cents,
			         delivery_fee_cents, total_cents, payment_method, pickup_lat, pickup_lng, pickup_label, dropoff_lat, dropoff_lng, driver_id)
			         VALUES ('j1', 'c1', 'C', 'm1', 'Kapana', 'OUT_FOR_DELIVERY', true, true, 100, 10, 110, 'card', -22.5, 17.0, 'Kapana', -22.6, 17.1, 'a1');
			       INSERT INTO order_items (order_id, menu_item_id, name, quantity, unit_price_cents) VALUES ('j1', 'i1', 'Platter', 1, 100);
			       INSERT INTO order_status_history (order_id, from_status, to_status) VALUES ('j1', 'CREATED', 'CONFIRMED');`,
			// The second job line proves the renamed sequence still feeds the id default.
			check: `INSERT INTO job_lines (job_id, catalogue_item_id, name, quantity, unit_price_cents) VALUES ('j1', 'i2', 'Chips', 1, 10);
			        SELECT merchant_id || '|' || agent_id || '|' || pack || '|' || milestone || '|' || (facts ->> 'paid') || (facts ->> 'merchant_accepted') || '|' ||
			               (SELECT string_agg(catalogue_item_id, ',' ORDER BY id) FROM job_lines WHERE job_id = 'j1') || '|' ||
			               (SELECT to_status FROM job_events WHERE job_id = 'j1') || '|' ||
			               (SELECT string_agg(seq || kind || lat || coalesce(label, '-') || coalesce(merchant_id, '-') || (completed_at IS NOT NULL), ',' ORDER BY seq)
			                FROM job_stops WHERE job_id = 'j1')
			        FROM jobs WHERE id = 'j1'`,
			want: "m1|a1|food|in_transit|truetrue|i1,i2|CONFIRMED|0pickup-22.5Kapanam1true,1dropoff-22.6--false",
		},
		{
			def: payment.Definition, oldSchema: "", before: 2,
			seed: `INSERT INTO payments (id, order_id, idempotency_key, customer_id, amount_cents, method, status) VALUES ('p1', 'j1', 'j1', 'c1', 110, 'card', 'COMPLETED');
			       INSERT INTO refunds (id, payment_id, order_id, amount_cents) VALUES ('r1', 'p1', 'j1', 110);
			       INSERT INTO outbox (event_id, event_type, order_id, partition_key, payload, created_at) VALUES ('e1', 't', 'j1', 'j1', '{}', now());`,
			check: `SELECT p.job_id || '|' || r.job_id || '|' || (SELECT job_id FROM outbox) FROM payments p JOIN refunds r ON r.payment_id = p.id`,
			want:  "j1|j1|j1",
		},
		{
			def: dispatch.Definition, oldSchema: "delivery", before: 3,
			seed: `INSERT INTO drivers (id, name, lat, lng, location) VALUES ('a1', 'D', 0, 0, ST_SetSRID(ST_MakePoint(0, 0), 4326)::geography);
			       INSERT INTO deliveries (id, order_id, driver_id, restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng)
			         VALUES ('s1', 'j1', 'a1', 'm1', 0, 0, 0, 0);
			       INSERT INTO pending_assignments (order_id, restaurant_id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng) VALUES ('j2', 'm1', 0, 0, 0, 0);
			       INSERT INTO cancelled_orders (order_id) VALUES ('j3');`,
			check: `SELECT a.job_id || '|' || a.agent_id || '|' || a.merchant_id || '|' || (SELECT job_id FROM pending_assignments) || '|' ||
			               (SELECT job_id FROM cancelled_jobs) || '|' || (SELECT name FROM agents)
			        FROM assignments a`,
			want: "j1|a1|m1|j2|j3|D",
		},
		{
			def: notification.Definition, oldSchema: "", before: 2,
			seed: `INSERT INTO notifications (id, recipient, order_id, title, body) VALUES
			         ('n1', 'restaurant:m1', 'j1', 't', 'b'), ('n2', 'driver:a1', 'j1', 't', 'b'), ('n3', 'order:j1', 'j1', 't', 'b'), ('n4', 'user:u1', 'j1', 't', 'b');`,
			check: `SELECT string_agg(recipient, ',' ORDER BY id) || '|' || min(job_id) FROM notifications`,
			want:  "merchant:m1,agent:a1,job:j1,user:u1|j1",
		},
		{
			def: reporting.Definition, oldSchema: "admin", before: 2,
			seed: `INSERT INTO orders_projection (order_id, restaurant_id, restaurant_name, customer_id, driver_id, status, total_cents, created_at, delivered_at)
			         VALUES ('j1', 'm1', 'Kapana', 'c1', 'a1', 'DELIVERED', 110, now(), '2026-01-02T03:04:05Z');`,
			check: `SELECT merchant_id || '|' || merchant_name || '|' || agent_id || '|' || to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM jobs_projection WHERE job_id = 'j1'`,
			want:  "m1|Kapana|a1|2026-01-02",
		},
	}

	for _, tc := range cases {
		t.Run(tc.def.Name, func(t *testing.T) {
			ctx := context.Background()
			_, dsn := dbtest.NewDSN(t, fstest.MapFS{})
			old := tc.oldSchema
			if old == "" {
				old = tc.def.Name // payment and notification kept their names
			}

			if err := db.MigrateSchema(ctx, dsn, old, migrationsBefore(t, tc.def.Migrations, tc.before)); err != nil {
				t.Fatalf("migrate old schema %s: %v", old, err)
			}
			exec(t, dsn, old, tc.seed)

			if err := db.MigrateSchema(ctx, dsn, tc.def.Name, tc.def.Migrations, tc.def.FormerSchemas...); err != nil {
				t.Fatalf("upgrade to %s: %v", tc.def.Name, err)
			}
			if got := queryString(t, dsn, tc.def.Name, tc.check); got != tc.want {
				t.Fatalf("after upgrade got %q, want %q", got, tc.want)
			}
			// A second boot on the upgraded install is a no-op.
			if err := db.MigrateSchema(ctx, dsn, tc.def.Name, tc.def.Migrations, tc.def.FormerSchemas...); err != nil {
				t.Fatalf("second boot: %v", err)
			}
		})
	}
}

// Both the old and the new schema present means someone ran the new binary
// and then restored an old backup beside it. Boot must refuse, not pick one.
func TestUpgradeRefusesWhenOldAndNewSchemaBothExist(t *testing.T) {
	ctx := context.Background()
	_, dsn := dbtest.NewDSN(t, fstest.MapFS{})
	if err := db.MigrateSchema(ctx, dsn, "orders", migrationsBefore(t, jobs.Definition.Migrations, 2)); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateSchema(ctx, dsn, "jobs", jobs.Definition.Migrations); err != nil { // no formers: a fresh "jobs"
		t.Fatal(err)
	}
	err := db.MigrateSchema(ctx, dsn, "jobs", jobs.Definition.Migrations, jobs.Definition.FormerSchemas...)
	if err == nil || !strings.Contains(err.Error(), "both exist") {
		t.Fatalf("want a 'both exist' refusal, got %v", err)
	}
}

// migrationsBefore is fsys with only the migrations numbered below version:
// the schema as the last pre-rename release left it.
func migrationsBefore(t *testing.T, fsys fs.FS, version int) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Fatalf("migration %s has no numeric prefix: %v", name, err)
		}
		if n >= version {
			continue
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = &fstest.MapFile{Data: data}
	}
	if len(out) == 0 {
		t.Fatalf("no migrations below version %d", version)
	}
	return out
}

func exec(t *testing.T, dsn, schema, sql string) {
	t.Helper()
	pool, err := db.OpenSchema(context.Background(), dsn, schema, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("seed %s: %v", schema, err)
	}
}

// queryString runs sql (statements before the last are executed for their
// side effects) and returns the last statement's single text value.
func queryString(t *testing.T, dsn, schema, sql string) string {
	t.Helper()
	stmts := strings.Split(strings.TrimSuffix(strings.TrimSpace(sql), ";"), ";")
	last := stmts[len(stmts)-1]
	pool, err := db.OpenSchema(context.Background(), dsn, schema, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, s := range stmts[:len(stmts)-1] {
		if _, err := pool.Exec(context.Background(), s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	var got *string
	if err := pool.QueryRow(context.Background(), last).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", last, err)
	}
	if got == nil {
		return "<null>"
	}
	return *got
}
