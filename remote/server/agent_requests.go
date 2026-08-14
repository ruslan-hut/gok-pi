package server

import (
	"fmt"
	"sync"
	"time"
)

// pendingRequests correlates server→agent requests with the agent's replies.
// Both request/response exchanges the server makes (logs, diagnostics) have the
// same shape: send a payload carrying a request ID, wait for the reply that
// quotes it, give up after a timeout. Only the response type differs.
type pendingRequests[T any] struct {
	mu sync.Mutex
	m  map[string]chan T
}

func newPendingRequests[T any]() *pendingRequests[T] {
	return &pendingRequests[T]{m: make(map[string]chan T)}
}

func (p *pendingRequests[T]) add(id string) chan T {
	ch := make(chan T, 1)
	p.mu.Lock()
	p.m[id] = ch
	p.mu.Unlock()
	return ch
}

// abandon removes a waiter and closes its channel. Closing is safe because
// resolve only ever sends to a channel it has already removed from the map.
func (p *pendingRequests[T]) abandon(id string, ch chan T) {
	p.mu.Lock()
	if current, ok := p.m[id]; ok && current == ch {
		delete(p.m, id)
		close(ch)
	}
	p.mu.Unlock()
}

func (p *pendingRequests[T]) resolve(id string, value T) {
	p.mu.Lock()
	ch, ok := p.m[id]
	if ok {
		delete(p.m, id)
	}
	p.mu.Unlock()

	if !ok {
		return
	}
	select {
	case ch <- value:
	default:
	}
	close(ch)
}

// awaitAgentResponse sends payload to the agent and blocks until the matching
// reply arrives, the timeout expires, or the connection drops. The waiter is
// registered before the send so a fast agent cannot answer before anyone is
// listening.
func awaitAgentResponse[T any](
	a *agentConnection,
	pending *pendingRequests[T],
	requestID string,
	payload interface{},
	timeout time.Duration,
	what string,
) (T, error) {
	var zero T
	ch := pending.add(requestID)

	select {
	case <-a.done:
		pending.abandon(requestID, ch)
		return zero, fmt.Errorf("agent connection closed")
	case a.send <- payload:
	default:
		pending.abandon(requestID, ch)
		return zero, fmt.Errorf("agent send buffer full")
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		pending.abandon(requestID, ch)
		return zero, fmt.Errorf("%s request timed out", what)
	case <-a.done:
		pending.abandon(requestID, ch)
		return zero, fmt.Errorf("agent connection closed")
	}
}
