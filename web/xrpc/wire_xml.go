package xrpc

import (
	"bytes"
	"encoding/xml"
	"strconv"

	"github.com/valentin-kaiser/go-core/apperror"
	"google.golang.org/protobuf/proto"
)

const (
	xmlElemBatch    = "batch"
	xmlElemRequest  = "request"
	xmlElemResponse = "response"
	xmlElemParams   = "params"
	xmlElemData     = "data"
	xmlElemError    = "error"
	xmlElemResult   = "result"

	xmlAttrID      = "id"
	xmlAttrMethod  = "method"
	xmlAttrCode    = "code"
	xmlAttrMessage = "message"
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
func (xmlWire) contentType() string { return mediaTypeXML }

func (w xmlWire) parse(data []byte) ([]*envelope, bool, *Error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, false, NewError(CodeParseError, "parse error")
	}
	if root == nil {
		return nil, false, NewError(CodeParseError, "parse error")
	}
	if root.name == xmlElemBatch {
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
	if v, ok := n.attr(xmlAttrID); ok {
		e.id = rpcID{set: true, text: v}
	}

	switch n.name {
	case xmlElemRequest:
		w.parseRequest(n, e)
	case xmlElemResponse:
		w.parseResponse(n, e)
	default:
		e.err = NewError(CodeInvalidRequest, "invalid request")
	}
	return e
}

func (w xmlWire) parseRequest(n *xmlNode, e *envelope) {
	method, _ := n.attr(xmlAttrMethod)
	if method == "" {
		e.err = NewError(CodeInvalidRequest, "invalid request")
		return
	}
	e.method = method
	params := n.child(xmlElemParams)
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
		v, ok := params.attr(xmlAttrID)
		if !ok {
			return rpcID{}, nil, errMissingStreamID
		}
		data := params.child(xmlElemData)
		return rpcID{set: true, text: v}, func(m proto.Message) error {
			if data == nil {
				return nil
			}
			return w.c.readXMLFields(data, m.ProtoReflect(), 0)
		}, nil
	}
}

func (w xmlWire) parseResponse(n *xmlNode, e *envelope) {
	e.isResp = true
	if er := n.child(xmlElemError); er != nil {
		code, _ := er.attr(xmlAttrCode)
		msg, _ := er.attr(xmlAttrMessage)
		c, _ := strconv.Atoi(code)
		e.err = &Error{Code: c, Message: msg}
		return
	}
	result := n.child(xmlElemResult)
	e.bind = func(m proto.Message) error {
		if m == nil || result == nil {
			return nil
		}
		return w.c.readXMLFields(result, m.ProtoReflect(), 0)
	}
}

func writeIDAttr(b *bytes.Buffer, id rpcID) error {
	if !id.set || id.null {
		return nil
	}
	b.WriteString(` id="`)
	err := xml.EscapeText(b, []byte(id.text))
	if err != nil {
		return apperror.NewError("escaping id attribute failed").AddError(err)
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
		return apperror.NewError("escaping attribute value failed").AddError(err)
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
		err := w.writeReply(&b, r)
		if err != nil {
			return nil, err
		}
	}
	if batch {
		b.WriteString("</batch>")
	}
	return b.Bytes(), nil
}

func (w xmlWire) writeReply(b *bytes.Buffer, r *reply) error {
	b.WriteString("<response")
	err := writeIDAttr(b, r.id)
	if err != nil {
		return apperror.NewError("writing response id failed").AddError(err)
	}
	b.WriteByte('>')
	if r.err != nil {
		err = w.writeErrorElem(b, r.err)
	} else {
		err = w.writeResultElem(b, r.result)
	}
	if err != nil {
		return err
	}
	b.WriteString("</response>")
	return nil
}

func (w xmlWire) writeErrorElem(b *bytes.Buffer, e *Error) error {
	b.WriteString("<error")
	err := writeAttr(b, xmlAttrCode, strconv.Itoa(e.Code))
	if err != nil {
		return apperror.NewError("writing error code failed").AddError(err)
	}
	err = writeAttr(b, xmlAttrMessage, e.Message)
	if err != nil {
		return apperror.NewError("writing error message failed").AddError(err)
	}
	b.WriteByte('>')
	if e.Data != nil {
		b.WriteString("<data>")
		err = w.c.writeXMLFields(b, e.Data.ProtoReflect(), 0)
		if err != nil {
			return apperror.NewError("encoding error data failed").AddError(err)
		}
		b.WriteString("</data>")
	}
	b.WriteString("</error>")
	return nil
}

func (w xmlWire) writeResultElem(b *bytes.Buffer, result proto.Message) error {
	b.WriteString("<result>")
	if result != nil {
		err := w.c.writeXMLFields(b, result.ProtoReflect(), 0)
		if err != nil {
			return apperror.NewError("encoding result failed").AddError(err)
		}
	}
	b.WriteString("</result>")
	return nil
}

func (w xmlWire) encodeRequest(id rpcID, method string, params proto.Message) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<request")
	err := writeIDAttr(&b, id)
	if err != nil {
		return nil, apperror.NewError("writing request id failed").AddError(err)
	}
	err = writeAttr(&b, xmlAttrMethod, method)
	if err != nil {
		return nil, apperror.NewError("writing request method failed").AddError(err)
	}
	b.WriteByte('>')
	if params != nil {
		b.WriteString("<params>")
		err := w.c.writeXMLFields(&b, params.ProtoReflect(), 0)
		if err != nil {
			return nil, apperror.NewError("encoding request params failed").AddError(err)
		}
		b.WriteString("</params>")
	}
	b.WriteString("</request>")
	return b.Bytes(), nil
}

func (w xmlWire) encodeEvent(method string, id rpcID, data proto.Message) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<request")
	err := writeAttr(&b, xmlAttrMethod, method)
	if err != nil {
		return nil, apperror.NewError("writing event method failed").AddError(err)
	}
	b.WriteString("><params")
	err = writeAttr(&b, xmlAttrID, id.text)
	if err != nil {
		return nil, apperror.NewError("writing event id failed").AddError(err)
	}
	b.WriteByte('>')
	if data != nil {
		b.WriteString("<data>")
		err := w.c.writeXMLFields(&b, data.ProtoReflect(), 0)
		if err != nil {
			return nil, apperror.NewError("encoding event data failed").AddError(err)
		}
		b.WriteString("</data>")
	}
	b.WriteString("</params></request>")
	return b.Bytes(), nil
}
