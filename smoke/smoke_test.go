package smoke

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/leandroluk/proto-go-named/gen/testdata"
)

// TestWireAndJSONUnaffected proves the custom Go identifier (UserID instead
// of the default UserId) is purely cosmetic: proto wire encoding and JSON
// still key off the original proto field name.
func TestWireAndJSONUnaffected(t *testing.T) {
	in := &testdata.FooInput{
		UserID:    "u-1",
		TenantURL: "https://acme.example",
		Name:      "Alice",
	}

	wire, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := &testdata.FooInput{}
	if err := proto.Unmarshal(wire, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.UserID != in.UserID || out.TenantURL != in.TenantURL || out.Name != in.Name {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", out, in)
	}

	js, err := protojson.Marshal(in)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	got := string(js)
	if !strings.Contains(got, `"userId":"u-1"`) || !strings.Contains(got, `"tenantUrl":"https://acme.example"`) {
		t.Fatalf("expected wire-standard JSON keys (userId/tenantUrl), got: %s", got)
	}
}
