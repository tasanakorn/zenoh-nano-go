package wire

import (
	"bytes"
	"testing"
)

func TestDecodeRequest(t *testing.T) {
	orig := &RequestMsg{
		RequestID:  42,
		DeclaredID: 0,
		KeyExpr:    "my/key",
		Body:       EncodeQueryBody(&QueryBody{Parameters: "?foo=1"}),
	}
	encoded := orig.Encode()
	if len(encoded) == 0 {
		t.Fatal("empty encode")
	}
	// encoded[0] is the network message header byte
	hdr := encoded[0]
	mid := MsgID(hdr)
	flags := MsgFlags(hdr)
	if mid != NidRequest {
		t.Fatalf("mid = 0x%02x, want NidRequest 0x%02x", mid, NidRequest)
	}
	req, err := DecodeRequest(encoded[1:], flags)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.RequestID != orig.RequestID {
		t.Fatalf("RequestID = %d, want %d", req.RequestID, orig.RequestID)
	}
	if req.KeyExpr != orig.KeyExpr {
		t.Fatalf("KeyExpr = %q, want %q", req.KeyExpr, orig.KeyExpr)
	}
	if req.Query == nil {
		t.Fatal("Query is nil")
	}
	if req.Query.Parameters != "?foo=1" {
		t.Fatalf("Query.Parameters = %q, want %q", req.Query.Parameters, "?foo=1")
	}
}

func TestBatchedFrameRegression(t *testing.T) {
	// Build REQUEST message
	reqMsg := &RequestMsg{
		RequestID:  7,
		DeclaredID: 0,
		KeyExpr:    "test/key",
		Body:       EncodeQueryBody(&QueryBody{Parameters: "?x=1"}),
	}
	reqBytes := reqMsg.Encode()

	// Build PUSH{PUT} message
	pushMsg := &PushMsg{
		DeclaredID: 0,
		KeyExpr:    "test/key",
		Body:       EncodePutBody(&PutBody{Payload: []byte("data")}),
	}
	pushBytes := pushMsg.Encode()

	// Concatenate into a single frame payload (simulates batching)
	combined := append(reqBytes, pushBytes...)

	msgs, err := DecodeNetworkStream(combined)
	if err != nil {
		t.Fatalf("DecodeNetworkStream: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (old drain bug would give 1)", len(msgs))
	}
	if msgs[0].Kind != NidRequest {
		t.Fatalf("msgs[0].Kind = 0x%02x, want NidRequest 0x%02x", msgs[0].Kind, NidRequest)
	}
	if msgs[0].Request == nil {
		t.Fatal("msgs[0].Request is nil")
	}
	if msgs[1].Kind != NidPush {
		t.Fatalf("msgs[1].Kind = 0x%02x, want NidPush 0x%02x", msgs[1].Kind, NidPush)
	}
	if msgs[1].Push == nil {
		t.Fatal("msgs[1].Push is nil")
	}
	if !bytes.Equal(msgs[1].Push.Put.Payload, []byte("data")) {
		t.Fatalf("push payload = %q, want %q", msgs[1].Push.Put.Payload, "data")
	}
}
