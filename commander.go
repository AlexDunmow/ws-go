package ws

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/kevinms/leakybucket-go"
	"github.com/ninja-software/terror/v2"
	"github.com/valyala/fastjson"
	"go.uber.org/atomic"
	"io"
	"net"
	"net/http"
	"strings"
)

type ReplyFunc func(interface{})
type CommandFunc func(ctx context.Context, key string, payload []byte, reply ReplyFunc) error
type SecureFunc func(ctx context.Context) bool

// Commander accepts message handlers based on a JSON key
type Commander struct {
	commands       *Tree[CommandFunc]
	maxRequestSize int64
	bucket         *leakybucket.Collector
	*chi.Mux
}

type CommanderOpts struct {
	MessagePerSecond float64
	Capacity         int64
}

func NewCommander(fn func(c *Commander), opts ...*CommanderOpts) *Commander {
	messagePerSecond := float64(20)
	capacity := int64(20)

	if len(opts) > 0 {
		messagePerSecond = opts[0].MessagePerSecond
		capacity = opts[0].Capacity
	}

	c := &Commander{NewTree[CommandFunc](), 1024 * 4, leakybucket.NewCollector(messagePerSecond, capacity, true), chi.NewRouter()}
	fn(c)
	return c
}

func (c *Commander) MaxRequestSize(n int64) {
	c.maxRequestSize = n
}

func (c *Commander) RestBridge(pattern string) {
	c.Post(pattern, c.bridge)
}

func (c *Commander) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.ServeWS(w, r)
}

func (c *Commander) bridge(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, c.maxRequestSize)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
	}

	key := fastjson.GetString(b, "key")
	txid := fastjson.GetString(b, "txid")
	prefix, fn, _, ok := c.commands.LongestPrefix(key)
	if ok {
		reply := func(payload interface{}) {
			resp := struct {
				Key           string      `json:"key"`
				TransactionID string      `json:"transactionId"`
				Success       bool        `json:"success"`
				Payload       interface{} `json:"payload"`
			}{
				Key:           key,
				TransactionID: txid,
				Success:       true,
				Payload:       payload,
			}
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				http.Error(w, "server failed to encode response", http.StatusInternalServerError)
				return
			}
		}
		err = fn(r.Context(), prefix, b, reply)
		if err != nil {
			// log error on console
			log.Debug().Err(err).Msgf("Error on handler: %s", key)
			resp := struct {
				Key           string `json:"key"`
				TransactionID string `json:"transactionId"`
				Error         string `json:"error"`
			}{
				Key:           "ERROR",
				TransactionID: txid,
				Error:         err.Error(),
			}
			if terr, ok := err.(*terror.TError); ok && terr.Message != "" {
				resp.Error = terr.Message
			}
			if err := json.NewEncoder(w).Encode(&resp); err != nil {
				http.Error(w, "server failed to encode response", http.StatusInternalServerError)
				return
			}
		}
	}

}

func (c *Commander) Command(key string, fn CommandFunc) {
	c.commands.Insert(key, fn)
}

func (c *Commander) ServeWS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}

	wsn := wsutil.NewWriter(conn, ws.StateServerSide, ws.OpText)
	j := json.NewEncoder(wsn)

	// get ip
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ipaddr, _, _ := net.SplitHostPort(r.RemoteAddr)
		userIP := net.ParseIP(ipaddr)
		if userIP == nil {
			ip = ipaddr
		} else {
			ip = userIP.String()
		}
	}

	defer conn.Close()
	shouldClose := atomic.NewBool(false)

	for {
		if shouldClose.Load() {
			return
		}
		msg, _, err := wsutil.ReadClientData(conn)
		if err != nil {
			return
		}

		if !skipRateLimit {
			// rate limiting the commands
			count := c.bucket.Add(ip, 1)
			if count == 0 {
				continue
			}
		}

		key := fastjson.GetString(msg, "key")
		txid := fastjson.GetString(msg, "transactionId")
		uri := r.URL.Path
		wsPrefix, hasWSPrefix := ctx.Value("ws_prefix").(string)
		if hasWSPrefix && wsPrefix != "" {
			uri = strings.TrimPrefix(uri, wsPrefix)
		}

		prefix, fn, _, ok := c.commands.LongestPrefix(key)
		if ok {
			reply := func(payload interface{}) {
				if txid == "" {
					//this could do with a warning
					return
				}

				resp := struct {
					Key           string      `json:"key"`
					TransactionID string      `json:"transactionId"`
					Payload       interface{} `json:"payload"`
					Uri           string      `json:"uri"`
				}{
					Key:           key,
					TransactionID: txid,
					Payload:       payload,
					Uri:           uri,
				}
				if err := j.Encode(&resp); err != nil {
					return
				}
				if err = wsn.Flush(); err != nil {
					shouldClose.Store(true)
					return
				}
			}

			go func() {
				defer func() {
					if r := recover(); r != nil {
						logPanicRecovery("panic recovered", r)
					}
				}()

				err = fn(ctx, prefix, msg, reply)
				if err != nil {
					// log error on console
					log.Debug().Err(err).Msgf("Error on handler: %s", key)
					resp := struct {
						Key           string `json:"key"`
						TransactionID string `json:"transactionId"`
						Error         string `json:"error"`
						Uri           string `json:"uri"`
					}{
						Key:           "ERROR",
						TransactionID: txid,
						Error:         err.Error(),
						Uri:           uri,
					}

					if terr, ok := err.(*terror.TError); ok && terr.Message != "" {
						resp.Error = terr.Message
					}

					if err = j.Encode(&resp); err != nil {
						return
					}
					if err = wsn.Flush(); err != nil {
						shouldClose.Store(true)
						return
					}
				}
			}()

		}
	}
}
