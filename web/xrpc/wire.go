package xrpc

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/valentin-kaiser/go-core/apperror"
	"google.golang.org/protobuf/proto"
)

// Protocol identifies a wire protocol.
type Protocol string

// Supported protocols.
const (
	ProtocolJSON Protocol = "json"
	ProtocolXML  Protocol = "xml"
	ProtocolGRPC Protocol = "grpc"
)

// Method names of the notifications used to carry stream messages over WebSocket.
const (
	methodMessage = "xrpc.message" // carries one stream message in either direction
	methodClose   = "xrpc.close"   // client finished sending
	methodCancel  = "xrpc.cancel"  // client aborts a stream
)

type rpcID struct {
	set  bool
	null bool
	num  bool
	text string
}

func (i rpcID) key() string {
	if i.num {
		return "n" + i.text
	}
	return "s" + i.text
}

func (i rpcID) appendJSON(b []byte) []byte {
	switch {
	case !i.set || i.null:
		return append(b, "null"...)
	case i.num:
		return append(b, i.text...)
	}
	return appendString(b, i.text)
}

func numericID(n uint64) rpcID {
	return rpcID{set: true, num: true, text: strconv.FormatUint(n, 10)}
}

func parseJSONID(raw []byte) (rpcID, bool) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return rpcID{}, true
	case isNull(raw):
		return rpcID{set: true, null: true}, true
	case raw[0] == '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return rpcID{}, false
		}
		return rpcID{set: true, text: s}, true
	case raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9'):
		return rpcID{set: true, num: true, text: string(raw)}, true
	}
	return rpcID{}, false
}

// envelope is a decoded request, notification or response.
type envelope struct {
	id     rpcID
	method string
	isResp bool
	// bind decodes the params of a request or the result of a response.
	bind func(proto.Message) error
	// ctrl decodes params of the form {id, data} used by stream notifications.
	ctrl func() (rpcID, func(proto.Message) error, error)
	err  *Error // invalid request, or error member of a response
}

type reply struct {
	id     rpcID
	result proto.Message
	err    *Error
}

// wire translates between the JSON-RPC 2.0 data model and a concrete encoding.
type wire interface {
	protocol() Protocol
	contentType() string
	parse(data []byte) (envs []*envelope, batch bool, err *Error)
	encodeReplies(replies []*reply, batch bool) ([]byte, error)
	// encodeRequest builds a request; a request without id is a notification.
	encodeRequest(id rpcID, method string, params proto.Message) ([]byte, error)
	// encodeEvent builds a stream notification carrying {id, data}.
	encodeEvent(method string, id rpcID, data proto.Message) ([]byte, error)
}

func newWire(p Protocol, c codec) wire {
	if p == ProtocolXML {
		return xmlWire{c}
	}
	return jsonWire{c}
}

type jsonWire struct{ c codec }

func (jsonWire) protocol() Protocol  { return ProtocolJSON }
func (jsonWire) contentType() string { return "application/json" }

func (w jsonWire) parse(data []byte) ([]*envelope, bool, *Error) {
	data = bytes.TrimSpace(data)
	if !json.Valid(data) {
		return nil, false, NewError(CodeParseError, "parse error")
	}
	if len(data) > 0 && data[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil || len(items) == 0 {
			return nil, true, NewError(CodeInvalidRequest, "invalid request")
		}
		envs := make([]*envelope, len(items))
		for i, it := range items {
			envs[i] = w.parseOne(it)
		}
		return envs, true, nil
	}
	return []*envelope{w.parseOne(data)}, false, nil
}

