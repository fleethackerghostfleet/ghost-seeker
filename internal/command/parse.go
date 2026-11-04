package command

import (
	"regexp"
	"strings"
)

type classPattern struct {
	class string
	re    *regexp.Regexp
}

type genericPattern struct {
	classes []string
	re      *regexp.Regexp
}

var synonymPatterns = []classPattern{
	{"warship", regexp.MustCompile(`\b(warships?|destroyers?|frigates?|cruisers?|corvettes?|combatants?|navy (ship|vessel)s?|naval (ship|vessel)s?|military (ship|vessel)s?|grey hulls?|gray hulls?)\b`)},
	{"submarine", regexp.MustCompile(`\b(submarines?|subs?|periscopes?|submersibles?)\b`)},
	{"tanker", regexp.MustCompile(`\b(tankers?|oilers?)\b`)},
	{"cargo ship", regexp.MustCompile(`\b(cargo( ships?| vessels?)?|container ?ships?|freighters?|merchant( ships?| vessels?)?|bulk carriers?)\b`)},
	{"fishing vessel", regexp.MustCompile(`\b(fishing( vessels?| boats?)?|trawlers?|fishermen)\b`)},
	{"small boat", regexp.MustCompile(`\b(small boats?|skiffs?|speed ?boats?|rhibs?|dinghy|dinghies|go-?fasts?|small craft|jet ?skis?|boats?)\b`)},
	{"military aircraft", regexp.MustCompile(`\b(military (aircraft|planes?|jets?)|fighters?|fighter jets?|jets?|bombers?|p-?8s?|patrol aircraft|warplanes?)\b`)},
	{"civil aircraft", regexp.MustCompile(`\b(airliners?|civil(ian)? (aircraft|planes?)|commercial (aircraft|planes?|flights?)|passenger (jets?|planes?)|private (jets?|planes?))\b`)},
	{"helicopter", regexp.MustCompile(`\b(helicopters?|helos?|rotary[- ]wing|rotorcraft|choppers?)\b`)},
	{"drone", regexp.MustCompile(`\b(drones?|uavs?|uas|suas|quadcopters?|unmanned aircraft)\b`)},
	{"person", regexp.MustCompile(`\b(persons?|people|swimmers?|man overboard|mob|divers?|survivors?|someone|humans?)( in the water)?\b`)},
	{"bird", regexp.MustCompile(`\b(birds?|gulls?|seagulls?|pelicans?|wildlife)\b`)},
	{"buoy", regexp.MustCompile(`\b(buoys?|channel markers?|navigation aids?|navaids?)\b`)},
	{"debris", regexp.MustCompile(`\b(debris|wreckage|floating objects?|flotsam|life ?rafts?|oil slicks?)\b`)},
}

var genericPatterns = []genericPattern{
	{[]string{"warship", "cargo ship", "tanker", "fishing vessel", "small boat", "submarine"}, regexp.MustCompile(`\b(ships?|vessels?|surface contacts?|watercraft|maritime traffic)\b`)},
	{[]string{"military aircraft", "civil aircraft", "helicopter", "drone"}, regexp.MustCompile(`\b(aircraft|planes?|airplanes?|air contacts?|air traffic|airborne|flying objects?)\b`)},
}

var attributePatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"approaching", regexp.MustCompile(`\b(approach\w*|inbound|toward\w*|closing|heading (to|for|toward)\w*|incoming|nearing)\b`)},
	{"new", regexp.MustCompile(`\b(new|newly|appear\w*|arriv\w*|enter\w*|change[sd]?|changes|emerg\w*|first seen|showed up|pop(ped)? up)\b`)},
	{"dark", regexp.MustCompile(`\b(ais|dark|unidentified|unknown|no transponder|not broadcasting|unregistered|spoof\w*|suspicious)\b`)},
	{"moving", regexp.MustCompile(`\b(moving|fast|speed\w*|underway|high[- ]speed|rapid\w*|racing|quickly)\b`)},
	{"loitering", regexp.MustCompile(`\b(loiter\w*|stationary|idle|anchored|circling|lingering|drifting|holding)\b`)},
}

var (
	negativePattern = regexp.MustCompile(`\b(ignore|deprioriti\w*|de-prioriti\w*|exclude|suppress|filter out|skip|hide|less important|lower priority|don'?t care|not interested|no need|downrank)\b`)
	onlyPattern     = regexp.MustCompile(`\b(only|exclusively|nothing (else|but))\b`)
	splitPattern    = regexp.MustCompile(`[,;.]|\bbut\b|\balso\b|\bthen\b`)
	andNegPattern   = regexp.MustCompile(`\band (ignore|deprioriti|exclude|suppress|skip|hide|filter|don)`)
)

func ParseDirective(text string) []Rule {
	text = strings.ToLower(text)
	text = andNegPattern.ReplaceAllString(text, "; $1")
	clauses := splitPattern.Split(text, -1)
	rules := make([]Rule, 0)
	for _, clause := range clauses {
		if strings.TrimSpace(clause) == "" {
			continue
		}
		negative := negativePattern.MatchString(clause)
		only := onlyPattern.MatchString(clause)
		classes := make([]string, 0)
		seen := make(map[string]bool)
		for _, pattern := range synonymPatterns {
			if pattern.re.MatchString(clause) {
				if !seen[pattern.class] {
					classes = append(classes, pattern.class)
					seen[pattern.class] = true
				}
				clause = pattern.re.ReplaceAllString(clause, " ")
			}
		}
		for _, pattern := range genericPatterns {
			if pattern.re.MatchString(clause) {
				for _, class := range pattern.classes {
					if !seen[class] {
						classes = append(classes, class)
						seen[class] = true
					}
				}
				clause = pattern.re.ReplaceAllString(clause, " ")
			}
		}
		attrs := make([]string, 0)
		for _, pattern := range attributePatterns {
			if pattern.re.MatchString(clause) {
				attrs = append(attrs, pattern.name)
			}
		}
		if len(classes) == 0 && len(attrs) == 0 {
			continue
		}
		rules = append(rules, Rule{Type: map[bool]string{true: "suppress", false: "boost"}[negative], Classes: classes, Attrs: attrs, Only: only && !negative})
	}
	return rules
}
