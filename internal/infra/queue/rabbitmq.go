package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
)

const (
	defaultPrefetch    = 1
	publishContentType = "application/json"
)

// RabbitDigestQueue реализует очередь задач через AMQP соединение с RabbitMQ.
type RabbitDigestQueue struct {
	conn       *amqp.Connection
	channel    *amqp.Channel
	deliveries <-chan amqp.Delivery
	queue      string
}

// NewRabbitDigestQueue создаёт очередь и настраивает потребителя.
func NewRabbitDigestQueue(amqpURL, queue string) (*RabbitDigestQueue, error) {
	if amqpURL == "" {
		return nil, errors.New("amqp url is empty")
	}
	if queue == "" {
		return nil, errors.New("queue name is empty")
	}

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if err := ch.Qos(defaultPrefetch, 0, false); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("set qos: %w", err)
	}

	if _, err := ch.QueueDeclare(
		queue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("declare queue: %w", err)
	}

	deliveries, err := ch.Consume(
		queue,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("start consuming: %w", err)
	}

	return &RabbitDigestQueue{
		conn:       conn,
		channel:    ch,
		deliveries: deliveries,
		queue:      queue,
	}, nil
}

// Enqueue публикует задачу в очередь RabbitMQ.
func (q *RabbitDigestQueue) Enqueue(ctx context.Context, job domain.DigestJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	start := time.Now()
	err = q.channel.PublishWithContext(ctx, "", q.queue, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  publishContentType,
		Body:         payload,
		Timestamp:    time.Now().UTC(),
		Type:         string(job.Cause),
	})
	metrics.ObserveNetworkRequest("rabbitmq", "publish", q.queue, start, err)
	if err != nil {
		return fmt.Errorf("publish message: %w", err)
	}

	return nil
}

// Pop блокирующе читает задачу из очереди.
func (q *RabbitDigestQueue) Pop(ctx context.Context) (domain.DigestJob, error) {
	for {
		select {
		case <-ctx.Done():
			return domain.DigestJob{}, ctx.Err()
		case delivery, ok := <-q.deliveries:
			if !ok {
				return domain.DigestJob{}, errors.New("rabbitmq: deliveries channel closed")
			}

			var job domain.DigestJob
			if err := json.Unmarshal(delivery.Body, &job); err != nil {
				_ = delivery.Nack(false, false)
				return domain.DigestJob{}, fmt.Errorf("decode job: %w", err)
			}

			if err := delivery.Ack(false); err != nil {
				return domain.DigestJob{}, fmt.Errorf("ack message: %w", err)
			}

			return job, nil
		}
	}
}

// Close освобождает ресурсы очереди.
func (q *RabbitDigestQueue) Close() error {
	if q.channel != nil {
		_ = q.channel.Close()
	}
	if q.conn != nil {
		return q.conn.Close()
	}
	return nil
}
