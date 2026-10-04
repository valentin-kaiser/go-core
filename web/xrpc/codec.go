package xrpc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// maxDepth limits message nesting on decode and encode.
const maxDepth = 100

var errDepth = errors.New("message nesting too deep")

// wkt lists well-known types whose canonical JSON form differs from the generic object form.
var wkt = map[protoreflect.FullName]bool{
	"google.protobuf.Any":         true,
	"google.protobuf.Timestamp":   true,
	"google.protobuf.Duration":    true,
	"google.protobuf.Struct":      true,
	"google.protobuf.Value":       true,
	"google.protobuf.ListValue":   true,
	"google.protobuf.FieldMask":   true,
	"google.protobuf.Empty":       true,
	"google.protobuf.DoubleValue": true,
	"google.protobuf.FloatValue":  true,
	"google.protobuf.Int64Value":  true,
	"google.protobuf.UInt64Value": true,
	"google.protobuf.Int32Value":  true,
	"google.protobuf.UInt32Value": true,
	"google.protobuf.BoolValue":   true,
	"google.protobuf.StringValue": true,
	"google.protobuf.BytesValue":  true,
}

func isWKT(md protoreflect.MessageDescriptor) bool { return wkt[md.FullName()] }

// codec encodes protobuf messages with field numbers as keys (or field names if names is set).
// Decoding accepts both forms.
type codec struct {
	names bool
}

type fieldValue struct {
	fd protoreflect.FieldDescriptor
	v  protoreflect.Value
}

func setFields(m protoreflect.Message) []fieldValue {
	var out []fieldValue
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if !fd.IsExtension() {
			out = append(out, fieldValue{fd, v})
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].fd.Number() < out[j].fd.Number() })
	return out
}

func sortedMapKeys(mp protoreflect.Map) []protoreflect.MapKey {
	keys := make([]protoreflect.MapKey, 0, mp.Len())
	mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}

func (c codec) jsonKey(fd protoreflect.FieldDescriptor) string {
	if c.names {
		return fd.JSONName()
	}
	return strconv.Itoa(int(fd.Number()))
}

func (c codec) xmlName(fd protoreflect.FieldDescriptor) string {
	if c.names {
		return fd.JSONName()
	}
	return "_" + strconv.Itoa(int(fd.Number()))
}

func lookupField(md protoreflect.MessageDescriptor, key string) protoreflect.FieldDescriptor {
	fields := md.Fields()
	num := strings.TrimPrefix(key, "_")
	if n, err := strconv.ParseInt(num, 10, 32); err == nil {
		return fields.ByNumber(protoreflect.FieldNumber(n))
	}
	if fd := fields.ByJSONName(key); fd != nil {
		return fd
	}
	return fields.ByName(protoreflect.Name(key))
}

func scalarText(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return strconv.FormatBool(v.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return strconv.FormatInt(v.Int(), 10)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return strconv.FormatUint(v.Uint(), 10)
	case protoreflect.FloatKind:
		return formatFloat(v.Float(), 32)
	case protoreflect.DoubleKind:
		return formatFloat(v.Float(), 64)
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(v.Bytes())
	case protoreflect.EnumKind:
		return strconv.FormatInt(int64(v.Enum()), 10)
	}
	return ""
}

