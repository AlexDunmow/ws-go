package ws

import (
	"fmt"

	gws "github.com/gobwas/ws"
	"github.com/sasha-s/go-deadlock"
)

type pool struct {
	connections *[10]*Client
	sender      chan *message
	inserter    chan *insertClient
	remover     chan func() bool
	onClose     func() bool
	is_full     bool
	URI         string
}

type insertClient struct {
	c   *Client
	pls *pools
	uri string
}

func (cpool *pool) insert(c *insertClient) {
	cpool.inserter <- c
}

func (cpool *pool) close() bool {
	return cpool.onClose()
}

var ErrPoolFull = fmt.Errorf("pool is full")

func (cpool *pool) send(m *message) {
	select {
	case cpool.sender <- m:
		return
	default:
		log.Error().Msg("unable to send to pool as sender is full")
	}
}

func (cpool *pool) _insert(c *Client) error {
	var i int
	is_full := true

	// don't subscribe again if client already sub on the uri
	if c.onCloseMap.exists(cpool.URI) {
		return nil
	}

	for i, _ = range cpool.connections {
		if cpool.connections[i] == nil {
			is_full = false
			break
		}
	}
	if is_full {
		cpool.is_full = true
		return ErrPoolFull
	}

	// add close func into client onclose map
	c.onCloseMap.add(cpool.URI, func() {
		cpool.remover <- func() bool {
			// remove client from pool
			cpool.connections[i] = nil
			cpool.is_full = false

			return cpool.close()
		}
	})
	cpool.connections[i] = c
	return nil
}

func (cpool *pool) run() {
	defer func() {
		if r := recover(); r != nil {
			logPanicRecovery("panic on pool run", r)
		}
	}()
	for {
		select {
		case m := <-cpool.sender:
			for _, cl := range cpool.connections {
				if cl != nil {
					go cl.send(m)
				}
			}
		case ic := <-cpool.inserter:
			err := cpool._insert(ic.c)
			if err != nil {

				func(ic *insertClient) {
					ic.pls.Lock()
					defer ic.pls.Unlock()
					// create a new pool, if current pool is the last pool
					if ic.pls.lastPool == cpool {
						ic.pls.newPool(ic.uri)
					}
				}(ic)

				// wrap pool insert into go routine to release current channel
				go ic.pls.lastPool.insert(ic)
			}

		case fn := <-cpool.remover:
			// close go routine, if last client is removed
			if fn() {
				return
			}
		}
	}
}

type pools struct {
	p        []*pool
	lastPool *pool
	deadlock.RWMutex
}

//newPool creates a new pool and points lastPool at it
// NOTE: this func is wrapped
func (pls *pools) newPool(uri string) *pool {
	p := &pool{
		connections: &[10]*Client{},
		sender:      make(chan *message, 30),
		inserter:    make(chan *insertClient),
		remover:     make(chan func() bool),
		URI:         uri,
	}
	p.onClose = func() bool {
		pls.Lock()
		defer pls.Unlock()

		// check any client still in the pool
		for x, _ := range p.connections {
			if p.connections[x] != nil {
				return false
			}
		}

		// do not close the pool, if it is the last pool
		if pls.lastPool == p {
			return false
		}

		// otherwise, remove the pool from the list
		for i, v := range pls.p {
			if v == p {
				pls.p = append(pls.p[:i], pls.p[i+1:]...)
				break
			}
		}

		return true
	}
	pls.p = append(pls.p, p)
	pls.lastPool = p

	go p.run()
	return p
}

// register finds or creates an empty pool and inserts a client into it
func (pls *pools) register(uri string, c *Client) {
	func() {
		pls.Lock()
		defer pls.Unlock()
		if pls.lastPool == nil {
			pls.newPool(uri)
		}
	}()
	pls.lastPool.insert(&insertClient{c, pls, uri})
}

func (pls *pools) publishBytes(key string, b []byte) {
	m := &message{op: gws.OpBinary, key: key, msg: b}
	for _, p := range pls.p {
		go p.send(m)
	}
}

func (pls *pools) publish(key string, b []byte) {
	m := &message{op: gws.OpText, key: key, msg: b}
	for _, pl := range pls.p {
		go pl.send(m)
	}
}
