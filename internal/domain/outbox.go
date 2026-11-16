package domain

import "time"

type OutboxStatus string

const (
	OutboxEventStatusPending OutboxStatus = "pending"
	OutboxEventStatusFailed  OutboxStatus = "failed"
	OutboxEventStatusDone    OutboxStatus = "done"
)

type OutboxAction string

const (
	OutboxEventActionUpsert OutboxAction = "upsert"
	OutboxEventActionDelete OutboxAction = "delete"
)

type OutboxEvent struct {
	ID             int64        `json:"id"`
	DocID          string       `json:"doc_id"`
	Action         OutboxAction `json:"action"`
	Version        int64        `json:"version"`
	PayloadChanged bool         `json:"payload_changed"`
	Status         OutboxStatus `json:"status"`
	RetryCount     int          `json:"retry_count"`
	LastError      string       `json:"last_error"`
	CreatedAt      time.Time    `json:"created_at"`
	ProcessedAt    *time.Time   `json:"processed_at"`
}

func (s OutboxStatus) IsValid() bool {
	switch s {
	case OutboxEventStatusPending, OutboxEventStatusFailed, OutboxEventStatusDone:
		return true
	default:
		return false
	}
}

func (a OutboxAction) IsValid() bool {
	switch a {
	case OutboxEventActionUpsert, OutboxEventActionDelete:
		return true
	default:
		return false
	}
}
