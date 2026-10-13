package queue

import (
	"context"
	"errors"
	"sync"
)

var ErrFull = errors.New("queue full")

type Event struct {
	Type    string
	Payload []byte
}

type Handler func(ctx context.Context, ev Event)

type Queue struct {
	ch      chan Event
	workers int
	handler Handler
	wg      sync.WaitGroup
}

func New(size, workers int, h Handler) *Queue {
	return &Queue{
		ch:      make(chan Event, size),
		workers: workers,
		handler: h,
	}
}

func (q *Queue) Start(ctx context.Context) {
	for range q.workers {
		q.wg.Go(func() {
			for ev := range q.ch {
				q.handler(ctx, ev)
			}
		})
	}
}

// Неблокирующая постановка: при переполнении возвращает ErrFull.
func (q *Queue) Push(ev Event) error {
	select {
	case q.ch <- ev:
		return nil
	default:
		return ErrFull
	}
}

// Вызывать после остановки HTTP-сервера: дренирует очередь и ждёт воркеров.
func (q *Queue) Close() {
	close(q.ch)
	q.wg.Wait()
}

func (q *Queue) Len() int { return len(q.ch) } // пригодится для метрик