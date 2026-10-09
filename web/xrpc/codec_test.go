package xrpc

import (
	"bytes"
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func field(name string, num int32, t descriptorpb.FieldDescriptorProto_Type, opts ...func(*descriptorpb.FieldDescriptorProto)) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Type:     t.Enum(),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		JsonName: proto.String(name),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

func repeated(f *descriptorpb.FieldDescriptorProto) {
	f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
}
func typeName(n string) func(*descriptorpb.FieldDescriptorProto) {
	return func(f *descriptorpb.FieldDescriptorProto) { f.TypeName = proto.String(n) }
}
func oneof(f *descriptorpb.FieldDescriptorProto) { f.OneofIndex = proto.Int32(0) }

func allDescriptor(t testing.TB) protoreflect.MessageDescriptor {
	t.Helper()
	entry := func(name string, key, val *descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
		return &descriptorpb.DescriptorProto{
			Name:    proto.String(name),
			Field:   []*descriptorpb.FieldDescriptorProto{key, val},
			Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		}
	}

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("codec/all.proto"),
		Package: proto.String("codec"),
		Syntax:  proto.String("proto3"),
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("Color"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("RED"), Number: proto.Int32(0)},
				{Name: proto.String("GREEN"), Number: proto.Int32(1)},
			},
		}},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("Inner"), Field: []*descriptorpb.FieldDescriptorProto{field("v", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)}},
			{
				Name: proto.String("All"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("i32", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32),
					field("i64", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("u32", 3, descriptorpb.FieldDescriptorProto_TYPE_UINT32),
					field("u64", 4, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
					field("s32", 5, descriptorpb.FieldDescriptorProto_TYPE_SINT32),
					field("s64", 6, descriptorpb.FieldDescriptorProto_TYPE_SINT64),
					field("f32", 7, descriptorpb.FieldDescriptorProto_TYPE_FIXED32),
					field("f64", 8, descriptorpb.FieldDescriptorProto_TYPE_FIXED64),
					field("sf32", 9, descriptorpb.FieldDescriptorProto_TYPE_SFIXED32),
					field("sf64", 10, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64),
					field("fl", 11, descriptorpb.FieldDescriptorProto_TYPE_FLOAT),
					field("db", 12, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
					field("b", 13, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					field("s", 14, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("by", 15, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					field("e", 16, descriptorpb.FieldDescriptorProto_TYPE_ENUM, typeName(".codec.Color")),
					field("inner", 17, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName(".codec.Inner")),
					field("ri", 18, descriptorpb.FieldDescriptorProto_TYPE_INT32, repeated),
					field("rs", 19, descriptorpb.FieldDescriptorProto_TYPE_STRING, repeated),
					field("rm", 20, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName(".codec.Inner"), repeated),
					field("m", 21, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName(".codec.All.MEntry"), repeated),
					field("mm", 22, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName(".codec.All.MmEntry"), repeated),
					field("os", 23, descriptorpb.FieldDescriptorProto_TYPE_STRING, oneof),
					field("oi", 24, descriptorpb.FieldDescriptorProto_TYPE_INT32, oneof),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("o")}},
				NestedType: []*descriptorpb.DescriptorProto{
					entry("MEntry",
						field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
						field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32)),
					entry("MmEntry",
						field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32),
						field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName(".codec.Inner"))),
				},
			},
		},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Messages().ByName("All")
}

