package webhook

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const maxRetryDelay = 5 * time.Minute

type outbox struct {
	db               *sql.DB
	destination      [sha256.Size]byte
	wake             chan struct{}
	deliveryFailures atomic.Uint64
	retries          atomic.Uint64
}

// OpenPersistentDispatcher makes Dispatch persist events. Run delivers them independently.
func OpenPersistentDispatcher(ctx context.Context, path, endpoint, secret string, client *http.Client) (*Dispatcher, error) {
	target, err := url.Parse(endpoint)
	if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, errors.New("webhook URL must be absolute HTTP or HTTPS")
	}
	if secret == "" {
		return nil, errors.New("webhook secret is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(absolute)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(file.Chmod(0o600), file.Close()); err != nil {
		return nil, err
	}

	filePath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	pragmas := url.Values{"_pragma": {"busy_timeout(2000)", "synchronous(FULL)"}}
	dsn := (&url.URL{Scheme: "file", Path: filePath, RawQuery: pragmas.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrateOutbox(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	d := NewDispatcher(endpoint, secret, client)
	// Pending message content belongs to the configured receiver.
	httpClient := *d.client
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	d.client = &httpClient
	d.outbox = &outbox{db: db, destination: sha256.Sum256([]byte(endpoint)), wake: make(chan struct{}, 1)}
	return d, nil
}

func migrateOutbox(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS webhook_outbox (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				destination BLOB NOT NULL,
				body BLOB NOT NULL,
				next_attempt INTEGER NOT NULL,
				attempts INTEGER NOT NULL DEFAULT 0
			);
			CREATE INDEX IF NOT EXISTS webhook_outbox_due
				ON webhook_outbox (destination, next_attempt, id);
			ALTER TABLE webhook_outbox ADD COLUMN enqueued_at INTEGER;
			PRAGMA user_version = 1;
		`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (q *outbox) enqueue(ctx context.Context, body []byte) error {
	now := time.Now().UnixMilli()
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO webhook_outbox (destination, body, next_attempt, enqueued_at) VALUES (?, ?, ?, ?)
	`, q.destination[:], body, now, now)
	if err == nil {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return err
}

// Close releases the outbox after Run has returned.
func (d *Dispatcher) Close() error {
	return d.outbox.db.Close()
}

// Run delivers pending events until ctx is canceled. Call it once per persistent dispatcher.
func (d *Dispatcher) Run(ctx context.Context, log *slog.Logger) {
	// ponytail: one delivery at a time. Use a bounded worker pool with claims if throughput requires it.
	for ctx.Err() == nil {
		delay, err := d.deliverNext(ctx, log)
		if err != nil && ctx.Err() == nil {
			log.Warn("webhook outbox operation failed", "error", err)
			delay = time.Second
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
		case <-d.outbox.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (d *Dispatcher) deliverNext(ctx context.Context, log *slog.Logger) (time.Duration, error) {
	q := d.outbox
	var id, nextAttempt int64
	var attempts int
	var body []byte
	err := q.db.QueryRowContext(ctx, `
		SELECT id, body, next_attempt, attempts FROM webhook_outbox
		WHERE destination = ? ORDER BY next_attempt, id LIMIT 1
	`, q.destination[:]).Scan(&id, &body, &nextAttempt, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Minute, nil
	}
	if err != nil {
		return 0, err
	}
	if delay := time.Until(time.UnixMilli(nextAttempt)); delay > 0 {
		return delay, nil
	}

	attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if attempts > 0 {
		q.retries.Add(1)
	}
	err = d.send(attemptCtx, body)
	cancel()
	if err == nil {
		_, err = q.db.ExecContext(ctx, "DELETE FROM webhook_outbox WHERE id = ?", id)
		return 0, err
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	q.deliveryFailures.Add(1)

	delay := min(time.Second<<min(attempts, 9), maxRetryDelay)
	_, err = q.db.ExecContext(ctx, `
		UPDATE webhook_outbox SET attempts = attempts + 1, next_attempt = ? WHERE id = ?
	`, time.Now().Add(delay).UnixMilli(), id)
	if err == nil {
		log.Warn("webhook delivery failed; retry scheduled", "outbox_id", id, "attempt", attempts+1, "delay", delay)
	}
	return 0, err
}

type QueueMetrics struct {
	Pending          int64
	DeliveryFailures uint64
	Retries          uint64
	OldestAgeSeconds float64
}

// Metrics reads pending rows for the configured receiver and this process's counters.
func (d *Dispatcher) Metrics(ctx context.Context) (QueueMetrics, error) {
	var metrics QueueMetrics
	if d == nil || d.outbox == nil {
		return metrics, nil
	}
	q := d.outbox
	var oldest int64
	// ponytail: scan the receiver's index on scrape; maintain a count if very large queues need it.
	// Legacy rows use the original event timestamp without rewriting the backlog.
	err := q.db.QueryRowContext(ctx, `
		WITH pending AS (
			SELECT COUNT(*) AS total, MIN(id) AS oldest_id
			FROM webhook_outbox WHERE destination = ?
		)
		SELECT pending.total, COALESCE(w.enqueued_at,
			CASE WHEN json_valid(CAST(w.body AS TEXT))
				THEN CAST(json_extract(CAST(w.body AS TEXT), '$.timestamp') AS INTEGER) END, 0)
		FROM pending LEFT JOIN webhook_outbox w ON w.id = pending.oldest_id
	`, q.destination[:]).Scan(&metrics.Pending, &oldest)
	if err != nil {
		return QueueMetrics{}, err
	}
	if oldest > 0 {
		metrics.OldestAgeSeconds = max(0, time.Since(time.UnixMilli(oldest)).Seconds())
	}
	metrics.DeliveryFailures = q.deliveryFailures.Load()
	metrics.Retries = q.retries.Load()
	return metrics, nil
}
