package queue

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/metrics"
)

const defaultPollInterval = time.Second

// RabbitDigestQueue реализует очередь задач через HTTP API RabbitMQ.
type RabbitDigestQueue struct {
	client       *http.Client
	baseURL      *url.URL
	vhost        string
	queue        string
	username     string
	password     string
	pollInterval time.Duration
}

// NewRabbitDigestQueue создаёт очередь с использованием AMQP URL и Management API URL.
func NewRabbitDigestQueue(amqpURL, managementURL, queue string) (*RabbitDigestQueue, error) {
	if amqpURL == "" {
		return nil, errors.New("amqp url is empty")
	}
	parsed, err := url.Parse(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("parse amqp url: %w", err)
	}
	if queue == "" {
		return nil, errors.New("queue name is empty")
	}
	username := parsed.User.Username()
	password, _ := parsed.User.Password()
	vhost := strings.TrimPrefix(parsed.Path, "/")
	if vhost == "" {
		vhost = "/"
	}
	base := strings.TrimSpace(managementURL)
	if base == "" {
		scheme := "http"
		if parsed.Scheme == "amqps" {
			scheme = "https"
		}
		host := parsed.Hostname()
		port := "15672"
		base = fmt.Sprintf("%s://%s:%s", scheme, host, port)
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse management url: %w", err)
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	return &RabbitDigestQueue{
		client:       &http.Client{Timeout: 10 * time.Second},
		baseURL:      baseURL,
		vhost:        vhost,
		queue:        queue,
		username:     username,
		password:     password,
		pollInterval: defaultPollInterval,
	}, nil
}

// Enqueue публикует задачу в очередь.
func (q *RabbitDigestQueue) Enqueue(ctx context.Context, job domain.DigestJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	reqBody := map[string]any{
		"properties":       map[string]any{},
		"routing_key":      q.queue,
		"payload":          base64.StdEncoding.EncodeToString(payload),
		"payload_encoding": "base64",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	endpoint := q.baseURL.ResolveReference(&url.URL{Path: fmt.Sprintf("/api/exchanges/%s/amq.default/publish", url.PathEscape(q.vhost))})
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	q.applyAuth(req)
	resp, err := q.client.Do(req)
	metrics.ObserveNetworkRequest("rabbitmq", "publish", q.queue, start, err)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("publish failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// Pop блокирующе читает задачу из очереди.
func (q *RabbitDigestQueue) Pop(ctx context.Context) (domain.DigestJob, error) {
	for {
		if err := ctx.Err(); err != nil {
			return domain.DigestJob{}, err
		}
		messages, err := q.fetchMessages(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return domain.DigestJob{}, ctx.Err()
				}
				continue
			}
			return domain.DigestJob{}, err
		}
		if len(messages) == 0 {
			select {
			case <-ctx.Done():
				return domain.DigestJob{}, ctx.Err()
			case <-time.After(q.pollInterval):
			}
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(messages[0].Payload)
		if err != nil {
			return domain.DigestJob{}, fmt.Errorf("decode payload: %w", err)
		}
		var job domain.DigestJob
		if err := json.Unmarshal(payload, &job); err != nil {
			return domain.DigestJob{}, fmt.Errorf("decode job: %w", err)
		}
		return job, nil
	}
}

func (q *RabbitDigestQueue) fetchMessages(ctx context.Context) ([]rabbitMessage, error) {
	reqBody := map[string]any{
		"count":    1,
		"ackmode":  "ack_requeue_false",
		"encoding": "base64",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := q.baseURL.ResolveReference(&url.URL{Path: fmt.Sprintf("/api/queues/%s/%s/get", url.PathEscape(q.vhost), url.PathEscape(q.queue))})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	q.applyAuth(req)
	start := time.Now()
	resp, err := q.client.Do(req)
	metrics.ObserveNetworkRequest("rabbitmq", "get", q.queue, start, err)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fetch messages failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var messages []rabbitMessage
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return messages, nil
}

func (q *RabbitDigestQueue) applyAuth(req *http.Request) {
	if q.username != "" {
		req.SetBasicAuth(q.username, q.password)
	}
}

type rabbitMessage struct {
	Payload string `json:"payload"`
}
