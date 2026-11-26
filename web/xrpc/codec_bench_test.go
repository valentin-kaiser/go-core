package xrpc

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

func BenchmarkCodecMarshalJSON(b *testing.B) {
	md := allDescriptor(b)
	src := fill(md)
	c := codec{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := c.marshalJSON(src)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecUnmarshalJSON(b *testing.B) {
	md := allDescriptor(b)
	c := codec{}
	data, err := c.marshalJSON(fill(md))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := c.unmarshalJSON(data, dynamicpb.NewMessage(md))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Reference point: the stock protobuf wire encoding of the same message.
func BenchmarkProtoMarshal(b *testing.B) {
	src := fill(allDescriptor(b))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := proto.Marshal(src)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCodecXMLRoundTrip(b *testing.B) {
	md := allDescriptor(b)
	src := fill(md)
	c := codec{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		buf.WriteString("<x>")
		err := c.writeXMLFields(&buf, src.ProtoReflect(), 0)
		if err != nil {
			b.Fatal(err)
		}
		buf.WriteString("</x>")
		root, err := parseXML(buf.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		err = c.readXMLFields(root, dynamicpb.NewMessage(md), 0)
		if err != nil {
			b.Fatal(err)
		}
	}
}
