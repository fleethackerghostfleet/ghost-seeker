package command

import (
	"context"
	"slices"
	"testing"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/openvocab"
)

func TestOpenVocabularyDirectives(t *testing.T) {
	c := &Compiler{Capabilities: testCapabilities{Capabilities{OpenVocabulary: true}}, EdgeSubtypes: true}
	for _, tt := range []struct{ text, label, action string }{
		{"look for life rafts", "life rafts", "boost"},
		{"can you look for life rafts?", "life rafts", "boost"},
		{"prioritize orange life jackets", "orange life jackets", "boost"},
		{"report oil slick", "oil slick", "boost"},
		{"ignore kayaks", "kayaks", "suppress"},
	} {
		t.Run(tt.text, func(t *testing.T) {
			compiled := c.Compile(context.Background(), tt.text)
			if compiled.Blocked || len(compiled.Unknown) != 0 || len(compiled.Rules) != 1 || !slices.Contains(compiled.Rules[0].Classes, tt.label) || compiled.Rules[0].Type != tt.action {
				t.Fatalf("compiled: %+v", compiled)
			}
			if slices.Contains(compiled.Rules[0].Classes, "debris") {
				t.Fatal("specific query collapsed into debris")
			}
			if !RuleMatch(compiled.Rules[0], Detection{Class: tt.label}) {
				t.Fatal("inference label does not match boost rule")
			}
		})
	}
	mixed := c.CompileRules("look for buoys and life rafts and orange life jackets")
	if len(mixed.Rules) != 1 || !slices.Equal(mixed.Rules[0].Classes, []string{"buoy", "life rafts", "orange life jackets"}) {
		t.Fatalf("mixed classes: %+v", mixed)
	}
	tooMany := c.CompileRules("find kayak, canoe, orange vest, red raft, oil slick, floating barrel")
	if !tooMany.Blocked || openVocabularyCount(tooMany.Rules) <= openvocab.MaxPrompts {
		t.Fatalf("unbounded: %+v", tooMany)
	}
	closed := (&Compiler{}).CompileRules("report kayaks")
	if len(closed.Rules) != 0 || len(closed.Unknown) == 0 {
		t.Fatalf("fixed sensor accepted query: %+v", closed)
	}
}
