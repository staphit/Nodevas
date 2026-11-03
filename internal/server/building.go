package server

import (
	"sync"
	"time"

	"nodevas/internal/realtime"
)

// BuildingChangedEvent tells Station's building, in every project the client
// may read, that one project's runs, autopilots or agents changed and the
// building is worth refetching. It names nothing: the id is always empty.
const BuildingChangedEvent = "building-changed"

// buildingPingEvery is the most often one project pings the building.
const buildingPingEvery = 2 * time.Second

// buildingPings throttles pingBuilding per project root: the first change
// pings at once, changes within the interval after it fold into one
// trailing ping at its end.
type buildingPings struct {
	mu      sync.Mutex
	every   time.Duration
	last    map[string]time.Time
	pending map[string]*time.Timer
}

func newBuildingPings(every time.Duration) *buildingPings {
	return &buildingPings{every: every, last: map[string]time.Time{}, pending: map[string]*time.Timer{}}
}

// ping calls send(root) now, or once at the end of the current interval if
// root pinged within it.
func (p *buildingPings) ping(root string, send func(string)) {
	p.mu.Lock()
	if p.pending[root] != nil {
		p.mu.Unlock()
		return // a trailing ping is already due
	}
	now := time.Now()
	wait := p.every - now.Sub(p.last[root])
	if wait <= 0 {
		p.last[root] = now
		p.mu.Unlock()
		send(root)
		return
	}
	p.pending[root] = time.AfterFunc(wait, func() {
		p.mu.Lock()
		delete(p.pending, root)
		p.last[root] = time.Now()
		p.mu.Unlock()
		send(root)
	})
	p.mu.Unlock()
}

// pingBuilding tells readers of the project at root that its part of the
// building changed (throttled; see buildingPings).
func (s *Server) pingBuilding(root string) {
	hub := s.hub
	if hub == nil || root == "" {
		return
	}
	s.buildingOnce.Do(func() {
		if s.buildingPings == nil {
			s.buildingPings = newBuildingPings(buildingPingEvery)
		}
	})
	s.buildingPings.ping(root, func(room string) { broadcastBuilding(hub, room) })
}

func broadcastBuilding(hub *realtime.Hub, room string) {
	hub.BroadcastReaders(room, BuildingChangedEvent, "")
}
