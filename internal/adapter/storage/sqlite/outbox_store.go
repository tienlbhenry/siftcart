package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

func (s *Store) FetchPending(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	if limit <= 0 {
		return []domain.OutboxEvent{}, nil
	}

	var events []domain.OutboxEvent

	queryContext, err := s.db.QueryContext(ctx,
		`SELECT id, doc_id, action, version, payload_changed, status, retry_count,
	        last_error, created_at, processed_at FROM outbox_events WHERE status = ? ORDER BY id ASC LIMIT ?
	        `, string(domain.OutboxEventStatusPending), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}

	defer queryContext.Close()

	for queryContext.Next() {
		var (
			event             domain.OutboxEvent
			action, status    string
			payloadChangedInt int
			processedAt       sql.NullTime
		)
		err = queryContext.Scan(&event.ID, &event.DocID, &action, &event.Version, &payloadChangedInt,
			&status, &event.RetryCount, &event.LastError, &event.CreatedAt, &processedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
		}

		event.Action = domain.OutboxAction(action)
		event.Status = domain.OutboxStatus(status)
		event.PayloadChanged = payloadChangedInt == 1
		if processedAt.Valid {
			event.ProcessedAt = &processedAt.Time
		}

		events = append(events, event)
	}

	if err = queryContext.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending outbox events: %w", err)
	}

	return events, nil
}

func (s *Store) MarkDone(ctx context.Context, eventID int64) error {
	now := time.Now().UTC()
	execContext, err := s.db.ExecContext(ctx, `UPDATE outbox_events SET status = ?, processed_at = ? WHERE id = ?`,
		domain.OutboxEventStatusDone, now, eventID)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event %d as done: %w", eventID, err)
	}

	affected, err := execContext.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to fetch affected rows: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("failed to mark outbox event %d as done: no affected", eventID)
	}

	return nil
}

func (s *Store) MarkFailed(ctx context.Context, eventID int64, errMsg string) error {
	now := time.Now().UTC()
	execContext, err := s.db.ExecContext(ctx, `UPDATE outbox_events SET 
                         status = ?,
                         last_error = ?,
                         processed_at = ?,
                         retry_count = retry_count + 1
                     WHERE id = ?`,
		domain.OutboxEventStatusFailed, errMsg, now, eventID)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event %d as failed: %w", eventID, err)
	}

	affected, err := execContext.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to fetch affected rows: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("failed to mark outbox event %d as failed: no affected", eventID)
	}

	return nil
}
