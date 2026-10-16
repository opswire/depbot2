package someaction

import (
	"context"

	"example.com/svc/internal/domain"
)

// Client — внешний сервис, который обрабатывает батч.
type Client interface {
	Process(ctx context.Context, events []domain.Event) ([]domain.Result, error)
}

type Action struct {
	client Client
}

func New(c Client) *Action {
	return &Action{client: c}
}

// Handle принимает батч и возвращает результат по каждому событию.
func (a *Action) Handle(ctx context.Context, events []domain.Event) ([]domain.Result, error) {
	// Здесь бизнес-логика: фильтрация, маппинг, дедупликация по ID и т.п.
	return a.client.Process(ctx, events)
}