func formatFloat(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

func parseScalar(fd protoreflect.FieldDescriptor, s string) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		switch s {
		case "true":
			return protoreflect.ValueOfBool(true), nil
		case "false":
			return protoreflect.ValueOfBool(false), nil
		}
		return protoreflect.Value{}, fmt.Errorf("invalid bool %q", s)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(s, 10, 32)
		return protoreflect.ValueOfInt32(int32(n)), err
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(s, 10, 64)
		return protoreflect.ValueOfInt64(n), err
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(s, 10, 32)
		return protoreflect.ValueOfUint32(uint32(n)), err
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(s, 10, 64)
		return protoreflect.ValueOfUint64(n), err
	case protoreflect.FloatKind:
		f, err := strconv.ParseFloat(s, 32)
		return protoreflect.ValueOfFloat32(float32(f)), err
	case protoreflect.DoubleKind:
		f, err := strconv.ParseFloat(s, 64)
		return protoreflect.ValueOfFloat64(f), err
	case protoreflect.StringKind:
		if !utf8.ValidString(s) {
			return protoreflect.Value{}, errors.New("invalid UTF-8 string")
		}
		return protoreflect.ValueOfString(s), nil
	case protoreflect.BytesKind:
		b, err := decodeBase64(s)
		return protoreflect.ValueOfBytes(b), err
	case protoreflect.EnumKind:
		if n, err := strconv.ParseInt(s, 10, 32); err == nil {
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)), nil
		}
		ev := fd.Enum().Values().ByName(protoreflect.Name(s))
		if ev == nil {
			return protoreflect.Value{}, fmt.Errorf("unknown enum value %q", s)
		}
		return protoreflect.ValueOfEnum(ev.Number()), nil
	}
	return protoreflect.Value{}, fmt.Errorf("unsupported field kind %s", fd.Kind())
}

func decodeBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64 value")
}

func marshalWKT(m protoreflect.Message) ([]byte, error) {
	raw, err := protojson.Marshal(m.Interface())
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unmarshalWKT(data []byte, m protoreflect.Message) error {
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, m.Interface())
}

// JSON

func (c codec) marshalJSON(m proto.Message) ([]byte, error) {
	return c.appendJSON(nil, m.ProtoReflect(), 0)
}

func (c codec) unmarshalJSON(data []byte, m proto.Message) error {
	return c.readJSON(data, m.ProtoReflect(), 0)
}

func (c codec) appendJSON(b []byte, m protoreflect.Message, depth int) ([]byte, error) {
	if depth > maxDepth {
		return nil, errDepth
	}
	if isWKT(m.Descriptor()) {
		raw, err := marshalWKT(m)
		if err != nil {
			return nil, err
		}
		return append(b, raw...), nil
	}

	b = append(b, '{')
	for i, f := range setFields(m) {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, c.jsonKey(f.fd))
		b = append(b, ':')
		var err error
		b, err = c.appendJSONField(b, f.fd, f.v, depth)
		if err != nil {
			return nil, err
		}
	}
	return append(b, '}'), nil
}

func (c codec) appendJSONField(b []byte, fd protoreflect.FieldDescriptor, v protoreflect.Value, depth int) ([]byte, error) {
	var err error
	switch {
	case fd.IsList():
		l := v.List()
		b = append(b, '[')
		for i := 0; i < l.Len(); i++ {
			if i > 0 {
				b = append(b, ',')
			}
			if b, err = c.appendJSONValue(b, fd, l.Get(i), depth); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case fd.IsMap():
		mp := v.Map()
		b = append(b, '{')
		for i, k := range sortedMapKeys(mp) {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, k.String())
			b = append(b, ':')
			if b, err = c.appendJSONValue(b, fd.MapValue(), mp.Get(k), depth); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	}
	return c.appendJSONValue(b, fd, v, depth)
}

func (c codec) appendJSONValue(b []byte, fd protoreflect.FieldDescriptor, v protoreflect.Value, depth int) ([]byte, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return c.appendJSON(b, v.Message(), depth+1)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.StringKind, protoreflect.BytesKind:
		return appendString(b, scalarText(fd, v)), nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return appendString(b, scalarText(fd, v)), nil
		}
	}
	return append(b, scalarText(fd, v)...), nil
}

const hexDigits = "0123456789abcdef"

