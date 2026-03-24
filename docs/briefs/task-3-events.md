# Task 3 Brief: Events Hub — In-Memory Pub/Sub

**Branch:** `feat/001-004-foundation` (same branch, commit after Tasks 1-2)
**Goal:** Pure in-memory fan-out for fire-and-forget events. No persistence, no replay.

**Files to create:**
- `internal/events/hub.go`
- `internal/events/hub_test.go`

**Dependencies:** None (no imports from other internal packages).

---

## What to build

**`Hub` struct** — Thread-safe topic registry. Internal map: `topic → subscriber name → chan []byte`

**Methods:**
- `NewHub() *Hub`
- `Subscribe(topic, name string) <-chan []byte` — register subscriber, return buffered channel (capacity 64)
- `Unsubscribe(topic, name string)` — remove subscriber, close channel, clean up empty topics
- `UnsubscribeAll(name string)` — remove subscriber from ALL topics (used on session disconnect)
- `Publish(topic string, msg []byte)` — fan out to all subscribers. Non-blocking: drop if channel full (fire-and-forget)
- `TopicCount() int` — number of active topics
- `SubscriberCount() int` — total subscribers across all topics

**Thread safety:** All methods use `sync.RWMutex`. Publish uses RLock (read). Everything else uses Lock (write).

## Invariants

| ID | Invariant | How to verify |
|----|-----------|---------------|
| E1 | Published message reaches all subscribers of that topic | Test: 2 subscribers, both receive |
| E2 | Publishing to a topic with no subscribers does not panic | Test: publish to empty topic |
| E3 | Unsubscribed channels stop receiving and are closed | Test: unsubscribe, publish, verify no receive + channel closed |
| E4 | UnsubscribeAll removes from all topics | Test: subscribe to 2 topics, UnsubscribeAll, verify removed |
| E5 | Different topics are isolated | Test: subscribe to topic-a, publish to topic-b, verify no receive on topic-a |
| E6 | Full channel drops message without blocking | Test: fill channel (64 msgs), publish one more, verify no deadlock |
| E7 | Concurrent publish/subscribe doesn't race | Test: run with -race flag |

## Tests

```
TestHub_SubscribeAndPublish       — publish reaches subscriber
TestHub_NoSubscribers             — publish to empty topic, no panic
TestHub_MultipleSubscribers       — 2 subscribers both receive same message
TestHub_UnsubscribeStopsDelivery  — unsubscribe, publish, no receive
TestHub_UnsubscribeAll            — removes from all topics
TestHub_TopicIsolation            — different topics don't cross (NEW)
TestHub_FullChannelDrops          — 65th message dropped, no deadlock (NEW)
```

## Acceptance criteria

- [ ] All 7 tests pass: `go test ./internal/events/ -v -count=1`
- [ ] Race detector passes: `go test ./internal/events/ -race -count=1`
- [ ] `go vet ./internal/events/` — zero warnings
- [ ] Only imports `sync`, `testing`, `time`
- [ ] hub.go under 80 lines
- [ ] Commit: `feat(events): in-memory pub/sub hub`
