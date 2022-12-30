package ws

import (
	"bytes"
	"encoding/json"
	"runtime/debug"

	"github.com/DataDog/gostackparse"
	"github.com/rs/zerolog"
)

// logPanicRecovery is intended to be used inside of a recover block.
//
// `r interface{}` is the empty interface returned from `recover()`
//
// Usage
//   if r := recover(); r != nil {
//      gamelog.logPanicRecovery("Message", r)
//      // other recovery code
//   }
func logPanicRecovery(msg string, r interface{}) {
	// Create log event to attach details to
	event := log.WithLevel(zerolog.FatalLevel).CallerSkipFrame(1).Interface("panic", r)

	// Get stack details
	s := debug.Stack()
	stack, errs := gostackparse.Parse(bytes.NewReader(s))
	if len(errs) != 0 {
		event = event.Errs("stack_parsing_errors", errs)
	}
	jStack, err := json.Marshal(stack)
	if err != nil {
		event.AnErr("stack_marshal_error", err).Msg(msg)
		return
	}

	// Execute log
	event.RawJSON("stack_trace", jStack).Msg(msg)
}