func appendString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"' || ch == '\\':
			b = append(b, '\\', ch)
		case ch == '\n':
			b = append(b, '\\', 'n')
		case ch == '\r':
			b = append(b, '\\', 'r')
		case ch == '\t':
			b = append(b, '\\', 't')
		case ch < 0x20:
			b = append(b, '\\', 'u', '0', '0', hexDigits[ch>>4], hexDigits[ch&0xf])
		default:
			b = append(b, ch)
		}
	}
	return append(b, '"')
}

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func (c codec) readJSON(data []byte, m protoreflect.Message, depth int) error {
	if depth > maxDepth {
		return errDepth
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || isNull(data) {
		return nil
	}
	if isWKT(m.Descriptor()) {
		return unmarshalWKT(data, m)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	for key, raw := range obj {
		fd := lookupField(m.Descriptor(), key)
		if fd == nil || isNull(raw) {
			continue
		}
		if err := c.readJSONField(m, fd, raw, depth); err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}
	return nil
}

func (c codec) readJSONField(m protoreflect.Message, fd protoreflect.FieldDescriptor, raw json.RawMessage, depth int) error {
	switch {
	case fd.IsList():
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		list := m.Mutable(fd).List()
		for _, it := range items {
			v, err := c.readJSONValue(fd, it, list.NewElement(), depth)
			if err != nil {
				return err
			}
			list.Append(v)
		}
		return nil
	case fd.IsMap():
		var items map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		mp := m.Mutable(fd).Map()
		for k, it := range items {
			key, err := parseScalar(fd.MapKey(), k)
			if err != nil {
				return err
			}
			v, err := c.readJSONValue(fd.MapValue(), it, mp.NewValue(), depth)
			if err != nil {
				return err
			}
			mp.Set(key.MapKey(), v)
		}
		return nil
	}

	if fd.Message() != nil {
		_, err := c.readJSONValue(fd, raw, m.Mutable(fd), depth)
		return err
	}
	v, err := c.readJSONValue(fd, raw, protoreflect.Value{}, depth)
	if err != nil {
		return err
	}
	m.Set(fd, v)
	return nil
}

func (c codec) readJSONValue(fd protoreflect.FieldDescriptor, raw []byte, cur protoreflect.Value, depth int) (protoreflect.Value, error) {
	if fd.Message() != nil {
		return cur, c.readJSON(raw, cur.Message(), depth+1)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return protoreflect.Value{}, errors.New("empty value")
	}
	var text string
	switch raw[0] {
	case '"':
		if err := json.Unmarshal(raw, &text); err != nil {
			return protoreflect.Value{}, err
		}
	case '{', '[':
		return protoreflect.Value{}, errors.New("unexpected object or array")
	default:
		text = string(raw)
	}
	return parseScalar(fd, text)
}

// XML

type xmlNode struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*xmlNode
}

func (n *xmlNode) attr(name string) (string, bool) {
	v, ok := n.attrs[name]
	return v, ok
}

func (n *xmlNode) child(name string) *xmlNode {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

const maxXMLNodes = 1 << 20

func parseXML(data []byte) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		root  *xmlNode
		stack []*xmlNode
		count int
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) && root != nil && len(stack) == 0 {
				return root, nil
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			count++
			if count > maxXMLNodes || len(stack) >= maxDepth*2 {
				return nil, errDepth
			}
			n := &xmlNode{name: t.Name.Local}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root != nil {
				return nil, errors.New("multiple root elements")
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.text += string(t)
			}
		}
	}
}

