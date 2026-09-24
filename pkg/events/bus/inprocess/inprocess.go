// Package inprocess is the default events bus (ADR-0002): a partitioned log
// in Postgres, with a dispatcher per (consumer group, partition). It needs
// nothing beyond the database the platform already has.
//
// Shape, deliberately a small Kafka look-alike:
//
//	Publish:    lock partition_heads[p] -> take next_seq -> insert log row -> NOTIFY
//	Dispatcher: read log[p] after offsets[group,p] in seq order -> handle ->
//	            advance offset (or dead-letter, then advance)
//
// A dispatcher owns its (group, partition) through a Postgres advisory lock,
// so a second platform replica can't double-deliver; it simply idles until
// the owner goes away.
package inprocess

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rainercoetzer/delivery-platform/pkg/db"
	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/events/bus"
	"github.com/rainercoetzer/delivery-platform/pkg/obs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Schema holds the bus tables, next to the module schemas.
const Schema = "bus"

const notifyChannel = "bus_log"

type Config struct {
	DSN string
	// Partitions per topic space. Same key -> same partition -> ordered.
	// Changing it on a live system reorders keys, so it's fixed per
	// deployment. 3 matches the Kafka topics.
	Partitions int
	Retry      bus.RetryPolicy
	// PollInterval is the fallback when a NOTIFY is missed (a reconnect,
	// say). Wakeups normally come from NOTIFY within milliseconds.
	PollInterval time.Duration
	BatchSize    int
	Log          *slog.Logger
}

type Bus struct {
	cfg  Config
	pool *db.Pool

	// lockConn holds the session advisory locks that give a dispatcher
	// ownership of its (group, partition). pgx.Conn isn't safe for
	// concurrent use, hence the mutex.
	lockMu   sync.Mutex
	lockConn *pgx.Conn

	wakeMu  sync.Mutex
	wakers  map[int][]chan struct{} // partition -> dispatcher wake channels
	cancel  context.CancelFunc
	stopped chan struct{}
}

func New(ctx context.Context, cfg Config) (*Bus, error) {
	if cfg.Partitions <= 0 {
		cfg.Partitions = 3
	}
	if cfg.Retry.Attempts == 0 {
		cfg.Retry = bus.DefaultRetry
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if err := db.MigrateSchema(ctx, cfg.DSN, Schema, mustSub(migrationsFS, "migrations")); err != nil {
		return nil, fmt.Errorf("bus migrations: %w", err)
	}
	pool, err := db.OpenSchema(ctx, cfg.DSN, Schema, 10)
	if err != nil {
		return nil, fmt.Errorf("bus pool: %w", err)
	}
	lockConn, err := pgx.Connect(ctx, cfg.DSN)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("bus lock connection: %w", err)
	}
	lctx, cancel := context.WithCancel(context.Background())
	b := &Bus{cfg: cfg, pool: pool, lockConn: lockConn, wakers: map[int][]chan struct{}{}, cancel: cancel, stopped: make(chan struct{})}
	go b.listen(lctx)
	return b, nil
}

// Publish implements bus.Bus.
func (b *Bus) Publish(ctx context.Context, topic, key string, value []byte) error {
	p := b.partitionFor(key)
	err := b.pool.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO partition_heads (partition, next_seq) VALUES ($1, 1) ON CONFLICT DO NOTHING`, p); err != nil {
			return err
		}
		var seq int64
		// FOR UPDATE holds the partition until commit: see the migration
		// for why sequence order must equal commit order.
		if err := tx.QueryRow(ctx, `SELECT next_seq FROM partition_heads WHERE partition = $1 FOR UPDATE`, p).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO log (partition, seq, topic, key, payload) VALUES ($1, $2, $3, $4, $5)`, p, seq, topic, key, value); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE partition_heads SET next_seq = next_seq + 1 WHERE partition = $1`, p); err != nil {
			return err
		}
		// Delivered on commit, so dispatchers never wake for a row they
		// can't see yet.
		_, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, strconv.Itoa(p))
		return err
	})
	if err != nil {
		return fmt.Errorf("publish %s (key %s): %w", topic, key, err)
	}
	return nil
}

func (b *Bus) partitionFor(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(b.cfg.Partitions))
}

// Subscribe implements bus.Bus: one dispatcher goroutine per partition.
func (b *Bus) Subscribe(ctx context.Context, group string, topics []string, h bus.Handler) (func(), error) {
	if group == "" || len(topics) == 0 || h == nil {
		return nil, fmt.Errorf("subscribe: group, topics and handler are required")
	}
	want := make(map[string]bool, len(topics))
	for _, t := range topics {
		want[t] = true
	}
	sctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for p := 0; p < b.cfg.Partitions; p++ {
		d := &dispatcher{bus: b, group: group, partition: p, topics: want, handle: h, wake: make(chan struct{}, 1)}
		b.addWaker(p, d.wake)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer b.removeWaker(d.partition, d.wake)
			d.run(sctx)
		}()
	}
	return func() { cancel(); wg.Wait() }, nil
}

// Close stops the NOTIFY listener and releases connections.
func (b *Bus) Close() {
	b.cancel()
	<-b.stopped
	b.lockMu.Lock()
	_ = b.lockConn.Close(context.Background())
	b.lockMu.Unlock()
	b.pool.Close()
}

// DeadLetter is a dead-lettered event, for tests and the future replay tool.
type DeadLetter struct {
	Group, Topic, Key, Reason string
}

func (b *Bus) DeadLetters(ctx context.Context) ([]DeadLetter, error) {
	rows, err := b.pool.Query(ctx, `SELECT grp, topic, key, reason FROM dead_letters ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list dead letters: %w", err)
	}
	defer rows.Close()
	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		if err := rows.Scan(&d.Group, &d.Topic, &d.Key, &d.Reason); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- wakeups ---

