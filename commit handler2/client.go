package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"example.com/svc/internal/domain"
)

// Client отправляет батч в сервис одним POST-запросом.
type Client struct {
	url  string
	http *http.Client
}

func New(url string) *Client {
	return &Client{
		url: url,
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100, // по умолчанию 2 — узкое место при нескольких воркерах
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (c *Client) Process(ctx context.Context, events []domain.Event) ([]domain.Result, error) {
	body, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service returned %s", resp.Status)
	}

	var results []domain.Result
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}
	return results, nil
}
