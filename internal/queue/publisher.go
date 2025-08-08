package queue

import (
	"context"
	"encoding/json"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Publisher struct {
	ch       *amqp.Channel
	exchange string
	routing  string
}

func NewPublisher(conn *amqp.Connection, exchange, routing string) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	return &Publisher{ch: ch, exchange: exchange, routing: routing}, nil
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
	return p.ch.PublishWithContext(ctx, p.exchange, p.routing, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        b,
	})
}
