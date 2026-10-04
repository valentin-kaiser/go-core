package xrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime/debug"
	"sync"

	"github.com/valentin-kaiser/go-core/apperror"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type kind int

const (
	kindUnary kind = iota
	kindServerStream
	kindClientStream
	kindBidi
)

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	messageType = reflect.TypeOf((*proto.Message)(nil)).Elem()
)

type recvFunc func() (proto.Message, error)
type sendFunc func(proto.Message) error

// method is a cached, validated service method.
type method struct {
	service string // short service name
	full    string // fully qualified service name
	name    string
	desc    protoreflect.MethodDescriptor
	fn      reflect.Value
	kind    kind
	in, out reflect.Type // pointer-to-message types
}

func (m *method) newIn() proto.Message  { return newMessage(m.in) }
func (m *method) newOut() proto.Message { return newMessage(m.out) }

func newMessage(t reflect.Type) proto.Message {
	return reflect.New(t.Elem()).Interface().(proto.Message) //nolint:forcetypeassert // validated at registration
}

// messageOf returns the pointer-to-message type matching desc, or an error.
func messageOf(t reflect.Type, desc protoreflect.MessageDescriptor) (reflect.Type, error) {
	if t.Kind() != reflect.Ptr || !t.Implements(messageType) {
		return nil, apperror.NewErrorf("%s is not a protobuf message pointer", t)
	}
	if got := newMessage(t).ProtoReflect().Descriptor().FullName(); got != desc.FullName() {
		return nil, apperror.NewErrorf("message type mismatch: expected %s, got %s", desc.FullName(), got)
	}
	return t, nil
}

func chanOf(t reflect.Type, dir reflect.ChanDir, desc protoreflect.MessageDescriptor) (reflect.Type, error) {
	if t.Kind() != reflect.Chan || t.ChanDir()&dir == 0 {
		return nil, apperror.NewErrorf("%s is not a channel with the required direction", t)
	}
	return messageOf(t.Elem(), desc)
}

// bind validates the signature of fn against the method descriptor.
func bind(fn reflect.Value, md protoreflect.MethodDescriptor) (*method, error) {
	t := fn.Type()
	m := &method{fn: fn, desc: md, name: string(md.Name())}
	if t.NumIn() < 1 || t.In(0) != contextType {
		return nil, apperror.NewError("first parameter must be context.Context")
	}

	var err error
	switch cs, ss := md.IsStreamingClient(), md.IsStreamingServer(); {
	case !cs && !ss:
		m.kind = kindUnary
		if t.NumIn() != 2 || t.NumOut() != 2 || t.Out(1) != errorType {
			return nil, apperror.NewError("unary method must be func(context.Context, *In) (*Out, error)")
		}
		if m.in, err = messageOf(t.In(1), md.Input()); err != nil {
			return nil, err
		}
		m.out, err = messageOf(t.Out(0), md.Output())
	case !cs && ss:
		m.kind = kindServerStream
		if t.NumIn() != 3 || t.NumOut() != 1 || t.Out(0) != errorType {
			return nil, apperror.NewError("server streaming method must be func(context.Context, *In, chan<- *Out) error")
		}
		if m.in, err = messageOf(t.In(1), md.Input()); err != nil {
			return nil, err
		}
		m.out, err = chanOf(t.In(2), reflect.SendDir, md.Output())
	case cs && !ss:
		m.kind = kindClientStream
		if t.NumIn() != 2 || t.NumOut() != 2 || t.Out(1) != errorType {
			return nil, apperror.NewError("client streaming method must be func(context.Context, <-chan *In) (*Out, error)")
		}
		if m.in, err = chanOf(t.In(1), reflect.RecvDir, md.Input()); err != nil {
			return nil, err
		}
		m.out, err = messageOf(t.Out(0), md.Output())
	default:
		m.kind = kindBidi
		if t.NumIn() != 3 || t.NumOut() != 1 || t.Out(0) != errorType {
			return nil, apperror.NewError("bidirectional streaming method must be func(context.Context, <-chan *In, chan<- *Out) error")
		}
		if m.in, err = chanOf(t.In(1), reflect.RecvDir, md.Input()); err != nil {
			return nil, err
		}
		m.out, err = chanOf(t.In(2), reflect.SendDir, md.Output())
	}
	return m, err
}