func (w jsonWire) parseOne(raw []byte) *envelope {
	var msg struct {
		Version string          `json:"jsonrpc"`
		Method  *string         `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	invalid := func(id rpcID) *envelope {
		return &envelope{id: id, err: NewError(CodeInvalidRequest, "invalid request")}
	}

	if err := json.Unmarshal(raw, &msg); err != nil {
		return invalid(rpcID{set: true, null: true})
	}
	id, ok := parseJSONID(msg.ID)
	if !ok {
		return invalid(rpcID{set: true, null: true})
	}
	if msg.Version != "2.0" {
		return invalid(id)
	}

	e := &envelope{id: id}
	switch {
	case msg.Method != nil:
		if *msg.Method == "" {
			return invalid(id)
		}
		e.method = *msg.Method
		e.bind = func(m proto.Message) error { return w.bindParams(msg.Params, m) }
		e.ctrl = func() (rpcID, func(proto.Message) error, error) {
			var p struct {
				ID   json.RawMessage `json:"id"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				return rpcID{}, nil, err
			}
			sid, ok := parseJSONID(p.ID)
			if !ok || !sid.set {
				return rpcID{}, nil, errMissingStreamID
			}
			return sid, func(m proto.Message) error { return w.c.unmarshalJSON(p.Data, m) }, nil
		}
	case len(msg.Result) > 0 || len(msg.Error) > 0:
		e.isResp = true
		if len(msg.Error) > 0 && !isNull(msg.Error) {
			var ev struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(msg.Error, &ev); err != nil {
				return invalid(id)
			}
			e.err = &Error{Code: ev.Code, Message: ev.Message, RawData: ev.Data}
		}
		e.bind = func(m proto.Message) error {
			if m == nil || len(msg.Result) == 0 || isNull(msg.Result) {
				return nil
			}
			return w.c.unmarshalJSON(msg.Result, m)
		}
	default:
		return invalid(id)
	}
	return e
}

func (w jsonWire) bindParams(params []byte, m proto.Message) error {
	p := bytes.TrimSpace(params)
	switch {
	case len(p) == 0 || isNull(p):
		return nil
	case p[0] == '{':
		return w.c.unmarshalJSON(p, m)
	case p[0] == '[':
		var items []json.RawMessage
		if err := json.Unmarshal(p, &items); err != nil {
			return err
		}
		switch len(items) {
		case 0:
			return nil
		case 1:
			return w.c.unmarshalJSON(items[0], m)
		}
	}
	return apperror.NewError("params must be an object or an array with at most one element")
}

func (w jsonWire) encodeReplies(replies []*reply, batch bool) ([]byte, error) {
	var (
		b   []byte
		err error
	)
	if batch {
		b = append(b, '[')
	}
	for i, r := range replies {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"jsonrpc":"2.0",`...)
		if r.err != nil {
			b = append(b, `"error":{"code":`...)
			b = strconv.AppendInt(b, int64(r.err.Code), 10)
			b = append(b, `,"message":`...)
			b = appendString(b, r.err.Message)
			if r.err.Data != nil {
				b = append(b, `,"data":`...)
				if b, err = w.c.appendJSON(b, r.err.Data.ProtoReflect(), 0); err != nil {
					return nil, err
				}
			}
			b = append(b, '}')
		} else {
			b = append(b, `"result":`...)
			if r.result == nil {
				b = append(b, "null"...)
			} else if b, err = w.c.appendJSON(b, r.result.ProtoReflect(), 0); err != nil {
				return nil, err
			}
		}
		b = append(b, `,"id":`...)
		b = r.id.appendJSON(b)
		b = append(b, '}')
	}
	if batch {
		b = append(b, ']')
	}
	return b, nil
}

func (w jsonWire) encodeRequest(id rpcID, method string, params proto.Message) ([]byte, error) {
	b := append([]byte(`{"jsonrpc":"2.0","method":`), appendString(nil, method)...)
	if params != nil {
		body, err := w.c.marshalJSON(params)
		if err != nil {
			return nil, err
		}
		b = append(b, `,"params":`...)
		if len(body) > 0 && body[0] != '{' {
			body = append(append([]byte{'['}, body...), ']') // params must be structured
		}
		b = append(b, body...)
	}
	if id.set {
		b = append(b, `,"id":`...)
		b = id.appendJSON(b)
	}
	return append(b, '}'), nil
}

func (w jsonWire) encodeEvent(method string, id rpcID, data proto.Message) ([]byte, error) {
	b := append([]byte(`{"jsonrpc":"2.0","method":`), appendString(nil, method)...)
	b = append(b, `,"params":{"id":`...)
	b = id.appendJSON(b)
	if data != nil {
		var err error
		b = append(b, `,"data":`...)
		if b, err = w.c.appendJSON(b, data.ProtoReflect(), 0); err != nil {
			return nil, err
		}
	}
	return append(b, "}}"...), nil
}
