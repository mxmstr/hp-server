package legacyfire

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCapturedLoginRequest(t *testing.T) {
	raw := decodeHex(t, "003100010028000000000009936a640000b61a6c010e6173646640617364662e636f6d00c21cf30109617364666173646600d2faee010100d39c250000")
	f, err := Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if f.Header.Component != 1 || f.Header.Command != 0x28 || f.Header.MessageID != 9 {
		t.Fatalf("unexpected header: %+v", f.Header)
	}
	if got := f.TDF["MAIL"]; got != "asdf@asdf.com" {
		t.Fatalf("MAIL=%#v", got)
	}
	if got := f.TDF["PASS"]; got != "asdfasdf" {
		t.Fatalf("PASS=%#v", got)
	}
}

func TestCapturedInvalidPasswordReply(t *testing.T) {
	req := &Frame{Header: Header{Component: 1, Command: 0x28, MessageID: 9}}
	payload := decodeHex(t, "c2e86d010100d699000000")
	response := ErrorReply(req, 0x0c, payload)
	var out bytes.Buffer
	if err := Write(&out, response); err != nil {
		t.Fatal(err)
	}
	want := decodeHex(t, "000b00010028000c30000009c2e86d010100d699000000")
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("reply mismatch\n got %x\nwant %x", out.Bytes(), want)
	}
}
