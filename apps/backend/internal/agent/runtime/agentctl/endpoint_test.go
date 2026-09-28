package client

import (
	"sync"
	"testing"
)

func TestEndpoint_CurrentBeforePublishIsZeroValue(t *testing.T) {
	e := NewEndpoint()
	id := e.Current()
	if id.Generation != 0 {
		t.Errorf("Generation = %d, want 0", id.Generation)
	}
	if id.Host != "" || id.Port != 0 || id.Token != "" || id.PID != 0 {
		t.Errorf("Current() = %+v, want zero value", id)
	}
}

func TestEndpoint_PublishIncrementsGeneration(t *testing.T) {
	e := NewEndpoint()

	first := e.Publish("localhost", 39429, "token-1", 111)
	if first.Generation != 1 {
		t.Errorf("first Generation = %d, want 1", first.Generation)
	}

	second := e.Publish("localhost", 40001, "token-2", 222)
	if second.Generation != 2 {
		t.Errorf("second Generation = %d, want 2", second.Generation)
	}
	if second.Port != 40001 || second.Token != "token-2" || second.PID != 222 {
		t.Errorf("second identity = %+v, want the new values", second)
	}

	if got := e.Current(); got != second {
		t.Errorf("Current() = %+v, want %+v", got, second)
	}
}

func TestEndpoint_SubscribeAfterGeneration1DeliversCurrentOnce(t *testing.T) {
	e := NewEndpoint()
	published := e.Publish("localhost", 39429, "token-1", 111)

	var received []EndpointIdentity
	e.Subscribe(func(id EndpointIdentity) {
		received = append(received, id)
	})

	if len(received) != 1 {
		t.Fatalf("received %d calls on Subscribe, want 1", len(received))
	}
	if received[0] != published {
		t.Errorf("delivered %+v, want the current identity %+v", received[0], published)
	}
}

func TestEndpoint_SubscribeBeforeAnyPublishDeliversNothingUntilPublish(t *testing.T) {
	e := NewEndpoint()

	var received []EndpointIdentity
	e.Subscribe(func(id EndpointIdentity) {
		received = append(received, id)
	})
	if len(received) != 0 {
		t.Fatalf("received %d calls before any publish, want 0", len(received))
	}

	published := e.Publish("localhost", 39429, "token-1", 111)
	if len(received) != 1 || received[0] != published {
		t.Errorf("received = %+v, want exactly one call with %+v", received, published)
	}
}

func TestEndpoint_ObserversCalledInRegistrationOrderOnEachPublish(t *testing.T) {
	e := NewEndpoint()
	e.Publish("localhost", 39429, "token-1", 111)

	var order []int
	e.Subscribe(func(EndpointIdentity) { order = append(order, 1) })
	e.Subscribe(func(EndpointIdentity) { order = append(order, 2) })

	// Both observers replay once on Subscribe (order 1, then 2), then again on
	// the next publish.
	e.Publish("localhost", 40001, "token-2", 222)

	want := []int{1, 2, 1, 2}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %d, want %d", i, order[i], want[i])
		}
	}
}

func TestEndpoint_CurrentNeverMixesGenerationsUnderRace(t *testing.T) {
	e := NewEndpoint()
	e.Publish("localhost", 39429, "token-0", 0)

	const publishers = 4
	const readers = 8
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(publishers + readers)

	for p := 0; p < publishers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				e.Publish("localhost", 40000+p, "token", p*1000+i)
			}
		}(p)
	}

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := e.Current()
				// A torn read would show a Port/PID pair that no single Publish
				// call ever produced. We can't assert exact correlation without
				// serializing, but reading the struct value must never panic or
				// race -- the race detector is the actual assertion here.
				_ = id.Generation
			}
		}()
	}

	wg.Wait()
}

func TestEndpoint_SubscribeAndPublishSerializeUnderRace(t *testing.T) {
	e := NewEndpoint()

	var mu sync.Mutex
	seen := make(map[uint64]int)

	const subscribers = 6
	var wg sync.WaitGroup
	wg.Add(subscribers + 1)

	for s := 0; s < subscribers; s++ {
		go func() {
			defer wg.Done()
			e.Subscribe(func(id EndpointIdentity) {
				mu.Lock()
				seen[id.Generation]++
				mu.Unlock()
			})
		}()
	}

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			e.Publish("localhost", 39429, "token", i)
		}
	}()

	wg.Wait()
}
