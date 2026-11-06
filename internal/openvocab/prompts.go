// Package openvocab defines the bounded directive vocabulary shared by compiler and edge.
package openvocab

import (
	"regexp"
	"sort"
	"strings"

	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

const MaxPrompts = 5
const Prefix = "open-vocab:"

var phrase = regexp.MustCompile(`^[a-z][a-z0-9 -]{0,63}$`)
var control = regexp.MustCompile(`\b(and|or|not|ignore|boost|suppress|show|prioritize|look|find|please|only|everything|anything|from|within|minutes?|hours?|near)\b`)

func Normalize(s string) (string, bool) {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	return s, phrase.MatchString(s) && !control.MatchString(s) && len(strings.Fields(s)) <= 7
}

func Closed(label string) bool {
	if _, ok := catalog.Classes[label]; ok || catalog.EdgeSubtypes[label] {
		return true
	}
	switch label {
	case "boat", "ship", "airplane", "approaching", "new", "dark", "moving", "loitering", "transient", "person in water", "dark contact":
		return true
	}
	return false
}

// Labels uses the effective policy (after target, lock and expiry processing),
// not raw directive text. Sorting makes overflow deterministic across map order.
func Labels(p *pb.Policy) []string {
	in := p.GetInterest()
	all := append(append([]string{}, in.GetBoostLabels()...), in.GetSuppressLabels()...)
	for label := range in.GetTierLabels() {
		all = append(all, label)
	}
	seen := map[string]bool{}
	for _, label := range all {
		if label, ok := Normalize(label); ok && !Closed(label) {
			seen[label] = true
		}
	}
	out := make([]string, 0, len(seen))
	for label := range seen {
		out = append(out, label)
	}
	sort.Strings(out)
	if len(out) > MaxPrompts {
		out = out[:MaxPrompts]
	}
	for i := range out {
		out[i] = Prefix + out[i]
	}
	return out
}
