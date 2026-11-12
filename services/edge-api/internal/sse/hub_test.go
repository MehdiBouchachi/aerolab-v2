package sse

import (
	"testing"
	"time"
)

func TestSlowSubscriberIsDroppedAndMemoryStaysBounded(t *testing.T) {
	h := NewHub()
	slow, _ := h.Subscribe("u1") // never reads
	fast, _ := h.Subscribe("u2")
	got := 0
	done := make(chan struct{})
	go func() {
		for range fast.C {
			got++
		}
		close(done)
	}()
	for i := 0; i < QueueLen*3; i++ {
		h.Publish("unit.state", map[string]int{"i": i})
		time.Sleep(50 * time.Microsecond)
	}
	if h.Dropped() != 1 || h.Count() != 1 {
		t.Fatalf("dropped=%d subscribers=%d, want the slow one removed", h.Dropped(), h.Count())
	}
	if len(slow.C) > QueueLen {
		t.Fatalf("queue grew to %d", len(slow.C))
	}
	fast.Close()
	<-done
	if got < QueueLen*3-5 {
		t.Fatalf("healthy subscriber lost events: %d", got)
	}
}

func TestSubscriberLimits(t *testing.T) {
	h := NewHub()
	for i := 0; i < MaxPerUser; i++ {
		if _, err := h.Subscribe("u"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Subscribe("u"); err != ErrTooMany {
		t.Fatalf("per-user cap not enforced: %v", err)
	}
	h2 := NewHub()
	for i := 0; i < MaxSubscribers; i++ {
		if _, err := h2.Subscribe(string(rune('a' + i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h2.Subscribe("zz"); err != ErrTooMany {
		t.Fatal("global cap not enforced")
	}
}

func TestEventIDsIncreaseAndCloseIsIdempotent(t *testing.T) {
	h := NewHub()
	s, _ := h.Subscribe("u")
	h.Publish("a", 1)
	h.Publish("b", 2)
	e1, e2 := <-s.C, <-s.C
	if e2.ID <= e1.ID {
		t.Fatal("ids not increasing")
	}
	s.Close()
	s.Close()
	if h.Count() != 0 {
		t.Fatal("leaked subscriber")
	}
}
