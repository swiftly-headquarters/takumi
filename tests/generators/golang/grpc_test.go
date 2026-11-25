package golang_test

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/swiftly-headquarters/takumi/generators/golang"
	"github.com/swiftly-headquarters/takumi/protospec"
	"github.com/swiftly-headquarters/takumi/tests/protohelper"
	_ "google.golang.org/grpc" // keeps the grpc version in go.mod; TestGRPCSDK builds the generated SDK against it
)

const shopProto = "../typescript/testdata/shop.proto"

func TestGenerateGRPC(t *testing.T) {
	fd, err := protospec.Load(shopProto)
	if err != nil {
		t.Fatal(err)
	}
	files, err := golang.GenerateGRPC(fd)
	if err != nil {
		t.Fatal(err)
	}
	types, client := string(files["types.go"]), string(files["client.go"])
	for _, want := range []string{
		"// Order is a customer order.\ntype Order struct {",
		"TotalCents int64",
		"BySku",
		"map[string]*Item",
		"type Order_Coupon struct {",
		"type isOrder_Payment interface{ isOrder_Payment() }",
		"*timestamppb.Timestamp",
		"Status_STATUS_PAID",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.go missing %q\n%s", want, types)
		}
	}
	for _, want := range []string{
		"// Place creates an order.\nfunc (c *ShopClient) Place(ctx context.Context, in *Order, opts ...grpc.CallOption) (*Receipt, error) {",
		"grpc.ServerStreamingClient[Receipt]",
		"grpc.ClientStreamingClient[Item, Receipt]",
		"grpc.BidiStreamingClient[Item, Item]",
		`"/shop.v1.Shop/Place"`,
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.go missing %q\n%s", want, client)
		}
	}
}

// TestGRPCSDK builds the generated package in a temp module and, from inside it,
// checks the encoding against bytes made by the canonical protobuf encoder and
// runs every call kind against a real grpc server.
func TestGRPCSDK(t *testing.T) {
	fd, err := protospec.Load(shopProto)
	if err != nil {
		t.Fatal(err)
	}
	files, err := golang.GenerateGRPC(fd)
	if err != nil {
		t.Fatal(err)
	}
	version := func(module string) string {
		out, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", module).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	sum, err := os.ReadFile("../../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files["go.mod"] = []byte("module sdktest\n\ngo 1.22\n\nrequire (\n\tgoogle.golang.org/grpc " + version("google.golang.org/grpc") +
		"\n\tgoogle.golang.org/protobuf " + version("google.golang.org/protobuf") + "\n)\n")
	files["go.sum"] = sum
	files["sdk_test.go"] = []byte(strings.NewReplacer("__WIRE__", hex.EncodeToString(protohelper.OrderWire(t, fd)),
		"__DESC__", hex.EncodeToString(protohelper.DescriptorSet(t, fd))).Replace(grpcTest))
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test on generated SDK failed: %v\n%s", err, out)
	}
}

const grpcTest = `package sdk

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWire(t *testing.T) {
	want, _ := hex.DecodeString("__WIRE__")
	o := &Order{
		ID: "o1", TotalCents: -5, Delta: -3, Weight: 1.5, Ratio: 0.25, Gift: true, Note: []byte{1, 2},
		Items: []*Item{{Sku: "a", Quantity: 2}}, Codes: []int32{1, -1, 300}, Tags: []string{"x", "y"},
		BySku: map[string]*Item{"k": {Sku: "b"}}, Labels: map[int32]string{7: "seven"},
		Status: Status_STATUS_PAID, Priority: OrderPriority_HIGH, Coupon: &Order_Coupon{Coupon: "c"},
		Payment: &Order_Voucher{Voucher: &Item{Sku: "v"}}, PlacedAt: &timestamppb.Timestamp{Seconds: 5},
		Big: 1 << 40, Small: -9, Huge: 1 << 63,
	}
	got, err := proto.MarshalOptions{Deterministic: true}.Marshal(protoadapt.MessageV2Of(o))
	if err != nil {
		t.Fatal(err)
	}
	// Field order may differ (this encoder writes oneof members last), so compare
	// after a round trip through the canonical dynamic encoder.
	descBytes, _ := hex.DecodeString("__DESC__")
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(descBytes, set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	d, err := files.FindDescriptorByName("shop.v1.Order")
	if err != nil {
		t.Fatal(err)
	}
	dyn := dynamicpb.NewMessage(d.(protoreflect.MessageDescriptor))
	if err := proto.Unmarshal(got, dyn); err != nil {
		t.Fatal(err)
	}
	canon, err := proto.MarshalOptions{Deterministic: true}.Marshal(dyn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canon, want) {
		t.Fatalf("encoded\n%x\nwant\n%x", canon, want)
	}
	var back Order
	if err := proto.Unmarshal(want, protoadapt.MessageV2Of(&back)); err != nil {
		t.Fatal(err)
	}
	v, ok := back.Payment.(*Order_Voucher)
	if !ok || v.Voucher.Sku != "v" || back.TotalCents != -5 || back.Delta != -3 || back.Coupon.(*Order_Coupon).Coupon != "c" || back.BySku["k"].Sku != "b" ||
		back.Labels[7] != "seven" || len(back.Codes) != 3 || back.Codes[2] != 300 || back.Huge != 1<<63 || back.PlacedAt.Seconds != 5 {
		t.Fatalf("decoded wrong: %v", &back)
	}
	if o.String() == "" {
		t.Fatal("empty String()")
	}
}

func TestCalls(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "shop.v1.Shop",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "Place", Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new(Order)
			if err := dec(in); err != nil {
				return nil, err
			}
			return &Receipt{ID: in.ID, Status: Status_STATUS_PAID}, nil
		}}},
		Streams: []grpc.StreamDesc{
			{StreamName: "Watch", ServerStreams: true, Handler: func(_ any, s grpc.ServerStream) error {
				in := new(WatchRequest)
				if err := s.RecvMsg(in); err != nil {
					return err
				}
				for _, st := range []Status{Status_STATUS_PAID, Status_STATUS_SHIPPED} {
					if err := s.SendMsg(&Receipt{ID: in.ID, Status: st}); err != nil {
						return err
					}
				}
				return nil
			}},
			{StreamName: "Upload", ClientStreams: true, Handler: func(_ any, s grpc.ServerStream) error {
				n := 0
				for {
					in := new(Item)
					if s.RecvMsg(in) != nil {
						break
					}
					n += int(in.Quantity)
				}
				return s.SendMsg(&Receipt{ID: "got", Status: Status(n)})
			}},
		},
	}, nil)
	go srv.Serve(lis)
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	c := NewClient(conn)
	ctx := context.Background()

	r, err := c.Shop.Place(ctx, &Order{ID: "o9"})
	if err != nil || r.ID != "o9" || r.Status != Status_STATUS_PAID {
		t.Fatalf("Place = %v, %v", r, err)
	}

	w, err := c.Shop.Watch(ctx, &WatchRequest{ID: "w"})
	if err != nil {
		t.Fatal(err)
	}
	var got []Status
	for {
		m, err := w.Recv()
		if err != nil {
			break
		}
		got = append(got, m.Status)
	}
	if len(got) != 2 || got[1] != Status_STATUS_SHIPPED {
		t.Fatalf("Watch = %v", got)
	}

	u, err := c.Shop.Upload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u.Send(&Item{Quantity: 2})
	u.Send(&Item{Quantity: 3})
	rec, err := u.CloseAndRecv()
	if err != nil || rec.Status != 5 {
		t.Fatalf("Upload = %v, %v", rec, err)
	}
}
`
