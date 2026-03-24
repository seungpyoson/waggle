# Task 3: Events Hub — In-Memory Pub/Sub

**Files:**
- Create: `internal/events/hub.go`
- Create: `internal/events/hub_test.go`

Pure in-memory fan-out. No persistence, no replay (v1). Clean interface that the broker calls.

- [ ] **Step 1: Write failing tests**

```go
// internal/events/hub_test.go
package events

import (
    "testing"
    "time"
)

func TestHub_SubscribeAndPublish(t *testing.T) {
    h := NewHub()
    ch := h.Subscribe("task.events", "worker-1")
    defer h.Unsubscribe("task.events", "worker-1")

    h.Publish("task.events", []byte(`{"event":"task.created"}`))

    select {
    case msg := <-ch:
        if string(msg) != `{"event":"task.created"}` {
            t.Errorf("got %q", string(msg))
        }
    case <-time.After(time.Second):
        t.Fatal("timeout waiting for message")
    }
}

func TestHub_NoSubscribers(t *testing.T) {
    h := NewHub()
    // Should not panic
    h.Publish("task.events", []byte(`{"event":"test"}`))
}

func TestHub_MultipleSubscribers(t *testing.T) {
    h := NewHub()
    ch1 := h.Subscribe("topic-a", "sub-1")
    ch2 := h.Subscribe("topic-a", "sub-2")
    defer h.Unsubscribe("topic-a", "sub-1")
    defer h.Unsubscribe("topic-a", "sub-2")

    h.Publish("topic-a", []byte(`hello`))

    for _, ch := range []<-chan []byte{ch1, ch2} {
        select {
        case msg := <-ch:
            if string(msg) != "hello" {
                t.Errorf("got %q", string(msg))
            }
        case <-time.After(time.Second):
            t.Fatal("timeout")
        }
    }
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
    h := NewHub()
    ch := h.Subscribe("t", "s")
    h.Unsubscribe("t", "s")
    h.Publish("t", []byte(`msg`))

    select {
    case <-ch:
        t.Fatal("received message after unsubscribe")
    case <-time.After(100 * time.Millisecond):
        // Expected
    }
}

func TestHub_UnsubscribeAll(t *testing.T) {
    h := NewHub()
    h.Subscribe("t1", "s")
    h.Subscribe("t2", "s")
    h.UnsubscribeAll("s")

    h.Publish("t1", []byte(`msg`))
    h.Publish("t2", []byte(`msg`))
    // No panic = pass (channels closed)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/events/ -v`
Expected: FAIL — `NewHub` not defined

- [ ] **Step 3: Implement Hub**

```go
// internal/events/hub.go
package events

import "sync"

// Hub is an in-memory pub/sub fan-out. Thread-safe.
type Hub struct {
    mu   sync.RWMutex
    subs map[string]map[string]chan []byte // topic -> subscriber name -> channel
}

func NewHub() *Hub {
    return &Hub{subs: make(map[string]map[string]chan []byte)}
}

// Subscribe registers a named subscriber for a topic. Returns a channel for receiving messages.
func (h *Hub) Subscribe(topic, name string) <-chan []byte {
    h.mu.Lock()
    defer h.mu.Unlock()

    if h.subs[topic] == nil {
        h.subs[topic] = make(map[string]chan []byte)
    }
    ch := make(chan []byte, 64)
    h.subs[topic][name] = ch
    return ch
}

// Unsubscribe removes a subscriber from a topic and closes its channel.
func (h *Hub) Unsubscribe(topic, name string) {
    h.mu.Lock()
    defer h.mu.Unlock()

    if topicSubs, ok := h.subs[topic]; ok {
        if ch, ok := topicSubs[name]; ok {
            close(ch)
            delete(topicSubs, name)
        }
        if len(topicSubs) == 0 {
            delete(h.subs, topic)
        }
    }
}

// UnsubscribeAll removes a subscriber from all topics. Used on session disconnect.
func (h *Hub) UnsubscribeAll(name string) {
    h.mu.Lock()
    defer h.mu.Unlock()

    for topic, topicSubs := range h.subs {
        if ch, ok := topicSubs[name]; ok {
            close(ch)
            delete(topicSubs, name)
        }
        if len(topicSubs) == 0 {
            delete(h.subs, topic)
        }
    }
}

// Publish sends a message to all subscribers of a topic. Non-blocking — drops if buffer full.
func (h *Hub) Publish(topic string, msg []byte) {
    h.mu.RLock()
    defer h.mu.RUnlock()

    for _, ch := range h.subs[topic] {
        select {
        case ch <- msg:
        default:
            // Subscriber buffer full — drop (fire-and-forget)
        }
    }
}

// TopicCount returns the number of active topics.
func (h *Hub) TopicCount() int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    return len(h.subs)
}

// SubscriberCount returns total subscriber count across all topics.
func (h *Hub) SubscriberCount() int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    count := 0
    for _, topicSubs := range h.subs {
        count += len(topicSubs)
    }
    return count
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ~/Projects/Claude/waggle && go test ./internal/events/ -v`
Expected: PASS (5/5)

- [ ] **Step 5: Commit**

```bash
git add internal/events/
python3 ~/.claude/lib/safe_git.py commit -m "feat: events hub — in-memory pub/sub fan-out"
```
