package wire

import (
	"bufio"
	"bytes"
	"testing"
)

func TestDecodeQueryBody(t *testing.T) {
	t.Run("no params no payload", func(t *testing.T) {
		encoded := EncodeQueryBody(&QueryBody{})
		r := bufio.NewReader(bytes.NewReader(encoded))
		body, mid, err := DecodeBody(r)
		if err != nil {
			t.Fatalf("DecodeBody: %v", err)
		}
		if mid != ZidQuery {
			t.Fatalf("mid = 0x%02x, want 0x%02x", mid, ZidQuery)
		}
		q, ok := body.(*QueryBody)
		if !ok {
			t.Fatalf("body type %T, want *QueryBody", body)
		}
		if q.Parameters != "" {
			t.Fatalf("Parameters = %q, want empty", q.Parameters)
		}
		if q.HasPayload {
			t.Fatal("HasPayload should be false")
		}
	})

	t.Run("params only", func(t *testing.T) {
		encoded := EncodeQueryBody(&QueryBody{Parameters: "?foo=bar"})
		r := bufio.NewReader(bytes.NewReader(encoded))
		body, mid, err := DecodeBody(r)
		if err != nil {
			t.Fatalf("DecodeBody: %v", err)
		}
		if mid != ZidQuery {
			t.Fatalf("mid = 0x%02x, want 0x%02x", mid, ZidQuery)
		}
		q, ok := body.(*QueryBody)
		if !ok {
			t.Fatalf("body type %T, want *QueryBody", body)
		}
		if q.Parameters != "?foo=bar" {
			t.Fatalf("Parameters = %q, want %q", q.Parameters, "?foo=bar")
		}
		if q.HasPayload {
			t.Fatal("HasPayload should be false")
		}
	})

	t.Run("params plus payload plus encoding", func(t *testing.T) {
		orig := &QueryBody{
			Parameters:     "?k=v",
			EncodingID:     1,
			EncodingSchema: "",
			Payload:        []byte("hello"),
			HasPayload:     true,
		}
		encoded := EncodeQueryBody(orig)
		r := bufio.NewReader(bytes.NewReader(encoded))
		body, mid, err := DecodeBody(r)
		if err != nil {
			t.Fatalf("DecodeBody: %v", err)
		}
		if mid != ZidQuery {
			t.Fatalf("mid = 0x%02x, want 0x%02x", mid, ZidQuery)
		}
		q, ok := body.(*QueryBody)
		if !ok {
			t.Fatalf("body type %T, want *QueryBody", body)
		}
		if q.Parameters != orig.Parameters {
			t.Fatalf("Parameters = %q, want %q", q.Parameters, orig.Parameters)
		}
		if !q.HasPayload {
			t.Fatal("HasPayload should be true")
		}
		if q.EncodingID != orig.EncodingID {
			t.Fatalf("EncodingID = %d, want %d", q.EncodingID, orig.EncodingID)
		}
		if !bytes.Equal(q.Payload, orig.Payload) {
			t.Fatalf("Payload = %q, want %q", q.Payload, orig.Payload)
		}
	})
}
