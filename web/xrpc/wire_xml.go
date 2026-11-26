package xrpc

import (
	"bytes"
	"encoding/xml"
	"strconv"

	"google.golang.org/protobuf/proto"
)

// xmlWire carries JSON-RPC 2.0 semantics in XML:
//
//	<request id="1" method="Service.Method"><params>…</params></request>
//	<response id="1"><result>…</result></response>
//	<response id="1"><error code="-32601" message="…"><data>…</data></error></response>
//	<batch>…</batch>
//
// Message fields are child elements named _<field number>.
type xmlWire struct{ c codec }

func (xmlWire) protocol() Protocol  { return ProtocolXML }
func (xmlWire) contentType() string { return "application/xml" }

func (w xmlWire) parse(data []byte) ([]*envelope, bool, *Error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, false, NewError(CodeParseError, "parse error")
	}
	if root == nil {
		return nil, false, NewError(CodeParseError, "parse error")
	}
	if root.name == "batch" {
		if len(root.kids) == 0 {
			return nil, true, NewError(CodeInvalidRequest, "invalid request")
		}
		envs := make([]*envelope, len(root.kids))
		for i, k := range root.kids {
			envs[i] = w.parseOne(k)
		}
		return envs, true, nil
	}
	return []*envelope{w.parseOne(root)}, false, nil
}

func (w xmlWire) parseOne(n *xmlNode) *envelope {
	e := &envelope{}
	if v, ok := n.attr("id"); ok {
		e.id = rpcID{set: true, text: v}
	}

	switch n.name {
	case "request":
		method, _ := n.attr("method")
		if method == "" {
			e.err = NewError(CodeInvalidRequest, "invalid request")
			return e
		}
		e.method = method
		params := n.child("params")
		e.bind = func(m proto.Message) error {
			if params == nil {
				return nil
			}
			return w.c.readXMLFields(params, m.ProtoReflect(), 0)
		}
		e.ctrl = func() (rpcID, func(proto.Message) error, error) {
			if params == nil {
				return rpcID{}, nil, errMissingStreamID
			}
			v, ok := params.attr("id")
			if !ok {
				return rpcID{}, nil, errMissingStreamID
			}
			data := params.child("data")
			return rpcID{set: true, text: v}, func(m proto.Message) error {
				if data == nil {
					return nil
				}
				return w.c.readXMLFields(data, m.ProtoReflect(), 0)
			}, nil
		}
	case "response":
		e.isResp = true
		if er := n.child("error"); er != nil {
			code, _ := er.attr("code")
			msg, _ := er.attr("message")
			c, _ := strconv.Atoi(code)
			e.err = &Error{Code: c, Message: msg}
			return e
		}
		result := n.child("result")
		e.bind = func(m proto.Message) error {
			if m == nil || result == nil {
				return nil
			}
			return w.c.readXMLFields(result, m.ProtoReflect(), 0)
		}
	default:
		e.err = NewError(CodeInvalidRequest, "invalid request")
	}
	return e
}

func writeIDAttr(b *bytes.Buffer, id rpcID) error {
	if !id.set || id.null {
		return nil
	}
	b.WriteString(` id="`)
	err := xml.EscapeText(b, []byte(id.text))
	if err != nil {
		return err
	}
	b.WriteByte('"')
	return nil
}

func writeAttr(b *bytes.Buffer, name, value string) error {
	b.WriteByte(' ')
	b.WriteString(name)
	b.WriteString(`="`)
	err := xml.EscapeText(b, []byte(value))
	if err != nil {
		return err
	}
	b.WriteByte('"')
	return nil
}

func (w xmlWire) encodeReplies(replies []*reply, batch bool) ([]byte, error) {
	var b bytes.Buffer
	if batch {
		b.WriteString("<batch>")
	}
	for _, r := range replies {
		b.WriteString("<response")
		err := writeIDAttr(&b, r.id)
		if err != nil {
			return nil, err
		}
		b.WriteByte('>')
		if r.err != nil {
			b.WriteString("<error")
			err := writeAttr(&b, "code", strconv.Itoa(r.err.Code))
			if err != nil {
				return nil, err
			}
			err = writeAttr(&b, "message", r.err.Message)
			if err != nil {
				return nil, err
			}
			b.WriteByte('>')
			if r.err.Data != nil {
				b.WriteString("<data>")
				err := w.c.writeXMLFields(&b, r.err.Data.ProtoReflect(), 0)
				if err != nil {
					return nil, err
				}
				b.WriteString("</data>")
			}
			b.WriteString("</error>")
		} else {
			b.WriteString("<result>")
			if r.result != nil {
				err := w.c.writeXMLFields(&b, r.result.ProtoReflect(), 0)
				if err != nil {
					return nil, err
				}
			}
			b.WriteString("</result>")
		}
		b.WriteString("</response>")
	}
	if batch {
		b.WriteString("</batch>")
	}
	return b.Bytes(), nil
}

func (w xmlWire) encodeRequest(id rpcID, method string, params proto.Message) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<request")
	err := writeIDAttr(&b, id)
	if err != nil {
		return nil, err
	}
	err = writeAttr(&b, "method", method)
	if err != nil {
		return nil, err
	}
	b.WriteByte('>')
	if params != nil {
		b.WriteString("<params>")
		err := w.c.writeXMLFields(&b, params.ProtoReflect(), 0)
		if err != nil {
			return nil, err
		}
		b.WriteString("</params>")
	}
	b.WriteString("</request>")
	return b.Bytes(), nil
}

func (w xmlWire) encodeEvent(method string, id rpcID, data proto.Message) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<request")
	err := writeAttr(&b, "method", method)
	if err != nil {
		return nil, err
	}
	b.WriteString("><params")
	err = writeAttr(&b, "id", id.text)
	if err != nil {
		return nil, err
	}
	b.WriteByte('>')
	if data != nil {
		b.WriteString("<data>")
		err := w.c.writeXMLFields(&b, data.ProtoReflect(), 0)
		if err != nil {
			return nil, err
		}
		b.WriteString("</data>")
	}
	b.WriteString("</params></request>")
	return b.Bytes(), nil
}
