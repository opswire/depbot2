package batch

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	sizes []int
	total int
}

func (r *recorder) handle(_ context.Context, b []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sizes = append(r.sizes, len(b))
	r.total += len(b)
}

func TestFlushBySize(t *testing.T) {
	var r recorder
	in := make(chan int, 1000)
	b := New(Config{Size: 50, MaxWait: time.Hour, Workers: 4}, r.handle)
	b.Start(in)

	for i := range 1000 {
		in <- i
	}
	close(in)

	if err := b.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.total != 1000 || len(r.sizes) != 20 {
		t.Fatalf("total=%d batches=%d, want 1000/20", r.total, len(r.sizes))
	}
}

func TestFlushByTimeout(t *testing.T) {
	var r recorder
	in := make(chan int, 10)
	b := New(Config{Size: 50, MaxWait: 20 * time.Millisecond, Workers: 1}, r.handle)
	b.Start(in)

	in <- 1
	in <- 2
	time.Sleep(60 * time.Millisecond) // канал не закрыт — сработать должен таймер

	r.mu.Lock()
	got := append([]int(nil), r.sizes...)
	r.mu.Unlock()
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("sizes=%v, want [2]", got)
	}

	close(in)
	_ = b.Wait(context.Background())
}

func TestDrainOnClose(t *testing.T) {
	var r recorder
	in := make(chan int, 100)
	b := New(Config{Size: 50, MaxWait: time.Hour, Workers: 2}, r.handle)
	b.Start(in)

	for i := range 73 {
		in <- i
	}
	close(in)
	_ = b.Wait(context.Background())

	if r.total != 73 {
		t.Fatalf("total=%d, want 73", r.total)
	}
}

func BenchmarkBatcher(b *testing.B) {
	in := make(chan int, 10_000)
	bt := New(Config{Size: 50, MaxWait: time.Second, Workers: 4}, func(context.Context, []int) {})
	bt.Start(in)

	b.ResetTimer()
	for i := range b.N {
		in <- i
	}
	close(in)
	_ = bt.Wait(context.Background())
}