func (c codec) writeXMLFields(b *bytes.Buffer, m protoreflect.Message, depth int) error {
	if depth > maxDepth {
		return errDepth
	}
	if md := m.Descriptor(); isWKT(md) {
		if md.FullName() == "google.protobuf.Empty" {
			return nil
		}
		raw, err := marshalWKT(m)
		if err != nil {
			return err
		}
		return xml.EscapeText(b, raw)
	}

	for _, f := range setFields(m) {
		name := c.xmlName(f.fd)
		var err error
		switch {
		case f.fd.IsList():
			l := f.v.List()
			for i := 0; i < l.Len() && err == nil; i++ {
				err = c.writeXMLElem(b, name, nil, f.fd, l.Get(i), depth)
			}
		case f.fd.IsMap():
			mp := f.v.Map()
			for _, k := range sortedMapKeys(mp) {
				key := k.String()
				if err = c.writeXMLElem(b, name, &key, f.fd.MapValue(), mp.Get(k), depth); err != nil {
					break
				}
			}
		default:
			err = c.writeXMLElem(b, name, nil, f.fd, f.v, depth)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (c codec) writeXMLElem(b *bytes.Buffer, name string, key *string, fd protoreflect.FieldDescriptor, v protoreflect.Value, depth int) error {
	b.WriteByte('<')
	b.WriteString(name)
	if key != nil {
		if !validXMLString(*key) {
			return errors.New("map key contains characters not representable in XML")
		}
		b.WriteString(` k="`)
		if err := xml.EscapeText(b, []byte(*key)); err != nil {
			return err
		}
		b.WriteByte('"')
	}

	text := ""
	if fd.Message() == nil {
		text = scalarText(fd, v)
		if fd.Kind() == protoreflect.StringKind && !validXMLString(text) {
			b.WriteString(` enc="b64"`)
			text = base64.StdEncoding.EncodeToString([]byte(text))
		}
	}
	b.WriteByte('>')
	if fd.Message() != nil {
		if err := c.writeXMLFields(b, v.Message(), depth+1); err != nil {
			return err
		}
	} else if err := xml.EscapeText(b, []byte(text)); err != nil {
		return err
	}
	b.WriteString("</")
	b.WriteString(name)
	b.WriteByte('>')
	return nil
}

// validXMLString reports whether s only contains characters allowed in XML 1.0.
func validXMLString(s string) bool {
	for _, r := range s {
		switch {
		case r == 0x9, r == 0xA, r == 0xD, r >= 0x20 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD, r >= 0x10000:
		default:
			return false
		}
	}
	return true
}

func (c codec) readXMLFields(n *xmlNode, m protoreflect.Message, depth int) error {
	if depth > maxDepth {
		return errDepth
	}
	if isWKT(m.Descriptor()) {
		text := strings.TrimSpace(n.text)
		if text == "" {
			return nil
		}
		return unmarshalWKT([]byte(text), m)
	}

	for _, k := range n.kids {
		fd := lookupField(m.Descriptor(), k.name)
		if fd == nil {
			continue
		}
		if err := c.readXMLField(m, fd, k, depth); err != nil {
			return fmt.Errorf("field %q: %w", k.name, err)
		}
	}
	return nil
}

func (c codec) readXMLField(m protoreflect.Message, fd protoreflect.FieldDescriptor, n *xmlNode, depth int) error {
	switch {
	case fd.IsList():
		list := m.Mutable(fd).List()
		v, err := c.readXMLValue(fd, n, list.NewElement(), depth)
		if err != nil {
			return err
		}
		list.Append(v)
		return nil
	case fd.IsMap():
		ks, ok := n.attr("k")
		if !ok {
			return errors.New("map entry without key attribute")
		}
		key, err := parseScalar(fd.MapKey(), ks)
		if err != nil {
			return err
		}
		mp := m.Mutable(fd).Map()
		v, err := c.readXMLValue(fd.MapValue(), n, mp.NewValue(), depth)
		if err != nil {
			return err
		}
		mp.Set(key.MapKey(), v)
		return nil
	}

	if fd.Message() != nil {
		_, err := c.readXMLValue(fd, n, m.Mutable(fd), depth)
		return err
	}
	v, err := c.readXMLValue(fd, n, protoreflect.Value{}, depth)
	if err != nil {
		return err
	}
	m.Set(fd, v)
	return nil
}

func (c codec) readXMLValue(fd protoreflect.FieldDescriptor, n *xmlNode, cur protoreflect.Value, depth int) (protoreflect.Value, error) {
	if fd.Message() != nil {
		return cur, c.readXMLFields(n, cur.Message(), depth+1)
	}
	text := n.text
	switch {
	case fd.Kind() == protoreflect.StringKind && n.attrs["enc"] == "b64":
		raw, err := decodeBase64(strings.TrimSpace(text))
		if err != nil {
			return protoreflect.Value{}, err
		}
		text = string(raw)
	case fd.Kind() != protoreflect.StringKind:
		text = strings.TrimSpace(text)
	}
	return parseScalar(fd, text)
}
