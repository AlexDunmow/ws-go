package ws

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	gws "github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/gofrs/uuid"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/atomic"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type Client struct {
	trackKey   string // for connection tracking
	path       string
	conn       net.Conn
	sendQueue  chan *message
	close      chan struct{}
	server     *Server
	prefix     string
	root       string
	SessionID  string
	onCloseMap *funcMap
	isClosed   atomic.Bool
}

type funcMap struct {
	m map[string]func()
	deadlock.Mutex
}

// check client already subscribe on the url
func (fm *funcMap) exists(key string) bool {
	fm.Lock()
	defer fm.Unlock()
	_, ok := fm.m[key]
	return ok
}

func (fm *funcMap) add(key string, fn func()) {
	fm.Lock()
	defer fm.Unlock()
	fm.m[key] = fn
}

func (fm *funcMap) fireAndRemove(keys ...string) {
	fm.Lock()
	defer fm.Unlock()
	for _, key := range keys {
		if fn, ok := fm.m[key]; ok {
			fn()
			delete(fm.m, key)
		}
	}
}

func (fm *funcMap) fireAndRemoveAll() {
	fm.Lock()
	defer fm.Unlock()
	for key, fn := range fm.m {
		fn()
		delete(fm.m, key)
	}
}

func C(server *Server, prefix string, w http.ResponseWriter, r *http.Request, root string) (*Client, error) {
	conn, _, _, err := gws.UpgradeHTTP(r, w)
	if err != nil {
		return nil, err
	}

	err = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return nil, err
	}

	cl := &Client{
		path:      r.URL.Path,
		conn:      conn,
		sendQueue: make(chan *message, 1000),
		close:     make(chan struct{}),
		server:    server,
		prefix:    prefix,
		root:      root,
		SessionID: uuid.Must(uuid.NewV4()).String(), // track client
		onCloseMap: &funcMap{
			m: make(map[string]func()),
		},
	}

	return cl, nil
}

func (cl *Client) send(m *message) {
	defer func() {
		if r := recover(); r != nil {
			logPanicRecovery("panic recovered", r)
			if cl != nil {
				cl.onCloseMap.fireAndRemoveAll()
			}
		}
	}()
	if cl == nil || cl.isClosed.Load() {
		return
	}

	select {
	case cl.sendQueue <- m:
	default:
		select {
		case cl.close <- struct{}{}:
			log.Warn().Msgf("Send queue is full, closing client %s", cl.root)
		default:
			// exit, if the client is already closed
		}
	}
}

func (cl *Client) Send(op gws.OpCode, key string, msg []byte) {
	cl.send(&message{op, key, msg})
}

func (cl *Client) MarshalSend(key string, msg interface{}) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	cl.send(&message{gws.OpText, key, b})
	return nil
}

func (cl *Client) SendBinary(key string, msg []byte) {
	cl.send(&message{gws.OpBinary, key, msg})
}

func (cl *Client) SendText(key string, msg string) {
	cl.send(&message{gws.OpText, key, []byte(msg)})
}

func (cl *Client) Run(r *http.Request) {
	if cl.sendQueue == nil {
		panic("client has not been initialised correctly")
	}

	defer func() {
		trackKey := cl.trackKey

		cl.isClosed.Store(true)
		cl.onCloseMap.fireAndRemoveAll()

		err := cl.conn.Close()
		if err != nil {
			log.Warn().Err(err).Msg("problem closing ws connection")
		}

		// drop client from track map
		trackMap.dropClient(cl)

		if trackKey != "" {
			select {
			case ClientDisconnectedChan <- trackKey:
			case <-time.After(3 * time.Second): // timeout
			}
		}
	}()

	cctx := chi.RouteContext(r.Context())
	attachedToken := r.URL.Query().Get("token")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logPanicRecovery("panic recovered", r)
			}
			select {
			case cl.close <- struct{}{}:
			default:
			}
		}()
		for {
			msg, _, err := wsutil.ReadClientData(cl.conn)
			if err != nil {
				log.Debug().Err(err).Str("path", r.URL.Path).Msg("Read client data error")
				return
			}
			s := string(msg)
			if strings.HasPrefix(s, "SUBSCRIBE:") {
				uri := strings.Replace(s, "SUBSCRIBE:", "", 1)
				req, err := http.NewRequest(http.MethodConnect, strings.TrimPrefix(uri, cl.root), nil)
				if err != nil {
					log.Error().Err(err).Str("targeted uri", uri).Msg("Failed to generate request for subscribe uri")
					continue
				}
				req.Header = r.Header
				req.RemoteAddr = r.RemoteAddr

				ctx := req.Context()
				w := NewCustomResponseWriter(cl)
				rctx := chi.NewRouteContext()
				rctx.Routes = cctx.Routes
				rctx.RouteMethod = http.MethodConnect
				rctx.URLParams = cctx.URLParams

				for i, key := range cctx.URLParams.Keys {
					ctx = context.WithValue(ctx, key, cctx.URLParams.Values[i])
				}
				ctx = context.WithValue(ctx, "request-uri", uri)
				ctx = context.WithValue(ctx, "client-session-id", cl.SessionID)
				if attachedToken != "" {
					ctx = context.WithValue(ctx, "token", attachedToken)
				}

				rctx.RoutePath = strings.TrimPrefix(uri, cl.root)
				req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

				cl.server.ServeHTTP(w, req)
				continue
			} else if strings.HasPrefix(s, "UNSUBSCRIBE:") {
				// unsubscribe a specific cpool
				uri := strings.Replace(s, "UNSUBSCRIBE:", "", 1)
				cl.onCloseMap.fireAndRemove(uri)
				continue
			}
			log.Debug().Bytes("msg", msg).Msg("invalid message")

			cl.Send(gws.OpText, "", []byte("exiting"))
			return
		}
	}()

	go func() {
		for {
			m := <-cl.sendQueue

			err := cl.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err != nil {
				log.Debug().Err(err).Msg("Set write deadline error.")
				return
			}

			doTrace := os.Getenv("PASSPORT_ENVIRONMENT") == "production" || os.Getenv("GAMESERVER_ENVIRONMENT") == "production"

			span, _ := tracer.StartSpanFromContext(
				context.Background(),
				"write_server_message",
				tracer.ResourceName(m.key),
				tracer.Tag("env", "production"),
				tracer.Tag("len", len(m.msg)),
			)

			err = wsutil.WriteServerMessage(cl.conn, m.op, m.msg)
			if err != nil {
				log.Debug().Err(err).Msg("Failed to send message.")
				if doTrace {
					span.Finish()
				}
				return
			}
			if doTrace {
				span.Finish()
			}
		}
	}()

	<-cl.close
}

type WSResponseWriter struct {
	client *Client
	header http.Header
}

func NewCustomResponseWriter(client *Client) *WSResponseWriter {
	return &WSResponseWriter{
		client,
		http.Header{},
	}
}

func (w *WSResponseWriter) Header() http.Header {
	return w.header
}

func (w *WSResponseWriter) Write(b []byte) (int, error) {
	// implement it as per your requirement
	if len(b) == 0 {
		return 0, nil
	}

	opType := gws.OpText

	if b[0] == BinaryReplyLeadingByte {
		opType = gws.OpBinary
		b = b[1:]
	}

	w.client.Send(opType, "", b)
	return 0, nil
}

func (w *WSResponseWriter) WriteHeader(statusCode int) {
}
