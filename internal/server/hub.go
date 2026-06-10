package server

import "strings"

// subscriberBuffer bounds how many commit event-groups a slow subscriber may lag
// before the hub drops groups for it (drop-and-reconcile: the client recovers the
// gap from the durable log via the cursor).
const subscriberBuffer = 64

// subscriber is one live watch connection registered with a repo's hub.
type subscriber struct {
	principalID string
	pathPrefix  string          // watched path; events under it match (hierarchical)
	eventTypes  map[string]bool // nil = all types
	out         chan []EventJSON
}

// matches reports whether an event type passes this subscriber's type filter.
func (s *subscriber) wantsType(t string) bool {
	if s.eventTypes == nil {
		return true
	}
	return s.eventTypes[t]
}

// hub is the in-memory event router for a single repo. One run goroutine owns the
// subscriber registry; register/unregister/publish all arrive over channels, so
// the registry needs no mutex.
type hub struct {
	register   chan *subscriber
	unregister chan *subscriber
	publish    chan []EventJSON // one commit's full event group
	stop       chan struct{}

	// canRead evaluates a principal's CURRENT read permission on a path (live
	// permission filtering). Backed by the repo store, so a revoke takes effect
	// immediately without reconnecting.
	canRead func(principalID, path string) bool
}

func newHub(canRead func(principalID, path string) bool) *hub {
	return &hub{
		register:   make(chan *subscriber),
		unregister: make(chan *subscriber),
		publish:    make(chan []EventJSON, 256),
		stop:       make(chan struct{}),
		canRead:    canRead,
	}
}

// run owns the subscriber registry until stopped.
func (h *hub) run() {
	subs := make(map[*subscriber]struct{})
	for {
		select {
		case s := <-h.register:
			subs[s] = struct{}{}
		case s := <-h.unregister:
			if _, ok := subs[s]; ok {
				delete(subs, s)
				close(s.out)
			}
		case group := <-h.publish:
			for s := range subs {
				filtered := h.filterGroup(s, group)
				if len(filtered) == 0 {
					continue
				}
				// Non-blocking send: if the subscriber is too slow and its buffer
				// is full, drop this whole group for it. The client detects the
				// resulting seq gap and backfills from the durable log.
				select {
				case s.out <- filtered:
				default:
				}
			}
		case <-h.stop:
			for s := range subs {
				delete(subs, s)
				close(s.out)
			}
			return
		}
	}
}

// filterGroup returns the events of a commit's group visible to a subscriber:
// path-prefix ∩ event-type ∩ current read permission.
func (h *hub) filterGroup(s *subscriber, group []EventJSON) []EventJSON {
	var out []EventJSON
	for _, e := range group {
		switch e.Type {
		case EventCommitPushed:
			if !s.wantsType(e.Type) {
				continue
			}
			var paths []string
			for _, p := range e.Paths {
				if underPath(s.pathPrefix, p) && h.canRead(s.principalID, p) {
					paths = append(paths, p)
				}
			}
			if len(paths) == 0 {
				continue
			}
			ce := e
			ce.Paths = paths
			out = append(out, ce)
		default: // file.*
			if !s.wantsType(e.Type) {
				continue
			}
			if underPath(s.pathPrefix, e.Path) && h.canRead(s.principalID, e.Path) {
				out = append(out, e)
			}
		}
	}
	return out
}

// Publish hands a commit's full event group to the hub (non-blocking on the
// caller side via the buffered publish channel). Called after the SQLite commit.
func (h *hub) Publish(group []EventJSON) {
	select {
	case h.publish <- group:
	case <-h.stop:
	}
}

// Subscribe registers a subscriber and returns it.
func (h *hub) Subscribe(s *subscriber) {
	select {
	case h.register <- s:
	case <-h.stop:
	}
}

// Unsubscribe removes a subscriber.
func (h *hub) Unsubscribe(s *subscriber) {
	select {
	case h.unregister <- s:
	case <-h.stop:
	}
}

// Stop shuts the hub down and closes all subscriber channels.
func (h *hub) Stop() {
	close(h.stop)
}

// underPath reports whether concrete path p is at or beneath the watched prefix.
// A watch on "/strategy" matches "/strategy" and everything under "/strategy/".
// A watch on "/" (or "") matches everything.
func underPath(prefix, p string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
