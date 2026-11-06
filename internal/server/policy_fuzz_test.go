package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func FuzzDecodePolicy(f *testing.F) {
	for _, raw := range []string{"", "{", "null", "{}", `{"id":"scenario","version":"1","interest":{"suppressLabels":["cruise ship"]}}`, `{"layers":[{"echelon":"ECHELON_COMMAND","issuedBy":"CTF-99","rules":[{"id":"new-lock","locked":true,"boostLabels":["aircraft"]}]}]}`, `{"version":"18446744073709551616"}`, `{"unknown":1}`, `{"id":"a","id":"b"}`, `{} {}`} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := decodeProtoPolicy(bytes.NewReader(raw))
		if err != nil {
			if p != nil {
				t.Fatal("failed decode returned a usable policy")
			}
			return
		}
		if p == nil || !json.Valid(raw) {
			t.Fatal("malformed JSON accepted as a policy")
		}
		encoded, err := protojson.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		again, err := decodeProtoPolicy(bytes.NewReader(encoded))
		if err != nil || !proto.Equal(p, again) {
			t.Fatal("policy did not protobuf JSON round trip")
		}
	})
}
