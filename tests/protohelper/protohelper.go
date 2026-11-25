package protohelper

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func OrderWire(t *testing.T, fd protoreflect.FileDescriptor) []byte {
	t.Helper()
	order := fd.Messages().ByName("Order")
	item := fd.Messages().ByName("Item")
	f := func(name string) protoreflect.FieldDescriptor { return order.Fields().ByName(protoreflect.Name(name)) }

	newItem := func(sku string, qty uint32) protoreflect.Value {
		m := dynamicpb.NewMessage(item)
		m.Set(item.Fields().ByName("sku"), protoreflect.ValueOfString(sku))
		if qty > 0 {
			m.Set(item.Fields().ByName("quantity"), protoreflect.ValueOfUint32(qty))
		}
		return protoreflect.ValueOfMessage(m)
	}
	m := dynamicpb.NewMessage(order)
	m.Set(f("id"), protoreflect.ValueOfString("o1"))
	m.Set(f("total_cents"), protoreflect.ValueOfInt64(-5))
	m.Set(f("delta"), protoreflect.ValueOfInt32(-3))
	m.Set(f("weight"), protoreflect.ValueOfFloat64(1.5))
	m.Set(f("ratio"), protoreflect.ValueOfFloat32(0.25))
	m.Set(f("gift"), protoreflect.ValueOfBool(true))
	m.Set(f("note"), protoreflect.ValueOfBytes([]byte{1, 2}))
	m.Mutable(f("items")).List().Append(newItem("a", 2))
	for _, c := range []int32{1, -1, 300} {
		m.Mutable(f("codes")).List().Append(protoreflect.ValueOfInt32(c))
	}
	for _, s := range []string{"x", "y"} {
		m.Mutable(f("tags")).List().Append(protoreflect.ValueOfString(s))
	}
	m.Mutable(f("by_sku")).Map().Set(protoreflect.ValueOfString("k").MapKey(), newItem("b", 0))
	m.Mutable(f("labels")).Map().Set(protoreflect.ValueOfInt32(7).MapKey(), protoreflect.ValueOfString("seven"))
	m.Set(f("status"), protoreflect.ValueOfEnum(1))
	m.Set(f("priority"), protoreflect.ValueOfEnum(1))
	m.Set(f("coupon"), protoreflect.ValueOfString("c"))
	m.Set(f("voucher"), newItem("v", 0))
	ts := dynamicpb.NewMessage(f("placed_at").Message())
	ts.Set(ts.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(5))
	m.Set(f("placed_at"), protoreflect.ValueOfMessage(ts))
	m.Set(f("big"), protoreflect.ValueOfUint64(1<<40))
	m.Set(f("small"), protoreflect.ValueOfInt32(-9))
	m.Set(f("huge"), protoreflect.ValueOfUint64(1<<63))

	out, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// DescriptorSet serializes fd and its imports, dependencies first.
func DescriptorSet(t *testing.T, fd protoreflect.FileDescriptor) []byte {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{}
	for i := 0; i < fd.Imports().Len(); i++ {
		set.File = append(set.File, protodesc.ToFileDescriptorProto(fd.Imports().Get(i).FileDescriptor))
	}
	set.File = append(set.File, protodesc.ToFileDescriptorProto(fd))
	out, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
