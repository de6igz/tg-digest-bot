package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
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
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
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
		MessageId:    job.ID,
	})
	metrics.ObserveNetworkRequest("rabbitmq", "publish", q.queue, start, err)
	if err != nil {
		return fmt.Errorf("publish message: %w", err)
	}

	return nil
}

// Receive блокирующе читает задачу из очереди и возвращает функцию подтверждения.
func (q *RabbitDigestQueue) Receive(ctx context.Context) (domain.DigestJob, domain.DigestAckFunc, error) {
	for {
		select {
		case <-ctx.Done():
			return domain.DigestJob{}, nil, ctx.Err()
		case delivery, ok := <-q.deliveries:
			if !ok {
				return domain.DigestJob{}, nil, errors.New("rabbitmq: deliveries channel closed")
			}

			var job domain.DigestJob
			if err := json.Unmarshal(delivery.Body, &job); err != nil {
				_ = delivery.Nack(false, false)
				return domain.DigestJob{}, nil, fmt.Errorf("decode job: %w", err)
			}

			if job.ID == "" {
				switch {
				case delivery.MessageId != "":
					job.ID = delivery.MessageId
				default:
					job.ID = uuid.NewString()
				}
			}

			var once sync.Once
			ack := func(success bool) error {
				var ackErr error
				once.Do(func() {
					if success {
						ackErr = delivery.Ack(false)
					} else {
						ackErr = delivery.Nack(false, true)
					}
				})
				return ackErr
			}

			return job, ack, nil
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
