package main

import (
	"time"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"example.com/svc/internal/app"
	"example.com/svc/internal/batch"
)

func main() {
	fx.New(
		fx.Supply(app.Config{
			HTTPAddr:   ":8080",
			ServiceURL: "http://localhost:9090/process",
			QueueSize:  10_000,
			Batch: batch.Config{
				Size:    50,
				MaxWait: 200 * time.Millisecond,
				Workers: 4,
			},
		}),
		fx.Provide(zap.NewProduction),
		app.Module,
		fx.StopTimeout(30*time.Second), // общий бюджет на Shutdown HTTP + дренаж очереди
	).Run()
}
