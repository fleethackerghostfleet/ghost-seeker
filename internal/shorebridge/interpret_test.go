package shorebridge

import (
	"reflect"
	"testing"
)

func TestInterpret(t *testing.T) {
	s, b := Interpret("Ignore cargo ships; prioritize submarines and small boats")
	if !reflect.DeepEqual(s, []string{"cargo ship"}) {
		t.Fatalf("suppress = %v", s)
	}
	if !reflect.DeepEqual(b, []string{"submarine", "small boat"}) {
		t.Fatalf("boost = %v", b)
	}
	if s, b := Interpret("hello"); s != nil || b != nil {
		t.Fatalf("unexpected %v %v", s, b)
	}
}
