package command

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
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
	{"unknown", regexp.MustCompile(`\b((unknown|unidentified) (objects?|contacts?)|unclassified)\b`)},
	{"warship", regexp.MustCompile(`\b(warships?|destroyers?|frigates?|cruisers?|corvettes?|combatants?|navy (ship|vessel)s?|naval (ship|vessel)s?|military (ship|vessel)s?|grey hulls?|gray hulls?)\b`)},
	{"submarine", regexp.MustCompile(`\b(submarines?|subs?|periscopes?|submersibles?)\b`)},
	{"tanker", regexp.MustCompile(`\b(tankers?|oilers?)\b`)},
	{"cargo ship", regexp.MustCompile(`\b(cargo( ships?| vessels?)?|container ?ships?|freighters?|merchant( ships?| vessels?)?|bulk carriers?)\b`)},
	{"fishing vessel", regexp.MustCompile(`\b(fishing( vessels?| boats?)?|trawlers?|fishermen)\b`)},
	{"small boat", regexp.MustCompile(`\b(sail\s?boats?|sailing (boats?|vessels?|yachts?)|yachts?|sloops?|catamarans?|schooners?|small boats?|skiffs?|speed ?boats?|rhibs?|dinghy|dinghies|go-?fasts?|small craft|jet ?skis?|boats?)\b`)},
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

const negativeActions = `ignore|deprioriti\w*|de-prioriti\w*|exclude|suppress|filter out|skip|hide|less important|lower priority|don'?t care|not interested|no need|downrank`
const positiveActions = `prioriti\w*|boost|show|report|focus`

var (
	negativePattern  = regexp.MustCompile(`\b(` + negativeActions + `)\b`)
	onlyPattern      = regexp.MustCompile(`\b(only|exclusively|nothing (else|but))\b`)
	splitPattern     = regexp.MustCompile(`[,;.]|\bbut\b|\balso\b|\bthen\b`)
	andActionPattern = regexp.MustCompile(`\band\s+(` + negativeActions + `|` + positiveActions + `)\b`)
)

func ParseDirective(text string) []Rule {
	rules, _ := ParseDirectiveDetailed(text)
	return rules
}

var windowPattern = regexp.MustCompile(`\b(last|past)\s+(?:(\d+)\s*(minutes?|mins?|hours?|h)|(?:an?\s+)?hour)\b`)
var grammarPattern = regexp.MustCompile(`\b(` + positiveActions + `|on|only|exclusively|nothing|else|and|or|the|a|an|any|all|object|objects|with|without|no|from|in|of|to|more|please|not|at)\b`)
var wordPattern = regexp.MustCompile(`[a-z][a-z0-9-]*`)

// nearShorePattern scopes a clause to the harbor-scene cameras unless it
// names cameras itself; a negated phrase is left unparsed so readback warns.
var nearShorePattern = regexp.MustCompile(`\b(?:near(?:-|\s+the\s+|\s*)shore|inshore|close (?:in|to (?:the )?shore)|coastal|littoral|harbou?rs?)\b`)
var negatedShorePattern = regexp.MustCompile(`\b(?:not|never|away from|far from)\s+$`)

func ParseDirectiveDetailed(text string) ([]Rule, []string) {
	return parseDirectiveDetailed(text, false)
}

var edgeConceptPatterns = []classPattern{
	{"sailboat", regexp.MustCompile(`\b(sail\s?boats?|sailing (boats?|vessels?|yachts?)|yachts?|sloops?|catamarans?|schooners?)\b`)},
	{"marine mammal", regexp.MustCompile(`\b(marine mammals?|(?:killer |humpback )?whales?|dolphins?|porpoises?|orcas?|cetaceans?)\b`)},
}

func parseDirectiveDetailed(text string, edgeSubtypes bool) ([]Rule, []string) {
	rules, unknown, _ := parseOpen(text, edgeSubtypes)
	return rules, unknown
}

// parseOpen parses free-form text: normalizeDirective first rewrites everyday
// phrasing into the parser vocabulary and reports nearest-class mappings.
func parseOpen(text string, edgeSubtypes bool) ([]Rule, []string, []ResearchResolution) {
	normal := normalizeDirective(text, edgeSubtypes)
	text = normal.text
	text = regexp.MustCompile(`fixed[- ]wing`).ReplaceAllString(text, "military aircraft or civil aircraft")
	text = andActionPattern.ReplaceAllString(text, "; $1")
	clauses := splitPattern.Split(text, -1)
	rules := make([]Rule, 0)
	unknown := make([]string, 0)
	unknownSeen := make(map[string]bool)
	for _, clause := range clauses {
		if strings.TrimSpace(clause) == "" {
			continue
		}
		negative := negativePattern.MatchString(clause)
		only := onlyPattern.MatchString(clause)
		sources := make([]string, 0)
		for _, source := range catalog.Sources {
			id, name := strings.ToLower(source.ID), strings.ToLower(source.Name)
			re := regexp.MustCompile(`\b(?:` + regexp.QuoteMeta(id) + "|" + regexp.QuoteMeta(name) + `)\b`)
			if re.MatchString(clause) {
				sources = append(sources, source.ID)
				clause = re.ReplaceAllString(clause, " ")
			}
		}
		if loc := nearShorePattern.FindStringIndex(clause); loc != nil && !negatedShorePattern.MatchString(clause[:loc[0]]) {
			if len(sources) == 0 {
				for _, source := range catalog.Sources {
					if source.Scene == "harbor" {
						sources = append(sources, source.ID)
					}
				}
			}
			clause = nearShorePattern.ReplaceAllString(clause, " ")
		}
		within := 0
		if match := windowPattern.FindStringSubmatch(clause); match != nil {
			within = 60
			if match[2] != "" {
				within, _ = strconv.Atoi(match[2])
				if strings.HasPrefix(match[3], "h") {
					within *= 60
				}
			}
			clause = windowPattern.ReplaceAllString(clause, " ")
		}
		classes := make([]string, 0)
		seen := make(map[string]bool)
		patterns := synonymPatterns
		if edgeSubtypes {
			patterns = append(append([]classPattern{}, edgeConceptPatterns...), patterns...)
		}
		for _, pattern := range patterns {
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
				clause = pattern.re.ReplaceAllString(clause, " ")
			}
		}
		unused := grammarPattern.ReplaceAllString(negativePattern.ReplaceAllString(clause, " "), " ")
		for _, word := range wordPattern.FindAllString(unused, -1) {
			if fillerWords[word] || len(word) < 2 {
				continue
			}
			if !unknownSeen[word] {
				unknown = append(unknown, word)
				unknownSeen[word] = true
			}
		}
		if len(classes) == 0 && len(attrs) == 0 && len(sources) == 0 && within == 0 {
			// "the birds are everywhere, get rid of them": the pronoun points
			// back at the previous clause.
			if negative && len(rules) > 0 && pronounNegativePattern.MatchString(clause) {
				rules[len(rules)-1].Type, rules[len(rules)-1].Only = "suppress", false
			}
			continue
		}
		rules = append(rules, Rule{Type: map[bool]string{true: "suppress", false: "boost"}[negative], Classes: classes, Attrs: attrs, Only: only && !negative, Sources: sources, WithinMin: within})
	}
	if normal.onlyOthers {
		for i := range rules {
			if rules[i].Type == "boost" && len(rules[i].Classes) > 0 {
				rules[i].Only = true
			}
		}
	}
	return rules, unknown, normal.resolutions
}
