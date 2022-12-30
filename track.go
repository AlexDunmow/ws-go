package ws

import (
	"github.com/sasha-s/go-deadlock"
)

var trackMap *TrackMap

type TrackMap struct {
	m map[string]map[*Client]bool
	deadlock.RWMutex
}

func (tm *TrackMap) trackClient(key string, c *Client) {
	if key == "" {
		return
	}

	tm.Lock()
	defer tm.Unlock()

	// set client key
	c.trackKey = key

	// add client to the track map
	m, ok := tm.m[key]
	if !ok {
		m = make(map[*Client]bool)
		tm.m[key] = m
	}

	if _, ok := m[c]; !ok {
		m[c] = true
	}
}

func (tm *TrackMap) dropClient(c *Client) {
	tm.Lock()
	defer tm.Unlock()
	key := c.trackKey
	if key == "" {
		return
	}

	m, ok := tm.m[key]
	if ok {
		_, ok = m[c]
		if ok {
			delete(m, c)
		}

		if len(m) == 0 {
			delete(tm.m, key)
		}
	}
}

func IsConnected(key string) bool {
	trackMap.RLock()
	defer trackMap.RUnlock()

	m, ok := trackMap.m[key]
	if !ok || len(m) == 0 {
		return false
	}

	return true
}

func ShutConnections(key string) {
	trackMap.Lock()
	defer trackMap.Unlock()
	if m, ok := trackMap.m[key]; ok {
		for c := range m {
			delete(m, c)
			go func(c *Client) {
				if c != nil {
					select {
					case c.close <- struct{}{}:
					default:
					}
				}
			}(c)
		}
		delete(trackMap.m, key)
	}
}

func ShutClientPool(key string, uri ...string) {
	if len(uri) == 0 {
		return
	}

	trackMap.Lock()
	defer trackMap.Unlock()

	if m, ok := trackMap.m[key]; ok {
		wg := deadlock.WaitGroup{}
		for c := range m {
			wg.Add(1)
			go func(c *Client) {
				if c != nil {
					c.onCloseMap.fireAndRemove(uri...)
				}
				wg.Done()
			}(c)
		}
		wg.Wait()
	}
}

func TrackedIdents() []string {
	var idents []string

	trackMap.RLock()
	defer trackMap.RUnlock()

	for ident := range trackMap.m {
		idents = append(idents, ident)
	}

	return idents
}
