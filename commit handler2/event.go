package domain

import "encoding/json"

type EventType string

const (
	EventOrder   EventType = "order"
	EventPayment EventType = "payment"
)

// Event — вебхук, принятый и поставленный в очередь.
type Event struct {
	ID      string          `json:"id"`
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload"` // тело как есть, без лишнего парсинга в хендлере
}

// Result — ответ сервиса по одному событию.
type Result struct {
	EventID string `json:"event_id"`
	Error   string `json:"error,omitempty"`
}
