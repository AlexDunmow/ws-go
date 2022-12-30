package ws

import (
	"encoding/json"

	gws "github.com/gobwas/ws"
	leakybucket "github.com/kevinms/leakybucket-go"
	"github.com/rs/zerolog"
	"github.com/sasha-s/go-deadlock"
)

var (
	subscribers *Tree[*pools]
	once        deadlock.Once
)

var log *zerolog.Logger
var skipRateLimit bool

func sub(URI string, c *Client) {
	pl, ok := subscribers.Get(URI)
	if !ok {
		pl = &pools{
			p: make([]*pool, 0),
		}
		subscribers.Insert(URI, pl)
	}
	pl.register(URI, c)
}

type message struct {
	op  gws.OpCode
	key string
	msg []byte
}

func PublishBytes(URI string, binaryDateTypeKey byte, v interface{}) {
	var err error
	b, ok := v.([]byte)
	if !ok {
		b, err = json.Marshal(v)
		if err != nil {
			log.Debug().Str("uri", URI).Msg("Failed to marshal struct")
			return
		}
	}

	ps, ok := subscribers.Get(URI)
	if !ok {
		return
	}
	ps.publishBytes(URI, append([]byte{binaryDateTypeKey}, b...))
}

type Message struct {
	URI     string      `json:"uri"`
	Key     string      `json:"key"`
	Payload interface{} `json:"payload"`
}

type BatchMessage struct {
	BatchURI    string    `json:"batchURI"`
	SubMessages []Message `json:"subMessages"`
}

func PublishMessageDomain(domain string, URI string, key string, v interface{}) {
	b, _ := json.Marshal(&Message{
		URI:     URI,
		Key:     key,
		Payload: v,
	})
	key, ps, ok := subscribers.DomainGet(URI, domain)
	if !ok {
		return
	}
	ps.publish(key, b)
}

var streamBucket = leakybucket.NewCollector(100, 1, true)

func StreamMessage(URI string, key string, v interface{}) {
	bucket := streamBucket.Add(URI+key, 1)
	if bucket == 0 {
		return
	}
	b, _ := json.Marshal(&Message{
		URI:     URI,
		Key:     key,
		Payload: v,
	})

	_, ps, _, ok := subscribers.LongestPrefix(URI)
	if !ok {
		return
	}
	ps.publish(key, b)
}

func PublishMessage(URI string, key string, v interface{}) {

	b, _ := json.Marshal(&Message{
		URI:     URI,
		Key:     key,
		Payload: v,
	})

	ps, ok := subscribers.Get(URI)
	if !ok {
		return
	}

	ps.publish(key, b)
}

func Publish(URI string, b []byte) {
	ps, ok := subscribers.Get(URI)
	if !ok {
		return
	}
	ps.publish(URI, b)
}

type Config struct {
	Logger        *zerolog.Logger
	SkipRateLimit bool
}

var ClientDisconnectedChan chan string

func Init(conf *Config) {
	log = conf.Logger
	skipRateLimit = conf.SkipRateLimit
	trackMap = &TrackMap{
		m: make(map[string]map[*Client]bool),
	}

	ClientDisconnectedChan = make(chan string)
}