func (b *Bus) addWaker(p int, ch chan struct{}) {
	b.wakeMu.Lock()
	defer b.wakeMu.Unlock()
	b.wakers[p] = append(b.wakers[p], ch)
}

func (b *Bus) removeWaker(p int, ch chan struct{}) {
	b.wakeMu.Lock()
	defer b.wakeMu.Unlock()
	list := b.wakers[p]
	for i, c := range list {
		if c == ch {
			b.wakers[p] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

func (b *Bus) wake(p int) {
	b.wakeMu.Lock()
	defer b.wakeMu.Unlock()
	for _, ch := range b.wakers[p] {
		select {
		case ch <- struct{}{}:
		default: // already has a pending wakeup
		}
	}
}

// listen turns NOTIFYs into dispatcher wakeups. It reconnects on failure;
// while it's down, dispatchers fall back to PollInterval, so a lost
// listener only adds latency, never loses events.
func (b *Bus) listen(ctx context.Context) {
	defer close(b.stopped)
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, b.cfg.DSN)
		if err == nil {
			_, err = conn.Exec(ctx, "LISTEN "+notifyChannel)
		}
		for err == nil {
			var n *pgconn.Notification
			n, err = conn.WaitForNotification(ctx)
			if err == nil {
				if p, perr := strconv.Atoi(n.Payload); perr == nil {
					b.wake(p)
				}
			}
		}
		if conn != nil {
			_ = conn.Close(context.Background())
		}
		if ctx.Err() != nil {
			return
		}
		b.cfg.Log.Warn("bus listener lost its connection, reconnecting (dispatchers keep polling meanwhile)", "error", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// --- dispatcher ---

type dispatcher struct {
	bus       *Bus
	group     string
	partition int
	topics    map[string]bool
	handle    bus.Handler
	wake      chan struct{}
}

func (d *dispatcher) lockKey() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("bus:" + d.group + ":" + strconv.Itoa(d.partition)))
	return int64(h.Sum64() >> 1)
}

func (d *dispatcher) run(ctx context.Context) {
	log := d.bus.cfg.Log.With("group", d.group, "partition", d.partition)
	for !d.acquire(ctx) {
		select {
		case <-time.After(5 * d.bus.cfg.PollInterval):
		case <-ctx.Done():
			return
		}
	}
	defer d.release()

	ticker := time.NewTicker(d.bus.cfg.PollInterval)
	defer ticker.Stop()
	for {
		n, err := d.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Error("bus dispatcher error, retrying on next wakeup", "error", err)
		}
		if n == d.bus.cfg.BatchSize {
			continue // more waiting; don't sleep
		}
		select {
		case <-d.wake:
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (d *dispatcher) acquire(ctx context.Context) bool {
	d.bus.lockMu.Lock()
	defer d.bus.lockMu.Unlock()
	var ok bool
	if err := d.bus.lockConn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, d.lockKey()).Scan(&ok); err != nil {
		d.bus.cfg.Log.Error("bus: acquire dispatcher lock", "group", d.group, "partition", d.partition, "error", err)
		return false
	}
	return ok
}

func (d *dispatcher) release() {
	d.bus.lockMu.Lock()
	defer d.bus.lockMu.Unlock()
	_, _ = d.bus.lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, d.lockKey())
}

type logRow struct {
	seq     int64
	topic   string
	key     string
	payload []byte
}

// drain handles one batch after the group's offset. It returns how many
// rows it read, so run knows whether to loop straight away.
func (d *dispatcher) drain(ctx context.Context) (int, error) {
	pool := d.bus.pool
	if _, err := pool.Exec(ctx, `INSERT INTO offsets (grp, partition) VALUES ($1, $2) ON CONFLICT DO NOTHING`, d.group, d.partition); err != nil {
		return 0, err
	}
	var last int64
	if err := pool.QueryRow(ctx, `SELECT last_seq FROM offsets WHERE grp = $1 AND partition = $2`, d.group, d.partition).Scan(&last); err != nil {
		return 0, err
	}
	rows, err := pool.Query(ctx,
		`SELECT seq, topic, key, payload FROM log WHERE partition = $1 AND seq > $2 ORDER BY seq LIMIT $3`,
		d.partition, last, d.bus.cfg.BatchSize)
	if err != nil {
		return 0, err
	}
	batch, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (logRow, error) {
		var lr logRow
		err := r.Scan(&lr.seq, &lr.topic, &lr.key, &lr.payload)
		return lr, err
	})
	if err != nil {
		return 0, err
	}

	// Rows on topics this group doesn't consume are skipped, but the offset
	// still moves past them; we commit that lazily, together with the next
	// handled row or at the end of the batch.
	pending := last
	for _, r := range batch {
		if d.topics[r.topic] {
			if err := d.deliver(ctx, r); err != nil {
				// Stopping mid-event: commit what was finished before it and
				// leave this one to be redelivered (at-least-once).
				_ = d.commit(context.Background(), pending)
				return len(batch), err
			}
		}
		pending = r.seq
		if d.topics[r.topic] {
			if err := d.commit(ctx, pending); err != nil {
				return len(batch), err
			}
		}
	}
	if pending != last {
		if err := d.commit(ctx, pending); err != nil {
			return len(batch), err
		}
	}
	return len(batch), nil
}

// deliver runs the handler with retries. A dead-lettered event counts as
// handled. Only a cancelled context returns an error, meaning "don't commit".
func (d *dispatcher) deliver(ctx context.Context, r logRow) error {
	var env events.Envelope
	if err := json.Unmarshal(r.payload, &env); err != nil {
		return d.deadLetter(ctx, r, "undecodable envelope: "+err.Error())
	}
	err, dead := bus.Deliver(ctx, d.bus.cfg.Retry, d.handle, env)
	switch {
	case err == nil:
		obs.KafkaEventsConsumedTotal.WithLabelValues(d.group, r.topic, "success").Inc()
		return nil
	case dead:
		d.bus.cfg.Log.Error("handler failed, dead-lettering", "group", d.group, "topic", r.topic, "eventId", env.EventID, "error", err)
		obs.KafkaEventsConsumedTotal.WithLabelValues(d.group, r.topic, "dead_lettered").Inc()
		return d.deadLetter(ctx, r, err.Error())
	default:
		return err // context cancelled
	}
}

func (d *dispatcher) deadLetter(ctx context.Context, r logRow, reason string) error {
	_, err := d.bus.pool.Exec(ctx,
		`INSERT INTO dead_letters (grp, topic, key, partition, seq, payload, reason) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		d.group, r.topic, r.key, d.partition, r.seq, r.payload, reason)
	if err != nil {
		// Not dead-lettered means not handled: returning the error stops the
		// batch without committing, so the event is retried rather than lost.
		return fmt.Errorf("dead-letter %s seq %d: %w", r.topic, r.seq, err)
	}
	return nil
}

func (d *dispatcher) commit(ctx context.Context, seq int64) error {
	_, err := d.bus.pool.Exec(ctx, `UPDATE offsets SET last_seq = $3 WHERE grp = $1 AND partition = $2 AND last_seq < $3`, d.group, d.partition, seq)
	if err != nil {
		return fmt.Errorf("commit offset %d: %w", seq, err)
	}
	return nil
}

// mustSub roots the embedded migrations at their directory. A failure here
// means the embed pattern and the directory name disagree: a build-time
// mistake, so panicking at startup is the right amount of loud.
func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("inprocess bus: embedded migrations dir %q: %v", dir, err))
	}
	return sub
}
