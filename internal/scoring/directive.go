package scoring

import (
	"regexp"
	"sort"
	"strings"
)

// Rule is one boost/suppress clause parsed from a natural-language directive.
type Rule struct {
	Type    string   `json:"type"` // "boost" or "suppress"
	Classes []string `json:"classes"`
	Attrs   []string `json:"attrs"`
	Only    bool     `json:"only"`
}

// Directive is an operator request that reweights the command model.
type Directive struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Rules []Rule `json:"rules"`
}

type synonym struct {
	classes []string
	re      *regexp.Regexp
}

var classSyn = []synonym{
	{[]string{"warship"}, regexp.MustCompile(`\b(warships?|destroyers?|frigates?|cruisers?|corvettes?|combatants?|navy (ship|vessel)s?|naval (ship|vessel)s?|military (ship|vessel)s?|grey hulls?|gray hulls?)\b`)},
	{[]string{"submarine"}, regexp.MustCompile(`\b(submarines?|subs?|periscopes?|submersibles?)\b`)},
	{[]string{"tanker"}, regexp.MustCompile(`\b(tankers?|oilers?)\b`)},
	{[]string{"cargo ship"}, regexp.MustCompile(`\b(cargo( ships?| vessels?)?|container ?ships?|freighters?|merchant( ships?| vessels?)?|bulk carriers?)\b`)},
	{[]string{"fishing vessel"}, regexp.MustCompile(`\b(fishing( vessels?| boats?)?|trawlers?|fishermen)\b`)},
	{[]string{"small boat"}, regexp.MustCompile(`\b(small boats?|skiffs?|speed ?boats?|rhibs?|dinghy|dinghies|go-?fasts?|small craft|jet ?skis?|boats?)\b`)},
	{[]string{"military aircraft"}, regexp.MustCompile(`\b(military (aircraft|planes?|jets?)|fighters?|fighter jets?|jets?|bombers?|p-?8s?|patrol aircraft|warplanes?)\b`)},
	{[]string{"civil aircraft"}, regexp.MustCompile(`\b(airliners?|civil(ian)? (aircraft|planes?)|commercial (aircraft|planes?|flights?)|passenger (jets?|planes?)|private (jets?|planes?))\b`)},
	{[]string{"helicopter"}, regexp.MustCompile(`\b(helicopters?|helos?|rotary[- ]wing|rotorcraft|choppers?)\b`)},
	{[]string{"drone"}, regexp.MustCompile(`\b(drones?|uavs?|uas|suas|quadcopters?|unmanned aircraft)\b`)},
	{[]string{"person"}, regexp.MustCompile(`\b(persons?|people|swimmers?|man overboard|mob|divers?|survivors?|someone|humans?)( in the water)?\b`)},
	{[]string{"bird"}, regexp.MustCompile(`\b(birds?|gulls?|seagulls?|pelicans?|wildlife)\b`)},
	{[]string{"buoy"}, regexp.MustCompile(`\b(buoys?|channel markers?|navigation aids?|navaids?)\b`)},
	{[]string{"debris"}, regexp.MustCompile(`\b(debris|wreckage|floating objects?|flotsam|life ?rafts?|oil slicks?)\b`)},
}

var genericSyn = []synonym{
	{[]string{"warship", "cargo ship", "tanker", "fishing vessel", "small boat", "submarine"}, regexp.MustCompile(`\b(ships?|vessels?|surface contacts?|watercraft|maritime traffic)\b`)},
	{[]string{"military aircraft", "civil aircraft", "helicopter", "drone"}, regexp.MustCompile(`\b(aircraft|planes?|airplanes?|air contacts?|air traffic|airborne|flying objects?)\b`)},
}

var attrSyn = []struct {
	attr string
	re   *regexp.Regexp
}{
	{"approaching", regexp.MustCompile(`\b(approach\w*|inbound|toward\w*|closing|heading (to|for|toward)\w*|incoming|nearing)\b`)},
	{"new", regexp.MustCompile(`\b(new|newly|appear\w*|arriv\w*|enter\w*|change[sd]?|changes|emerg\w*|first seen|showed up|pop(ped)? up)\b`)},
	{"dark", regexp.MustCompile(`\b(ais|dark|unidentified|unknown|no transponder|not broadcasting|unregistered|spoof\w*|suspicious)\b`)},
	{"moving", regexp.MustCompile(`\b(moving|fast|speed\w*|underway|high[- ]speed|rapid\w*|racing|quickly)\b`)},
	{"loitering", regexp.MustCompile(`\b(loiter\w*|stationary|idle|anchored|circling|lingering|drifting|holding)\b`)},
}

var (
	negRe    = regexp.MustCompile(`\b(ignore|deprioriti\w*|de-prioriti\w*|exclude|suppress|filter out|skip|hide|less important|lower priority|don'?t care|not interested|no need|downrank)\b`)
	onlyRe   = regexp.MustCompile(`\b(only|exclusively|nothing (else|but))\b`)
	andNegRe = regexp.MustCompile(`\band (ignore|deprioriti|exclude|suppress|skip|hide|filter|don)`)
	splitRe  = regexp.MustCompile(`[,;.]|\bbut\b|\balso\b|\bthen\b`)
)

// ParseDirective maps free text onto boost/suppress rules over known object
// classes and behaviors. It is keyword-based; an LLM can replace it behind the
// same Rule output.
func ParseDirective(text string) []Rule {
	t := andNegRe.ReplaceAllString(strings.ToLower(text), ";$1")
	var rules []Rule
	for _, c := range splitRe.Split(t, -1) {
		if strings.TrimSpace(c) == "" {
			continue
		}
		neg := negRe.MatchString(c)
		only := onlyRe.MatchString(c)
		set := map[string]bool{}
		for _, s := range append(append([]synonym{}, classSyn...), genericSyn...) {
			if s.re.MatchString(c) {
				for _, k := range s.classes {
					set[k] = true
				}
				c = s.re.ReplaceAllString(c, " ")
			}
		}
		attrs := []string{}
		for _, a := range attrSyn {
			if a.re.MatchString(c) {
				attrs = append(attrs, a.attr)
			}
		}
		if len(set) == 0 && len(attrs) == 0 {
			continue
		}
		classes := make([]string, 0, len(set))
		for k := range set {
			classes = append(classes, k)
		}
		sort.Strings(classes)
		typ := "boost"
		if neg {
			typ = "suppress"
		}
		rules = append(rules, Rule{Type: typ, Classes: classes, Attrs: attrs, Only: only && !neg})
	}
	return rules
}
