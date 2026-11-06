package command

import (
	"context"
	"reflect"
	"testing"
)

func TestOpenLanguageReviewRegressions(t *testing.T) {
	compiler := &Compiler{}
	got := compiler.Compile(context.Background(), "prioritize LNG carriers")
	if len(got.Rules) != 1 || !reflect.DeepEqual(got.Rules[0].Classes, []string{"tanker"}) {
		t.Fatalf("LNG carriers: %+v", got.Rules)
	}
	got = compiler.Compile(context.Background(), "focus on warships and other contacts from UAV-3")
	for _, rule := range got.Rules {
		if rule.Only {
			t.Fatalf("positive 'other contacts' became only: %+v", got.Rules)
		}
	}
	got = compiler.Compile(context.Background(), "I care most about people in the water, everything else can wait")
	if len(got.Rules) != 1 || !got.Rules[0].Only || !reflect.DeepEqual(got.Rules[0].Classes, []string{"person"}) {
		t.Fatalf("everything else can wait: %+v", got.Rules)
	}
}

func TestVertexEmptyTokenCommand(t *testing.T) {
	if _, _, err := commandToken(context.Background(), "   "); err == nil {
		t.Fatal("empty token command accepted")
	}
}
