package webhook

import (
	"encoding/json"
	"io"
	"net/http"

	"example.com/svc/internal/domain"
)

const maxBodySize = 1 << 20 // 1 MiB

type Enqueuer interface {
	Push(domain.Event) error
}

type Handlers struct {
	q Enqueuer
}

func New(q Enqueuer) *Handlers {
	return &Handlers{q: q}
}

func (h *Handlers) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/orders", h.accept(domain.EventOrder))
	mux.HandleFunc("POST /webhooks/payments", h.accept(domain.EventPayment))
	return mux
}

// accept быстро проверяет запрос и кладёт событие в очередь, не дожидаясь обработки.
func (h *Handlers) accept(typ domain.EventType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
		if err != nil || !json.Valid(body) {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		// Здесь же — проверка подписи отправителя, если она есть.

		ev := domain.Event{
			ID:      r.Header.Get("X-Event-ID"),
			Type:    typ,
			Payload: body,
		}

		if err := h.q.Push(ev); err != nil {
			// ErrFull или ErrClosed: отправитель повторит запрос позже.
			w.Header().Set("Retry-After", "5")
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
