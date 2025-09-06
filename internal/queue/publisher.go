package queue

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sony/gobreaker"
)

type Publisher struct {
	ch       *amqp.Channel
	exchange string
	routing  string
	breaker  *gobreaker.CircuitBreaker
}

func NewPublisher(conn *amqp.Connection, exchange, routing string) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	cbTimeout := 5 * time.Second
	if v := getEnv("TRANSCODER_PUB_CB_RESET_MS", ""); v != "" { if d, err := time.ParseDuration(v+"ms"); err == nil { cbTimeout = d } }
	cbFailures := uint32(5)
	if v := getEnv("TRANSCODER_PUB_CB_FAILS", ""); v != "" { if n, err := strconv.Atoi(v); err == nil && n > 0 { cbFailures = uint32(n) } }
	breaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{ Name: "amqp-publish", Timeout: cbTimeout, ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= cbFailures } })
	return &Publisher{ch: ch, exchange: exchange, routing: routing, breaker: breaker}, nil
}

func (p *Publisher) Close() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
}

func (p *Publisher) PublishJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Publish with breaker + retry backoff
	retries := 2
	if v := getEnv("TRANSCODER_PUB_RETRIES", ""); v != "" { if n, err := strconv.Atoi(v); err == nil && n >= 0 { retries = n } }
	var last error
	backoff := 200 * time.Millisecond
	for i := 0; i <= retries; i++ {
		_, err = p.breaker.Execute(func() (interface{}, error) {
			return nil, p.ch.PublishWithContext(ctx, p.exchange, p.routing, false, false, amqp.Publishing{
				ContentType: "application/json",
				Body:        b,
			})
		})
		if err == nil { return nil }
		last = err
		if i < retries { time.Sleep(backoff); if backoff < 1500*time.Millisecond { backoff *= 2 } }
	}
	return last
}
