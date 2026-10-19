package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"example.com/svc/internal/action/someaction"
	"example.com/svc/internal/batch"
	"example.com/svc/internal/client"
	"example.com/svc/internal/domain"
	"example.com/svc/internal/queue"
	"example.com/svc/internal/webhook"
)

type Config struct {
	HTTPAddr   string
	ServiceURL string
	QueueSize  int
	Batch      batch.Config
}

type (
	EventQueue   = queue.Queue[domain.Event]
	EventBatcher = batch.Batcher[domain.Event]
)

var Module = fx.Module("app",
	fx.Provide(
		newQueue,
		func(q *EventQueue) webhook.Enqueuer { return q },

		func(cfg Config) someaction.Client { return client.New(cfg.ServiceURL) },
		someaction.New,
		newBatcher,

		webhook.New,
		newServer,
	),
	// Порядок важен: fx останавливает в обратном порядке,
	// поэтому сначала гасится HTTP, потом дренируется очередь.
	fx.Invoke(startBatcher, startServer),
)

func newQueue(cfg Config) *EventQueue {
	return queue.New[domain.Event](cfg.QueueSize)
}

func newBatcher(cfg Config, act *someaction.Action, log *zap.Logger) *EventBatcher {
	return batch.New(cfg.Batch, func(ctx context.Context, events []domain.Event) {
		results, err := act.Handle(ctx, events)
		if err != nil {
			log.Error("batch failed", zap.Int("size", len(events)), zap.Error(err))
			return // здесь ретрай / DLQ
		}
		for _, r := range results {
			if r.Error != "" {
				log.Warn("event failed", zap.String("id", r.EventID), zap.String("error", r.Error))
			}
		}
	})
}

func newServer(cfg Config, h *webhook.Handlers) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func startBatcher(lc fx.Lifecycle, q *EventQueue, b *EventBatcher) {
	// Контекст обработчиков живёт до конца дренажа. Контекст из OnStart
	// не подходит: fx отменяет его сразу после старта.
	ctx, cancel := context.WithCancel(context.Background())

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			b.Start(ctx, q.C())
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			q.Close()              // новых событий не будет
			err := b.Wait(stopCtx) // дослать остаток
			cancel()               // не уложились — прерываем зависшие запросы
			return err
		},
	})
}

func startServer(lc fx.Lifecycle, srv *http.Server, log *zap.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr) // синхронно: занятый порт уронит старт
			if err != nil {
				return err
			}
			go func() {
				if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
					log.Error("http server stopped", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: srv.Shutdown, // дожидается активных хендлеров
	})
}
