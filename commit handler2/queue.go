package queue

import (
	"errors"
	"sync"
)

var (
	ErrFull   = errors.New("queue full")
	ErrClosed = errors.New("queue closed")
)

// Queue — буферизированный канал с неблокирующей записью
// и безопасным закрытием при нескольких писателях.
type Queue[T any] struct {
	ch     chan T
	mu     sync.RWMutex
	closed bool
}

func New[T any](size int) *Queue[T] {
	return &Queue[T]{ch: make(chan T, size)}
}

// Push не блокируется. Можно вызывать из любых горутин, в том числе после Close.
func (q *Queue[T]) Push(v T) error {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.closed {
		return ErrClosed
	}
	select {
	case q.ch <- v:
		return nil
	default:
		return ErrFull
	}
}

// Close закрывает вход. Читатель дочитает остаток и завершится. Идемпотентен.
func (q *Queue[T]) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

func (q *Queue[T]) C() <-chan T { return q.ch }
func (q *Queue[T]) Len() int    { return len(q.ch) }
