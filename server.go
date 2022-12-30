package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	gws "github.com/gobwas/ws"
	"github.com/ninja-software/terror/v2"
)

type Server struct {
	chi.Router
}

func NewServer(fn func(s *Server)) *Server {
	return newServer(fn, chi.NewRouter())
}

func newServer(fn func(s *Server), r chi.Router) *Server {
	once.Do(func() {
		subscribers = NewTree[*pools]()
	})
	s := &Server{
		Router: r,
	}
	fn(s)

	s.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				logPanicRecovery("panic recovered", r)
			}
		}()

		if r.Method != http.MethodGet {
			log.Error().Str("path", r.URL.Path).Str("expected method", http.MethodGet).Str("provided method", r.Method).Msg("incorrect method")
			return
		}

		err := s.client(w, r)
		if err != nil {
			log.Error().Err(err).Msg("unable to create client")
			return
		}
	})

	return s
}

func (srv *Server) client(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cctx := chi.RouteContext(ctx)
	rpattern := strings.TrimSuffix(cctx.RoutePattern(), "/*")

	for _, key := range cctx.URLParams.Keys {
		val := cctx.URLParam(key)
		rpattern = strings.Replace(rpattern, "{"+key+"}", val, 1)
	}

	prefix, hasPrefix := r.Context().Value("ws_prefix").(string)
	if hasPrefix {
		rpattern = strings.TrimPrefix(rpattern, prefix)
	}

	client, err := C(srv, prefix, w, r, rpattern)
	if err != nil {
		return err
	}

	sub(rpattern, client)

	client.Send(gws.OpText, "", []byte("ROOT:"+rpattern))
	client.Run(r)

	return nil
}

func TrimPrefix(prefix string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), "ws_prefix", prefix)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (srv *Server) WS(pattern string, key string, handler CommandFunc, secureFn ...SecureFunc) *Server {
	pattern = strings.TrimSuffix(pattern, "*")
	srv.Router.Connect(pattern, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				logPanicRecovery("panic recovered", r)
			}
		}()

		ctx := r.Context()
		requestUri, ok := ctx.Value("request-uri").(string)
		if !ok {
			log.Warn().Str("pattern", pattern).Msg("Missing request uri")
			return
		}

		if len(secureFn) > 0 {
			for _, secFn := range secureFn {
				if !secFn(ctx) {
					_, err := w.Write([]byte(`subscription failed: ` + requestUri))
					if err != nil {
						log.Debug().Err(err).Msgf("client write failed", key)
					}
					return
				}
			}
		}

		// sub client to the tree
		if wsww, ok := w.(*WSResponseWriter); ok {
			sub(requestUri, wsww.client)
		}

		// skip, if handler is not provided
		if handler == nil {
			return
		}

		reply := func(payload interface{}) {
			resp := struct {
				Key     string      `json:"key"`
				Uri     string      `json:"uri"`
				Payload interface{} `json:"payload"`
			}{
				Key:     key,
				Uri:     requestUri,
				Payload: payload,
			}
			b, err := json.Marshal(resp)
			if err != nil {
				log.Debug().Err(err).Msgf("marshalling json in reply for %s has failed", key)
				return
			}

			_, err = w.Write(b)
			if err != nil {
				log.Debug().Err(err).Msgf("client write failed", key)
			}
		}

		err := handler(ctx, key, []byte{}, reply)
		if err != nil {
			if terr, ok := err.(*terror.TError); ok {
				log.Debug().Str("key", key).Err(terr).Msg(terr.Message)
				return
			}
			log.Debug().Str("key", key).Err(err).Msg("OnConnect action has failed")
		}

	})
	return srv
}

const BinaryReplyLeadingByte byte = 0

func (srv *Server) WSBinary(pattern string, handler CommandFunc, secureFn ...SecureFunc) *Server {
	pattern = strings.TrimSuffix(pattern, "*")
	srv.Router.Connect(pattern, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				logPanicRecovery("panic recovered", r)
			}
		}()

		ctx := r.Context()
		requestUri, ok := ctx.Value("request-uri").(string)
		if !ok {
			log.Warn().Str("pattern", pattern).Msg("Missing request uri")
			return
		}

		if len(secureFn) > 0 {
			for _, secFn := range secureFn {
				if !secFn(ctx) {
					_, err := w.Write([]byte(`subscription failed: ` + requestUri))
					if err != nil {
						log.Debug().Err(err).Msgf("secure function fail", pattern)
					}
					return
				}
			}
		}

		// sub client to the tree
		if wsww, ok := w.(*WSResponseWriter); ok {
			sub(requestUri, wsww.client)
		}

		// skip, if handler is not provided
		if handler == nil {
			return
		}

		reply := func(payload interface{}) {
			b, ok := payload.([]byte)
			if !ok {
				log.Debug().Msgf("binary data only!!!", pattern)
				return
			}

			_, err := w.Write(append([]byte{BinaryReplyLeadingByte}, b...))
			if err != nil {
				log.Debug().Err(err).Msgf("client write failed:", pattern)
			}
		}

		err := handler(ctx, "", []byte{}, reply)
		if err != nil {
			if terr, ok := err.(*terror.TError); ok {
				log.Debug().Str("pattern", pattern).Err(terr).Msg(terr.Message)
				return
			}
			log.Debug().Str("pattern", pattern).Err(err).Msg("OnConnect action has failed")
		}

	})
	return srv
}

func (srv *Server) WSTrack(pattern string, trackKey string, key string, handler CommandFunc, secureFn ...SecureFunc) *Server {
	pattern = strings.TrimSuffix(pattern, "*")
	srv.Router.Connect(pattern, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				logPanicRecovery("panic recovered", r)
			}
		}()

		ctx := r.Context()
		requestUri, ok := ctx.Value("request-uri").(string)
		if !ok {
			log.Warn().Str("pattern", pattern).Msg("Missing request uri")
			return
		}

		if len(secureFn) > 0 {
			for _, secFn := range secureFn {
				if !secFn(ctx) {
					_, err := w.Write([]byte(`subscription failed: ` + requestUri))
					if err != nil {
						log.Debug().Err(err).Msgf("client write failed", key)
					}
					return
				}
			}
		}

		var client *Client
		// sub client to the tree
		if wsww, ok := w.(*WSResponseWriter); ok {
			client = wsww.client
			sub(requestUri, client)
		}

		// track client connection
		trackID := chi.RouteContext(ctx).URLParam(trackKey)
		if trackID != "" && client != nil {
			trackMap.trackClient(trackID, client)
		}

		// skip, if handler is not provided
		if handler == nil {
			return
		}

		reply := func(payload interface{}) {
			resp := struct {
				Key     string      `json:"key"`
				Uri     string      `json:"uri"`
				Payload interface{} `json:"payload"`
			}{
				Key:     key,
				Uri:     requestUri,
				Payload: payload,
			}
			b, err := json.Marshal(resp)
			if err != nil {
				log.Debug().Err(err).Msgf("marshalling json in reply for %s has failed", key)
				return
			}

			_, err = w.Write(b) // NOTE: will write args if reply is fired
			if err != nil {
				log.Debug().Err(err).Msgf("client write failed", key)
			}
		}

		err := handler(ctx, key, []byte{}, reply)
		if err != nil {
			if terr, ok := err.(*terror.TError); ok {
				log.Debug().Str("key", key).Err(terr).Msg(terr.Message)
				return
			}
			log.Debug().Str("key", key).Err(err).Msg("OnConnect action has failed")
		}
	})
	return srv
}
