package batch

import (
	"context"
	"sync"
	"time"
)

type Config struct {
	Size    int           // максимальный размер батча
	MaxWait time.Duration // сколько максимум ждёт первый элемент батча
	Workers int           // сколько батчей обрабатывается параллельно
}

// Handler обрабатывает один батч. Ошибки и результаты — его зона ответственности.
type Handler[T any] func(ctx context.Context, batch []T)

// Batcher собирает элементы из канала в батчи по размеру или таймауту
// и обрабатывает их пулом воркеров.
type Batcher[T any] struct {
	cfg     Config
	handle  Handler[T]
	batches chan []T
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

func New[T any](cfg Config, h Handler[T]) *Batcher[T] {
	if cfg.Size <= 0 {
		cfg.Size = 50
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 200 * time.Millisecond
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Batcher[T]{
		cfg:     cfg,
		handle:  h,
		batches: make(chan []T, cfg.Workers),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start запускает сборщик и воркеры. Работает, пока in не закрыт.
func (b *Batcher[T]) Start(in <-chan T) {
	b.wg.Go(func() { b.collect(in) })

	for range b.cfg.Workers {
		b.wg.Go(func() {
			for batch := range b.batches {
				b.handle(b.ctx, batch)
			}
		})
	}
}

// Wait ждёт, пока будут обработаны все батчи (in к этому моменту должен быть закрыт).
// Если ctx истёк раньше, отменяет контекст обработчиков и возвращает ошибку.
func (b *Batcher[T]) Wait(ctx context.Context) error {
	defer b.cancel()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Batcher[T]) collect(in <-chan T) {
	defer close(b.batches)

	buf := make([]T, 0, b.cfg.Size)

	// Таймер взводится, когда в батч приходит первый элемент.
	// С Go 1.23 Stop/Reset гарантируют, что в timer.C не останется старого значения.
	timer := time.NewTimer(b.cfg.MaxWait)
	timer.Stop()
	defer timer.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		timer.Stop()
		b.batches <- buf               // блокируется, если воркеры заняты: это и есть backpressure
		buf = make([]T, 0, b.cfg.Size) // старый слайс теперь принадлежит воркеру
	}

	for {
		select {
		case v, ok := <-in:
			if !ok {
				flush()
				return
			}
			buf = append(buf, v)
			if len(buf) == 1 {
				timer.Reset(b.cfg.MaxWait)
			}
			if len(buf) == b.cfg.Size {
				flush()
			}

		case <-timer.C:
			flush()
		}
	}
}
