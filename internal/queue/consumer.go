package queue

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

// Consumer wraps RabbitMQ consumption.
type Consumer struct {
	conn *amqp.Connection
	log  *zap.SugaredLogger

	url              string
	exchange         string
	uploadRoutingKey string
	queueName        string
}

func GetEnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func getEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func NewConsumerFromEnv(log *zap.SugaredLogger) (*Consumer, error) {
	c := &Consumer{
		log:              log,
		url:              getEnv("AMQP_URL", "amqp://guest:guest@localhost:5672/"),
		exchange:         getEnv("AMQP_EXCHANGE", "streamhive"),
		uploadRoutingKey: getEnv("AMQP_UPLOAD_ROUTING_KEY", "video.uploaded"),
		queueName:        getEnv("AMQP_QUEUE", "transcoder.video.uploaded"),
	}

	retries := GetEnvInt("AMQP_CONNECT_RETRIES", 30)
	backoffMS := GetEnvInt("AMQP_CONNECT_BACKOFF_MS", 1000)

	var conn *amqp.Connection
	var err error
	for attempt := 1; attempt <= retries; attempt++ {
		conn, err = amqp.DialConfig(c.url, amqp.Config{Properties: amqp.Table{"connection_name": "transcoder"}})
		if err == nil {
			break
		}
		log.Warnw("amqp dial failed, retrying", "attempt", attempt, "err", err)
		time.Sleep(time.Duration(backoffMS) * time.Millisecond)
	}
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	c.conn = conn

	// Ensure exchange/queue exist once using a setup channel
	ch, err := conn.Channel()
	if err != nil {
		c.conn.Close()
		return nil, fmt.Errorf("channel: %w", err)
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(c.exchange, "topic", true, false, false, false, nil); err != nil {
		return nil, fmt.Errorf("exchange declare: %w", err)
	}
	q, err := ch.QueueDeclare(c.queueName, true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("queue declare: %w", err)
	}
	if err := ch.QueueBind(q.Name, c.uploadRoutingKey, c.exchange, false, nil); err != nil {
		return nil, fmt.Errorf("queue bind: %w", err)
	}
	return c, nil
}

func (c *Consumer) Close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// Conn returns the underlying AMQP connection for creating publishers.
func (c *Consumer) Conn() *amqp.Connection { return c.conn }

// Exchange returns the configured exchange name.
func (c *Consumer) Exchange() string { return c.exchange }

// Consume starts N independent consumers (one channel per worker) and calls handler per message.
func (c *Consumer) Consume(ctx context.Context, workers int, handler func([]byte) error) error {
	if workers < 1 {
		workers = 1
	}
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			ch, err := c.conn.Channel()
			if err != nil {
				errCh <- fmt.Errorf("worker %d channel: %w", idx, err)
				return
			}
			defer ch.Close()

			// Fair dispatch
			_ = ch.Qos(1, 0, false)
			consumerTag := fmt.Sprintf("transcoder-%d-%d", os.Getpid(), idx)
			deliveries, err := ch.Consume(c.queueName, consumerTag, false, false, false, false, nil)
			if err != nil {
				errCh <- fmt.Errorf("worker %d consume: %w", idx, err)
				return
			}

			for {
				select {
				case <-ctx.Done():
					return
				case d, ok := <-deliveries:
					if !ok {
						return
					}
					start := time.Now()
					if err := handler(d.Body); err != nil {
						c.log.Errorw("handler error", "err", err)
						_ = d.Nack(false, false) // send to DLQ if configured
						continue
					}
					_ = d.Ack(false)
					c.log.Debugw("processed message", "ms", time.Since(start).Milliseconds())
				}
			}
		}(i)
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
