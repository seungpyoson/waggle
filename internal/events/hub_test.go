package events

import (
	"sync"
	"testing"
	"time"
)

// TestHub_SubscribeAndPublish verifies E1: Published message reaches subscriber
func TestHub_SubscribeAndPublish(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe("topic-a", "sub1")

	msg := []byte("hello")
	hub.Publish("topic-a", msg)

	select {
	case received := <-ch:
		if string(received) != string(msg) {
			t.Errorf("expected %q, got %q", msg, received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for message")
	}
}

// TestHub_NoSubscribers verifies E2: Publishing to empty topic doesn't panic
func TestHub_NoSubscribers(t *testing.T) {
	hub := NewHub()
	// Should not panic
	hub.Publish("empty-topic", []byte("test"))
}

// TestHub_MultipleSubscribers verifies E1: All subscribers receive the message
func TestHub_MultipleSubscribers(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe("topic-a", "sub1")
	ch2 := hub.Subscribe("topic-a", "sub2")

	msg := []byte("broadcast")
	hub.Publish("topic-a", msg)

	// Both should receive
	select {
	case received := <-ch1:
		if string(received) != string(msg) {
			t.Errorf("sub1: expected %q, got %q", msg, received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sub1: timeout waiting for message")
	}

	select {
	case received := <-ch2:
		if string(received) != string(msg) {
			t.Errorf("sub2: expected %q, got %q", msg, received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sub2: timeout waiting for message")
	}
}

// TestHub_UnsubscribeStopsDelivery verifies E3: Unsubscribed channels stop receiving and are closed
func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe("topic-a", "sub1")

	// Unsubscribe
	hub.Unsubscribe("topic-a", "sub1")

	// Publish after unsubscribe
	hub.Publish("topic-a", []byte("should-not-receive"))

	// Channel should be closed
	select {
	case msg, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel, got message: %q", msg)
		}
		// ok == false means channel is closed, which is correct
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for channel close")
	}
}

// TestHub_UnsubscribeAll verifies E4: UnsubscribeAll removes from all topics
func TestHub_UnsubscribeAll(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe("topic-a", "sub1")
	ch2 := hub.Subscribe("topic-b", "sub1")

	// UnsubscribeAll should remove from both topics
	hub.UnsubscribeAll("sub1")

	// Both channels should be closed
	select {
	case msg, ok := <-ch1:
		if ok {
			t.Fatalf("topic-a: expected closed channel, got message: %q", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("topic-a: timeout waiting for channel close")
	}

	select {
	case msg, ok := <-ch2:
		if ok {
			t.Fatalf("topic-b: expected closed channel, got message: %q", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("topic-b: timeout waiting for channel close")
	}
}

// TestHub_TopicIsolation verifies E5: Different topics are isolated
func TestHub_TopicIsolation(t *testing.T) {
	hub := NewHub()
	chA := hub.Subscribe("topic-a", "sub1")
	hub.Subscribe("topic-b", "sub2")

	// Publish to topic-b only
	hub.Publish("topic-b", []byte("only-for-b"))

	// topic-a subscriber should NOT receive
	select {
	case msg := <-chA:
		t.Fatalf("topic-a should not receive message from topic-b, got: %q", msg)
	case <-time.After(50 * time.Millisecond):
		// Expected: timeout means no message received
	}
}

// TestHub_FullChannelDrops verifies E6: Full channel drops message without blocking
func TestHub_FullChannelDrops(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe("topic-a", "sub1")

	// Fill the channel (capacity 64)
	for i := 0; i < 64; i++ {
		hub.Publish("topic-a", []byte("fill"))
	}

	// Publish one more — should drop without blocking
	done := make(chan bool)
	go func() {
		hub.Publish("topic-a", []byte("overflow"))
		done <- true
	}()

	select {
	case <-done:
		// Good: publish returned immediately (dropped the message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Publish blocked on full channel (should drop)")
	}

	// Drain and verify we only got 64 messages
	count := 0
	for {
		select {
		case <-ch:
			count++
		case <-time.After(10 * time.Millisecond):
			if count != 64 {
				t.Errorf("expected 64 messages, got %d", count)
			}
			return
		}
	}
}

// TestHub_ConcurrentPublishSubscribe verifies E7: Concurrent operations don't race
func TestHub_ConcurrentPublishSubscribe(t *testing.T) {
	hub := NewHub()
	var wg sync.WaitGroup

	// Concurrent subscribers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			topic := "topic-a"
			name := string(rune('a' + id))
			ch := hub.Subscribe(topic, name)
			// Read a few messages
			for j := 0; j < 5; j++ {
				select {
				case <-ch:
				case <-time.After(100 * time.Millisecond):
					return
				}
			}
			hub.Unsubscribe(topic, name)
		}(i)
	}

	// Concurrent publishers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				hub.Publish("topic-a", []byte("concurrent"))
			}
		}()
	}

	wg.Wait()
}

