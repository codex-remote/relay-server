package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Broker interface {
	Health(context.Context) error
	AppendRunEvent(context.Context, string, RunEvent) error
	ReadRunEvents(context.Context, string, int64) ([]RunEvent, error)
	NotifySession(context.Context, string) error
	SetAgentPresence(context.Context, bool) error
	AgentPresence(context.Context) (bool, time.Time, error)
	Close() error
}

type pollWaiter interface {
	WaitForRun(ctx context.Context, runID string) error
	WaitForSession(ctx context.Context, sessionID string) error
}

type runNotifier interface {
	NotifyRun(context.Context, string) error
}

type RedisBroker struct{ client *redis.Client }

func OpenRedis(ctx context.Context, rawURL string) (*RedisBroker, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	client := redis.NewClient(options)
	broker := &RedisBroker{client: client}
	if err := broker.Health(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return broker, nil
}

func (b *RedisBroker) Health(ctx context.Context) error { return b.client.Ping(ctx).Err() }
func (b *RedisBroker) Close() error                     { return b.client.Close() }

func runStream(runID string) string { return "runtime:result:run:" + runID }

func (b *RedisBroker) AppendRunEvent(ctx context.Context, runID string, event RunEvent) error {
	id := strconv.FormatInt(event.Sequence, 10) + "-0"
	values := map[string]any{"event_type": event.Type, "schema_version": "1", "payload": string(event.Payload), "occurred_at": event.OccurredAt.UTC().Format(time.RFC3339Nano)}
	err := b.client.XAdd(ctx, &redis.XAddArgs{Stream: runStream(runID), ID: id, Values: values}).Err()
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "equal or smaller") {
		return err
	}
	existing, readErr := b.client.XRangeN(ctx, runStream(runID), id, id, 1).Result()
	if readErr != nil || len(existing) != 1 {
		return err
	}
	encoded, _ := json.Marshal(existing[0].Values)
	wanted, _ := json.Marshal(values)
	if string(encoded) != string(wanted) {
		return fmt.Errorf("Redis event conflict for %s sequence %d", runID, event.Sequence)
	}
	return nil
}

func (b *RedisBroker) ReadRunEvents(ctx context.Context, runID string, after int64) ([]RunEvent, error) {
	start := fmt.Sprintf("(%d-0", after)
	items, err := b.client.XRange(ctx, runStream(runID), start, "+").Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]RunEvent, 0, len(items))
	for _, item := range items {
		parts := strings.SplitN(item.ID, "-", 2)
		sequence, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if parseErr != nil {
			return nil, parseErr
		}
		payload := json.RawMessage(fmt.Sprint(item.Values["payload"]))
		occurredAt, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(item.Values["occurred_at"]))
		result = append(result, RunEvent{Sequence: sequence, Type: fmt.Sprint(item.Values["event_type"]), Payload: payload, OccurredAt: occurredAt})
	}
	return result, nil
}

func (b *RedisBroker) NotifySession(ctx context.Context, sessionID string) error {
	return b.notify(ctx, "session", sessionID)
}

func (b *RedisBroker) NotifyRun(ctx context.Context, runID string) error {
	return b.notify(ctx, "run", runID)
}

func (b *RedisBroker) WaitForRun(ctx context.Context, runID string) error {
	return b.waitForNotification(ctx, "runtime:notify:run:"+runID)
}

func (b *RedisBroker) WaitForSession(ctx context.Context, sessionID string) error {
	return b.waitForNotification(ctx, "runtime:notify:session:"+sessionID)
}

func (b *RedisBroker) waitForNotification(ctx context.Context, channel string) error {
	versionKey := channelVersionKey(channel)
	before, _ := b.client.Get(ctx, versionKey).Result()
	subscriber := b.client.Subscribe(ctx, channel)
	defer subscriber.Close()
	if _, err := subscriber.Receive(ctx); err != nil {
		return err
	}
	after, _ := b.client.Get(ctx, versionKey).Result()
	if after != before {
		return nil
	}
	messages := subscriber.Channel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case _, ok := <-messages:
		if !ok {
			return errors.New("Redis notification subscription closed")
		}
		return nil
	}
}

func (b *RedisBroker) notify(ctx context.Context, kind, resourceID string) error {
	channel := "runtime:notify:" + kind + ":" + resourceID
	if err := b.client.Set(ctx, channelVersionKey(channel), strconv.FormatInt(time.Now().UnixNano(), 10), time.Hour).Err(); err != nil {
		return err
	}
	return b.client.Publish(ctx, channel, "changed").Err()
}

func channelVersionKey(channel string) string { return channel + ":version" }

func (b *RedisBroker) SetAgentPresence(ctx context.Context, online bool) error {
	if !online {
		return b.client.Del(ctx, "runtime:presence:agent").Err()
	}
	value := time.Now().UTC().Format(time.RFC3339Nano)
	return b.client.Set(ctx, "runtime:presence:agent", value, 45*time.Second).Err()
}

func (b *RedisBroker) AgentPresence(ctx context.Context) (bool, time.Time, error) {
	value, err := b.client.Get(ctx, "runtime:presence:agent").Result()
	if errors.Is(err, redis.Nil) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	when, err := time.Parse(time.RFC3339Nano, value)
	return true, when, err
}
