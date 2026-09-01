package hub

import (
	"sync"
	"testing"
	"time"
)

func TestPublishReachesAllSubscribers(t *testing.T) {
	h := New(4)
	a, b := h.Subscribe(), h.Subscribe()
	defer a.Close()
	defer b.Close()

	h.Publish(Event{Type: "metrics", Data: 1})

	for name, s := range map[string]*Subscriber{"a": a, "b": b} {
		select {
		case ev := <-s.C:
			if ev.Type != "metrics" {
				t.Errorf("%s got %q", name, ev.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("%s received nothing", name)
		}
	}
}

// The property that matters most: one browser tab that stops reading must not
// stall the collector for everyone else.
func TestSlowSubscriberIsDroppedNotBlocking(t *testing.T) {
	h := New(2)
	slow := h.Subscribe()
	fast := h.Subscribe()
	defer slow.Close()
	defer fast.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			h.Publish(Event{Type: "metrics", Data: i})
			<-fast.C // fast subscriber keeps up
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	if h.Dropped() == 0 {
		t.Error("expected drops for the subscriber that never read")
	}
}

// A dashboard that connects between ticks should render immediately.
func TestSubscribeReplaysLastPerType(t *testing.T) {
	h := New(8)
	h.Publish(Event{Type: "metrics", Data: "m1"})
	h.Publish(Event{Type: "disks", Data: "d1"})
	h.Publish(Event{Type: "metrics", Data: "m2"})

	s := h.Subscribe()
	defer s.Close()

	seen := map[string]any{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-s.C:
			seen[ev.Type] = ev.Data
		case <-time.After(time.Second):
			t.Fatalf("replay produced only %d events", len(seen))
		}
	}
	if seen["metrics"] != "m2" {
		t.Errorf("metrics replay = %v, want the latest (m2)", seen["metrics"])
	}
	if seen["disks"] != "d1" {
		t.Errorf("disks replay = %v", seen["disks"])
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	h := New(1)
	s := h.Subscribe()
	s.Close()
	s.Close() // must not panic on a double close
	if h.Count() != 0 {
		t.Errorf("Count = %d after close", h.Count())
	}
}

func TestConcurrentSubscribePublishClose(t *testing.T) {
	h := New(4)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				h.Publish(Event{Type: "metrics", Data: time.Now()})
			}
		}
	}()

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s := h.Subscribe()
				select {
				case <-s.C:
				default:
				}
				s.Close()
			}
		}()
	}

	time.AfterFunc(300*time.Millisecond, func() { close(stop) })
	wg.Wait()
	if h.Count() != 0 {
		t.Errorf("leaked %d subscribers", h.Count())
	}
}
