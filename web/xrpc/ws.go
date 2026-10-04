package xrpc

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

const (
	subprotocolJSON = "xrpc.json"
	subprotocolXML  = "xrpc.xml"
	maxInflight     = 128
	writeTimeout    = 10 * time.Second
)

type wsConn struct {
	svc    *Service
	conn   *websocket.Conn
	wire   wire
	ctx    context.Context
	wmu    sync.Mutex
	smu    sync.Mutex
	stream map[string]*wsStream
	sem    chan struct{}
}

type wsStream struct {
	m      *method
	in     chan proto.Message
	ctx    context.Context
	cancel context.CancelFunc
	closed bool // only accessed from the read loop
}

func (st *wsStream) recv() (proto.Message, error) {
	select {
	case msg, ok := <-st.in:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-st.ctx.Done():
		return nil, st.ctx.Err()
	}
}

func (s *Service) serveWS(w http.ResponseWriter, r *http.Request) {
	up := s.upgrader
	up.Subprotocols = []string{subprotocolJSON, subprotocolXML}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to upgrade connection to websocket")
		return
	}

	p := ProtocolJSON
	if conn.Subprotocol() == subprotocolXML {
		p = ProtocolXML
	}

	ctx, cancel := context.WithCancel(withProtocol(WithWebSocketContext(r.Context(), w, r, conn), p))
	defer cancel()

	c := &wsConn{
		svc:    s,
		conn:   conn,
		wire:   newWire(p, s.codecFor(w, r)),
		ctx:    ctx,
		stream: make(map[string]*wsStream),
		sem:    make(chan struct{}, maxInflight),
	}
	c.serve()
}

func (c *wsConn) send(data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *wsConn) sendReplies(replies []*reply, batch bool) {
	out, err := c.wire.encodeReplies(replies, batch)
	if err != nil {
		logger.Error().Err(err).Msg("failed to encode response")
		return
	}
	if err := c.send(out); err != nil {
		logger.Trace().Err(err).Msg("failed to write websocket message")
	}
}

func (c *wsConn) fail(id rpcID, err *Error) {
	c.sendReplies([]*reply{{id: id, err: err}}, false)
}

func (c *wsConn) acquire() bool {
	select {
	case c.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *wsConn) release() { <-c.sem }

func (c *wsConn) serve() {
	defer func() {
		c.wmu.Lock()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		c.wmu.Unlock()
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(c.svc.maxBody)
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage {
			c.fail(rpcID{set: true, null: true}, NewError(CodeInvalidRequest, "only text messages are supported"))
			continue
		}
		c.dispatch(data)
	}
}

func (c *wsConn) dispatch(data []byte) {
	envs, batch, perr := c.wire.parse(data)
	if perr != nil {
		c.fail(rpcID{set: true, null: true}, perr)
		return
	}

	if !batch && envs[0].err == nil && !envs[0].isResp {
		e := envs[0]
		switch e.method {
		case methodMessage, methodClose, methodCancel:
			c.control(e)
			return
		}
		if m := c.svc.find(e.method); m != nil && m.kind != kindUnary && e.id.set {
			c.startStream(m, e)
			return
		}
	}

	if !c.acquire() {
		c.fail(rpcID{set: true, null: true}, NewError(CodeServerError, "too many concurrent requests"))
		return
	}
	go func() {
		defer c.release()
		if replies := c.svc.process(c.ctx, envs); len(replies) > 0 {
			c.sendReplies(replies, batch)
		}
	}()
}

func (c *wsConn) lookup(id rpcID) *wsStream {
	c.smu.Lock()
	defer c.smu.Unlock()
	return c.stream[id.key()]
}

// control handles stream notifications; errors cannot be reported for notifications and are ignored.
func (c *wsConn) control(e *envelope) {
	id, bindData, err := e.ctrl()
	if err != nil {
		return
	}
	st := c.lookup(id)
	if st == nil {
		return
	}

	switch e.method {
	case methodCancel:
		st.cancel()
	case methodClose:
		if !st.closed && st.m.kind != kindServerStream {
			st.closed = true
			close(st.in)
		}
	case methodMessage:
		if st.closed || st.m.kind == kindServerStream {
			return
		}
		msg := st.m.newIn()
		if err := bindData(msg); err != nil {
			st.cancel()
			return
		}
		select {
		case st.in <- msg:
		case <-st.ctx.Done():
		}
	}
}

func (c *wsConn) startStream(m *method, e *envelope) {
	if e.id.null {
		c.fail(e.id, NewError(CodeInvalidRequest, "stream requests require an id"))
		return
	}

	var first proto.Message
	if m.kind == kindServerStream {
		first = m.newIn()
		if err := e.bind(first); err != nil {
			c.fail(e.id, NewError(CodeInvalidParams, "invalid params: "+err.Error()))
			return
		}
	}

	if !c.acquire() {
		c.fail(e.id, NewError(CodeServerError, "too many concurrent requests"))
		return
	}

	key := e.id.key()
	ctx, cancel := context.WithCancel(c.ctx)
	st := &wsStream{m: m, in: make(chan proto.Message, 16), ctx: ctx, cancel: cancel}

	c.smu.Lock()
	if _, dup := c.stream[key]; dup {
		c.smu.Unlock()
		cancel()
		c.release()
		c.fail(e.id, NewError(CodeInvalidRequest, "duplicate request id"))
		return
	}
	c.stream[key] = st
	c.smu.Unlock()

	go func() {
		defer c.release()
		defer cancel()

		send := func(msg proto.Message) error {
			data, err := c.wire.encodeEvent(methodMessage, e.id, msg)
			if err != nil {
				return err
			}
			return c.send(data)
		}
		res, err := m.invoke(ctx, first, st.recv, send)

		c.smu.Lock()
		delete(c.stream, key)
		c.smu.Unlock()

		r := &reply{id: e.id}
		switch {
		case err != nil:
			r.err = toError(err)
			logger.Warn().Err(err).Field("method", m.full+"/"+m.name).Msg("xrpc stream failed")
		case m.kind == kindClientStream:
			r.result = res
		}
		c.sendReplies([]*reply{r}, false)
	}()
}
