package wire

import (
	"bufio"
	"bytes"
	"testing"
)

func TestDeclQueryableBodyRoundTrip(t *testing.T) {
	in := &DeclQueryableBody{QueryableID: 1, DeclaredID: 0, KeyExpr: "demo/**"}
	encoded := EncodeDeclareBody(in)
	r := bufio.NewReader(bytes.NewReader(encoded))
	out, err := DecodeDeclareBody(r)
	if err != nil {
		t.Fatalf("DecodeDeclareBody: %v", err)
	}
	got, ok := out.(*DeclQueryableBody)
	if !ok {
		t.Fatalf("expected *DeclQueryableBody, got %T", out)
	}
	if got.QueryableID != in.QueryableID {
		t.Fatalf("QueryableID: got %d want %d", got.QueryableID, in.QueryableID)
	}
	if got.KeyExpr != in.KeyExpr {
		t.Fatalf("KeyExpr: got %q want %q", got.KeyExpr, in.KeyExpr)
	}
}

func TestUndeclQueryableBodyRoundTrip(t *testing.T) {
	in := &UndeclQueryableBody{QueryableID: 42}
	encoded := EncodeDeclareBody(in)
	r := bufio.NewReader(bytes.NewReader(encoded))
	out, err := DecodeDeclareBody(r)
	if err != nil {
		t.Fatalf("DecodeDeclareBody: %v", err)
	}
	got, ok := out.(*UndeclQueryableBody)
	if !ok {
		t.Fatalf("expected *UndeclQueryableBody, got %T", out)
	}
	if got.QueryableID != in.QueryableID {
		t.Fatalf("QueryableID: got %d want %d", got.QueryableID, in.QueryableID)
	}
}

func TestEncodeDecodeReplyBody(t *testing.T) {
	in := &ReplyBody{
		EncodingID:     42,
		EncodingSchema: "text/plain",
		Payload:        []byte("hello"),
	}
	encoded := EncodeReplyBody(in)
	r := bufio.NewReader(bytes.NewReader(encoded))
	body, mid, err := DecodeBody(r)
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if mid != ZidReply {
		t.Fatalf("mid: got 0x%02x want 0x%02x", mid, ZidReply)
	}
	got, ok := body.(*ReplyBody)
	if !ok {
		t.Fatalf("expected *ReplyBody, got %T", body)
	}
	if got.EncodingID != in.EncodingID {
		t.Fatalf("EncodingID: got %d want %d", got.EncodingID, in.EncodingID)
	}
	if got.EncodingSchema != in.EncodingSchema {
		t.Fatalf("EncodingSchema: got %q want %q", got.EncodingSchema, in.EncodingSchema)
	}
	if string(got.Payload) != string(in.Payload) {
		t.Fatalf("Payload: got %q want %q", got.Payload, in.Payload)
	}
}

func TestEncodeDecodeErrBody(t *testing.T) {
	in := &ErrBody{
		EncodingID:     7,
		EncodingSchema: "application/json",
		Payload:        []byte(`{"error":"not found"}`),
	}
	encoded := EncodeErrBody(in)
	r := bufio.NewReader(bytes.NewReader(encoded))
	body, mid, err := DecodeBody(r)
	if err != nil {
		t.Fatalf("DecodeBody: %v", err)
	}
	if mid != ZidErr {
		t.Fatalf("mid: got 0x%02x want 0x%02x", mid, ZidErr)
	}
	got, ok := body.(*ErrBody)
	if !ok {
		t.Fatalf("expected *ErrBody, got %T", body)
	}
	if got.EncodingID != in.EncodingID {
		t.Fatalf("EncodingID: got %d want %d", got.EncodingID, in.EncodingID)
	}
	if got.EncodingSchema != in.EncodingSchema {
		t.Fatalf("EncodingSchema: got %q want %q", got.EncodingSchema, in.EncodingSchema)
	}
	if string(got.Payload) != string(in.Payload) {
		t.Fatalf("Payload: got %q want %q", got.Payload, in.Payload)
	}
}

func TestResponseMsgRoundTrip(t *testing.T) {
	replyEncoded := EncodeReplyBody(&ReplyBody{Payload: []byte("answer")})
	in := &ResponseMsg{
		RequestID:  99,
		DeclaredID: 0,
		KeyExpr:    "demo/answer",
		Body:       replyEncoded,
	}
	encoded := in.Encode()
	r := bufio.NewReader(bytes.NewReader(encoded))
	// Consume the network header byte to extract flags before calling decoder.
	hdr, err := r.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte: %v", err)
	}
	if MsgID(hdr) != NidResponse {
		t.Fatalf("mid: got 0x%02x want 0x%02x", MsgID(hdr), NidResponse)
	}
	flags := MsgFlags(hdr)
	got, err := decodeResponseFromReader(r, flags)
	if err != nil {
		t.Fatalf("decodeResponseFromReader: %v", err)
	}
	if got.RequestID != in.RequestID {
		t.Fatalf("RequestID: got %d want %d", got.RequestID, in.RequestID)
	}
	if got.KeyExpr != in.KeyExpr {
		t.Fatalf("KeyExpr: got %q want %q", got.KeyExpr, in.KeyExpr)
	}
}

func TestResponseFinalMsgRoundTrip(t *testing.T) {
	in := &ResponseFinalMsg{RequestID: 123}
	encoded := in.Encode()
	r := bufio.NewReader(bytes.NewReader(encoded))
	// Consume the network header byte to extract flags before calling decoder.
	hdr, err := r.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte: %v", err)
	}
	if MsgID(hdr) != NidResponseFinal {
		t.Fatalf("mid: got 0x%02x want 0x%02x", MsgID(hdr), NidResponseFinal)
	}
	flags := MsgFlags(hdr)
	got, err := decodeResponseFinalFromReader(r, flags)
	if err != nil {
		t.Fatalf("decodeResponseFinalFromReader: %v", err)
	}
	if got.RequestID != in.RequestID {
		t.Fatalf("RequestID: got %d want %d", got.RequestID, in.RequestID)
	}
}