// call invokes the method and converts panics to errors.
func (m *method) call(args []reflect.Value) (res proto.Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error().Field("method", m.full+"/"+m.name).Field("panic", fmt.Sprint(r)).Field("stack", string(debug.Stack())).Msg("handler panicked")
			res, err = nil, NewError(CodeInternalError, "internal error")
		}
	}()

	outs := m.fn.Call(args)
	last := outs[len(outs)-1]
	if !last.IsNil() {
		return nil, last.Interface().(error) //nolint:forcetypeassert // validated at registration
	}
	if len(outs) == 2 && !outs[0].IsNil() {
		return outs[0].Interface().(proto.Message), nil //nolint:forcetypeassert // validated at registration
	}
	return nil, nil
}

func closeSafe(ch reflect.Value) {
	defer func() { _ = recover() }() // handlers may already have closed the channel
	ch.Close()
}

// feed forwards messages from recv into ch until recv fails or ctx ends.
func feed(ctx context.Context, cancel context.CancelFunc, ch reflect.Value, recv recvFunc) func() error {
	var (
		mu  sync.Mutex
		res error
	)
	go func() {
		defer closeSafe(ch)
		for {
			msg, err := recv()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					mu.Lock()
					res = err
					mu.Unlock()
					cancel()
				}
				return
			}
			chosen, _, _ := reflect.Select([]reflect.SelectCase{
				{Dir: reflect.SelectSend, Chan: ch, Send: reflect.ValueOf(msg)},
				{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
			})
			if chosen == 1 {
				return
			}
		}
	}()
	return func() error {
		mu.Lock()
		defer mu.Unlock()
		return res
	}
}

// invoke runs the method regardless of its streaming kind.
//
// first is the request for unary and server streaming methods, recv supplies
// the request messages for client and bidirectional streams, and send receives
// the messages emitted by server and bidirectional streams. The returned
// message is the response of unary and client streaming methods.
func (m *method) invoke(ctx context.Context, first proto.Message, recv recvFunc, send sendFunc) (proto.Message, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []reflect.Value{reflect.ValueOf(ctx)}
	var recvErr func() error
	switch m.kind {
	case kindUnary, kindServerStream:
		args = append(args, reflect.ValueOf(first))
	default:
		in := reflect.MakeChan(reflect.ChanOf(reflect.BothDir, m.in), 0)
		recvErr = feed(ctx, cancel, in, recv)
		args = append(args, in)
	}

	var out reflect.Value
	if m.kind == kindServerStream || m.kind == kindBidi {
		out = reflect.MakeChan(reflect.ChanOf(reflect.BothDir, m.out), 0)
		args = append(args, out)
	}

	type result struct {
		msg proto.Message
		err error
	}
	done := make(chan result, 1)
	go func() {
		msg, err := m.call(args)
		if out.IsValid() {
			closeSafe(out)
		}
		done <- result{msg, err}
	}()

	var sendErr error
	if out.IsValid() {
		for {
			v, ok := out.Recv()
			if !ok {
				break
			}
			if sendErr != nil || v.IsNil() {
				continue // keep draining so the handler never blocks
			}
			if err := send(v.Interface().(proto.Message)); err != nil { //nolint:forcetypeassert // validated at registration
				sendErr = err
				cancel()
			}
		}
	}

	r := <-done
	switch {
	case sendErr != nil:
		return nil, sendErr
	case r.err != nil:
		return nil, r.err
	case recvErr != nil && recvErr() != nil:
		return nil, recvErr()
	}
	if r.msg == nil && (m.kind == kindUnary || m.kind == kindClientStream) {
		return m.newOut(), nil
	}
	return r.msg, nil
}