func fill(md protoreflect.MessageDescriptor) *dynamicpb.Message {
	m := dynamicpb.NewMessage(md)
	set := func(name string, v protoreflect.Value) { m.Set(md.Fields().ByName(protoreflect.Name(name)), v) }
	set("i32", protoreflect.ValueOfInt32(-5))
	set("i64", protoreflect.ValueOfInt64(math.MinInt64))
	set("u32", protoreflect.ValueOfUint32(7))
	set("u64", protoreflect.ValueOfUint64(math.MaxUint64))
	set("s32", protoreflect.ValueOfInt32(-9))
	set("s64", protoreflect.ValueOfInt64(-10))
	set("f32", protoreflect.ValueOfUint32(11))
	set("f64", protoreflect.ValueOfUint64(12))
	set("sf32", protoreflect.ValueOfInt32(-13))
	set("sf64", protoreflect.ValueOfInt64(-14))
	set("fl", protoreflect.ValueOfFloat32(1.5))
	set("db", protoreflect.ValueOfFloat64(math.Inf(-1)))
	set("b", protoreflect.ValueOfBool(true))
	set("s", protoreflect.ValueOfString("a<b>&\"c\"\n\t\x01 ü"))
	set("by", protoreflect.ValueOfBytes([]byte{0, 1, 2, 255}))
	set("e", protoreflect.ValueOfEnum(1))

	inner := func(v string) protoreflect.Value {
		im := dynamicpb.NewMessage(md.Fields().ByName("inner").Message())
		im.Set(im.Descriptor().Fields().ByName("v"), protoreflect.ValueOfString(v))
		return protoreflect.ValueOfMessage(im)
	}
	set("inner", inner("x"))

	ri := m.Mutable(md.Fields().ByName("ri")).List()
	ri.Append(protoreflect.ValueOfInt32(1))
	ri.Append(protoreflect.ValueOfInt32(2))
	rs := m.Mutable(md.Fields().ByName("rs")).List()
	rs.Append(protoreflect.ValueOfString("p"))
	rs.Append(protoreflect.ValueOfString("q"))
	rm := m.Mutable(md.Fields().ByName("rm")).List()
	rm.Append(inner("m1"))
	rm.Append(inner("m2"))

	mp := m.Mutable(md.Fields().ByName("m")).Map()
	mp.Set(protoreflect.ValueOfString("k1").MapKey(), protoreflect.ValueOfInt32(1))
	mp.Set(protoreflect.ValueOfString("k<2").MapKey(), protoreflect.ValueOfInt32(2))
	mm := m.Mutable(md.Fields().ByName("mm")).Map()
	mm.Set(protoreflect.ValueOfInt32(-3).MapKey(), inner("mv"))

	set("os", protoreflect.ValueOfString("oneof"))
	return m
}

func TestCodecRoundTrip(t *testing.T) {
	md := allDescriptor(t)

	for _, names := range []bool{false, true} {
		c := codec{names: names}
		src := fill(md)

		t.Run("json", func(t *testing.T) {
			data, err := c.marshalJSON(src)
			if err != nil {
				t.Fatal(err)
			}
			dst := dynamicpb.NewMessage(md)
			if err := c.unmarshalJSON(data, dst); err != nil {
				t.Fatalf("%v\n%s", err, data)
			}
			if !proto.Equal(src, dst) {
				t.Fatalf("mismatch\n%s\n%v\n%v", data, src, dst)
			}
		})

		t.Run("xml", func(t *testing.T) {
			var buf bytes.Buffer
			buf.WriteString("<x>")
			if err := c.writeXMLFields(&buf, src.ProtoReflect(), 0); err != nil {
				t.Fatal(err)
			}
			buf.WriteString("</x>")
			root, err := parseXML(buf.Bytes())
			if err != nil {
				t.Fatalf("%v\n%s", err, buf.String())
			}
			dst := dynamicpb.NewMessage(md)
			if err := c.readXMLFields(root, dst, 0); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(src, dst) {
				t.Fatalf("mismatch\n%s\n%v\n%v", buf.String(), src, dst)
			}
		})
	}
}

func TestCodecFieldNumbers(t *testing.T) {
	md := allDescriptor(t)
	m := dynamicpb.NewMessage(md)
	m.Set(md.Fields().ByName("i32"), protoreflect.ValueOfInt32(5))
	m.Set(md.Fields().ByName("i64"), protoreflect.ValueOfInt64(6))
	m.Set(md.Fields().ByName("s"), protoreflect.ValueOfString("x"))

	data, err := codec{}.marshalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"1":5,"2":"6","14":"x"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	data, err = codec{names: true}.marshalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"i32":5,"i64":"6","s":"x"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}

	var buf bytes.Buffer
	if err := (codec{}).writeXMLFields(&buf, m, 0); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), `<_1>5</_1><_2>6</_2><_14>x</_14>`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCodecDecodeLeniency(t *testing.T) {
	md := allDescriptor(t)
	m := dynamicpb.NewMessage(md)
	// unknown field numbers are ignored, names and numbers are both accepted, null means unset
	err := codec{}.unmarshalJSON([]byte(`{"999":1,"i32":3,"14":"s","15":null,"16":"GREEN"}`), m)
	if err != nil {
		t.Fatal(err)
	}
	if m.Get(md.Fields().ByName("i32")).Int() != 3 || m.Get(md.Fields().ByName("s")).String() != "s" || m.Get(md.Fields().ByName("e")).Enum() != 1 {
		t.Fatalf("unexpected message %v", m)
	}
}

func TestCodecRejectsInvalid(t *testing.T) {
	md := allDescriptor(t)
	for _, in := range []string{`{"1":"abc"}`, `{"1":{}}`, `{"21":{"a":"x"}}`, `{"15":"***"}`, `[1]`} {
		if err := (codec{}).unmarshalJSON([]byte(in), dynamicpb.NewMessage(md)); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}
