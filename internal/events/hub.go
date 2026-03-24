package events

import "sync"

type Hub struct {
	mu     sync.RWMutex
	topics map[string]map[string]chan []byte
}

func NewHub() *Hub {
	return &Hub{topics: make(map[string]map[string]chan []byte)}
}

func (h *Hub) Subscribe(topic, name string) <-chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.topics[topic] == nil {
		h.topics[topic] = make(map[string]chan []byte)
	}
	ch := make(chan []byte, 64)
	h.topics[topic][name] = ch
	return ch
}

func (h *Hub) Unsubscribe(topic, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.topics[topic]; ok {
		if ch, exists := subs[name]; exists {
			close(ch)
			delete(subs, name)
		}
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
	}
}

func (h *Hub) UnsubscribeAll(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic, subs := range h.topics {
		if ch, exists := subs[name]; exists {
			close(ch)
			delete(subs, name)
		}
		if len(subs) == 0 {
			delete(h.topics, topic)
		}
	}
}

func (h *Hub) Publish(topic string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	subs := h.topics[topic]
	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (h *Hub) TopicCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.topics)
}

func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, subs := range h.topics {
		count += len(subs)
	}
	return count
}

