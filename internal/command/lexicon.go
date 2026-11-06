package command

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

// The open-language lexicon rewrites everyday phrasing into the vocabulary
// parse.go already understands, so the offline parser handles free-form
// directives without an LLM. Concepts without an exact catalog class map to
// the nearest classes and are reported back as resolutions.

type concept struct {
	phrases []string
	classes []string
	attrs   []string
	// approx marks a nearest-class mapping the readback must disclose.
	approx bool
}

var conceptLexicon = []concept{
	{phrases: []string{"fast attack craft", "fast attack boat", "fac", "fiac", "missile boat", "gunboat", "gun boat", "torpedo boat", "patrol boat", "patrol craft", "type 022", "houbei", "houbei class", "landing craft", "amphibious craft", "hovercraft"}, classes: []string{"small boat", "warship"}, approx: true},
	{phrases: []string{"pirate", "pirate skiff", "pirate boat", "smuggler", "smuggling boat", "smuggler boat", "drone boat", "unmanned surface vessel", "boat swarm", "swarm boat", "dhow", "tug", "tugboat", "water taxi"}, classes: []string{"small boat"}, approx: true},
	{phrases: []string{"rib", "rhib", "zodiac", "panga", "jetski", "pwc", "personal watercraft", "kayak", "canoe", "rowboat", "row boat", "motorboat", "motor boat", "powerboat", "power boat", "pleasure craft", "cabin cruiser", "gofast", "go fast boat", "go-fast boat", "sail", "small vessel", "small craft"}, classes: []string{"small boat"}},
	{phrases: []string{"aircraft carrier", "carrier", "amphib", "amphibious ship", "assault ship", "battleship", "minesweeper", "mine hunter", "minehunter", "ddg", "ffg", "man-of-war", "man of war", "naval ship", "navy ship", "naval vessel", "combat ship", "enemy warship"}, classes: []string{"warship"}},
	{phrases: []string{"enemy ship", "hostile ship", "enemy vessel", "hostile vessel", "coast guard cutter", "cutter"}, classes: []string{"warship"}, approx: true},
	{phrases: []string{"snorkel", "snorkel mast", "u-boat", "uboat", "ssn", "ssk", "ssbn"}, classes: []string{"submarine"}},
	{phrases: []string{"semi-submersible", "semi submersible", "narco sub", "narco-sub", "feather"}, classes: []string{"submarine"}, approx: true},
	{phrases: []string{"oil tanker", "lng carrier", "lpg carrier", "gas carrier", "vlcc", "crude carrier", "chemical tanker", "supertanker", "tanker ship"}, classes: []string{"tanker"}},
	{phrases: []string{"freighter", "container vessel", "containership", "boxship", "box ship", "bulk carrier", "bulker", "merchantman", "car carrier", "ro-ro", "roro", "coaster", "cargo boat", "ore carrier"}, classes: []string{"cargo ship"}},
	{phrases: []string{"barge", "cruise ship", "cruise liner", "ocean liner", "ferry", "ferries", "passenger ship", "passenger vessel"}, classes: []string{"cargo ship"}, approx: true},
	{phrases: []string{"fishing boat", "fishing trawler", "fishing fleet", "longliner", "long liner", "seiner", "purse seiner", "dragger", "crabber", "shrimper", "shrimp boat", "fisherman", "fishermen", "fishing ship"}, classes: []string{"fishing vessel"}},
	{phrases: []string{"fighter", "fighter jet", "bomber", "poseidon", "maritime patrol aircraft", "mpa", "warplane", "war plane", "military plane", "military jet", "f-18", "f/a-18", "super hornet", "f-35", "mig", "sukhoi", "awacs", "gunship"}, classes: []string{"military aircraft"}},
	{phrases: []string{"missile", "cruise missile", "anti-ship missile", "anti ship missile"}, classes: []string{"military aircraft", "drone"}, approx: true},
	{phrases: []string{"commercial airliner", "commercial plane", "commercial jet", "passenger jet", "passenger plane", "cessna", "private plane", "private jet", "bizjet", "business jet", "light aircraft", "small plane", "seaplane", "floatplane", "float plane", "crop duster", "civilian plane", "civilian aircraft", "civilian jet"}, classes: []string{"civil aircraft"}},
	{phrases: []string{"blimp", "balloon", "airship"}, classes: []string{"civil aircraft"}, approx: true},
	{phrases: []string{"rotary wing", "rotary-wing", "rotorcraft", "seahawk", "sea hawk", "mh-60", "sh-60", "black hawk", "blackhawk", "helicopter traffic"}, classes: []string{"helicopter"}},
	{phrases: []string{"quadcopter", "multicopter", "multirotor", "unmanned aircraft", "rpa", "uas", "suas"}, classes: []string{"drone"}},
	{phrases: []string{"loitering munition", "shahed", "kamikaze drone", "suicide drone"}, classes: []string{"drone"}, approx: true},
	{phrases: []string{"person in the water", "persons in the water", "people in the water", "anyone in the water", "anybody in the water", "someone in the water", "somebody in the water", "sailor overboard", "crew overboard", "piw", "castaway", "body in the water", "bodies in the water", "victim", "survivor", "swimmer", "diver"}, classes: []string{"person"}},
	{phrases: []string{"seabird", "sea bird", "cormorant", "albatross", "gannet", "tern", "flock of birds", "flock", "avian"}, classes: []string{"bird"}},
	{phrases: []string{"channel marker", "marker buoy", "navigation buoy", "nav aid", "mooring ball", "lobster pot", "crab pot"}, classes: []string{"buoy"}},
	{phrases: []string{"trash", "garbage", "junk", "jetsam", "driftwood", "floating log", "wreck", "container overboard", "raft", "liferaft", "floating debris"}, classes: []string{"debris"}},
	{phrases: []string{"mine", "sea mine", "naval mine", "floating mine"}, classes: []string{"debris"}, approx: true},
	{phrases: []string{"unknowns", "unknown object", "unknown contact", "unknown track", "mystery contact", "uncorrelated contact", "unidentified contact", "unidentified object", "unidentified track"}, classes: []string{"unknown"}},
	{phrases: []string{"bogey", "bogie", "bandit"}, classes: []string{"military aircraft", "civil aircraft", "helicopter", "drone"}, attrs: []string{"dark"}, approx: true},
	{phrases: []string{"whale", "killer whale", "humpback", "dolphin", "porpoise", "orca", "seal", "sea lion", "manatee", "cetacean", "marine mammal"}, classes: []string{"marine mammal"}},
}

// Phrases that rewrite to canonical words the base parser knows.
var genericRewrites = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b(?:anything|things?|something|stuff|objects?|contacts?) (?:flying|in the air|in the sky|airborne)\b`), " aircraft "},
	{regexp.MustCompile(`\b(?:air contacts?|air traffic|flying objects?)\b`), " aircraft "},
	{regexp.MustCompile(`\b(?:anything|contacts?|something|traffic) on the (?:water|surface)\b|\bmasts?(?: on the horizon)?\b|\bhulls (?:on|at|over|near)\b|\bshipping\b`), " vessels "},
	{regexp.MustCompile(`\b(?:people|persons?|crew|men) on deck\b`), " "},
	{regexp.MustCompile(`\blow[- ]flying\b`), " "},
	{regexp.MustCompile(`\b(?:nothing|none|no one|nobody)(?: else)? but\b|\bsolely\b|\bstrictly\b|\bexclusive(?:ly)?\b`), " only "},
}

var attrRewrites = []struct {
	re   *regexp.Regexp
	attr string
}{
	{regexp.MustCompile(`\b(?:heading|coming|headed) (?:our way|this way|in|at us|for us|closer)\b|\bcoming (?:at|toward\w*|for)\b|\bbearing down\b|\bclosing in\b|\bconverging\b|\bon (?:an )?intercept\b|\bmaking for\b`), "approaching"},
	{regexp.MustCompile(`\bhanging(?: (?:around|about|out))?\b|\bsitting (?:still|around|there)\b|\blurking\b|\bparked\b|\bhovering\b|\bstation[- ]?keeping\b|\bnot moving\b|\bdead in the water\b|\bmilling (?:around|about)\b|\bidling\b|\borbiting\b|\bstaying put\b`), "loitering"},
	{regexp.MustCompile(`\b(?:with )?(?:their |its |the )?ais (?:turned |switched )?(?:off|disabled|dark)\b|\bnot (?:transmitting|squawking)\b|\brunning (?:dark|silent)\b|\btransponders? off\b|\bgoing dark\b|\bgone dark\b`), "dark"},
	{regexp.MustCompile(`\bzooming\b|\bspeeding\b|\bfast[- ]moving\b|\bhauling\b|\bat speed\b|\bhigh speed\b`), "moving"},
	{regexp.MustCompile(`\bjust (?:appeared|arrived|showed up)\b|\bfresh\b|\blatest\b|\bpopping up\b`), "new"},
}

// ownShipPattern finds references to the operator's own ship or formation;
// "toward us" means approaching, "near the ship" is context only.
var ownShipPattern = regexp.MustCompile(`\b(toward|towards|to|at|on|for|approaching|approach|closing on|nearing|near|by|around|alongside|close to|next to|beside)\s+(?:the |our |my )?(?:own ?ship|ship|fleet|carrier|strike group|task force|convoy|formation|battle ?group|us|me|our position|my position|our way)\b`)
var bareOwnShipPattern = regexp.MustCompile(`\b(?:own ?ship|our (?:own )?ship)\b`)

var sourceAliases = []struct {
	re *regexp.Regexp
	id string
}{
	{regexp.MustCompile(`\b(?:from|by|on|via|through|with|off) (?:the )?(?:drone|uav|uas)(?:'s)?(?: (?:feed|camera|cam|gimbal|footage|imagery|video|sensor))?\b`), "from uav-3"},
	{regexp.MustCompile(`\b(?:the )?(?:drone|uav)(?:'s)? (?:feed|camera|cam|gimbal|footage|imagery|video|sensor)s?\b|\buav[- ]?3\b|\bpatrol orbit\b|\bsector 4\b`), "uav-3"},
	{regexp.MustCompile(`\bbuoy (?:sensor |camera |cam )?b-?12\b|\bb-?12\b|\bbuoy (?:sensor|camera|cam)s?\b|\boffshore (?:buoy|sensor|camera|cam)s?\b`), "by-12"},
	{regexp.MustCompile(`\b(?:ir )?(?:thermal )?(?:· )?harbou?r (?:entrance|mouth)(?: (?:camera|cam|channel))?\b|\bentrance channel\b|\b(?:ir|thermal|infrared|flir)(?: (?:camera|cam|imager|sensor|feed))s?\b|\b(?:ir )?(?:thermal|infrared|flir)\b`), "ir-he"},
	{regexp.MustCompile(`\b(?:at|by|near|around|off) (?:the )?pier\b`), "from eo-p7"},
	{regexp.MustCompile(`\bpier (?:7|seven)(?: (?:camera|cam))?\b|\bpier (?:camera|cam)s?\b|\b(?:eo )?mast (?:camera|cam)s?\b`), "eo-p7"},
	{regexp.MustCompile(`\bsky ?(?:cam|camera)s?\b|\bair station(?: approach)?(?: (?:camera|cam))?\b|\brunway(?: 28)?(?: approach)?(?: (?:camera|cam))?\b`), "sky-1"},
	{regexp.MustCompile(`\b(?:commercial )?(?:sar )?(?:satellite|sat)(?: (?:pass(?:es)?|imagery|images?|feed|downlink))?\b|\bsar (?:pass(?:es)?|imagery|images?)\b|\bsector 7\b`), "sar-7"},
}

var negativeRewrites = regexp.MustCompile(`\b(?:disregard|stop (?:showing|sending|flagging|reporting)|(?:don'?t|do not) (?:show|send|flag|report|need|want)(?: to see)?|no more|get rid of|tune out|screen out|weed out|mute|remove|forget(?: about)?|dismiss|nobody cares about|no one cares about|who cares about|(?:less|not) (?:interested in|worried about|concerned (?:about|with))|i'?m not worried about)\b`)
var postNegativeRewrites = regexp.MustCompile(`\b(?:can wait|(?:is|are) not a (?:concern|priority|threat)|not a (?:concern|priority|threat)|(?:is|are) (?:just )?(?:noise|clutter|a nuisance)|just noise|(?:is|are) low priority|low priority|(?:don'?t|doesn'?t|do not|does not) matter|not important|unimportant|irrelevant|not needed|driving me crazy|annoying|cluttering(?: up)?(?: the feed)?)\b`)
var elseClausePattern = regexp.MustCompile(`(?:^|[,;.]|\bbut\b|\band\b)\s*(?:(?:everything|anything|everyone|all|whatever)\s+else|the rest|all (?:the )?others?|other (?:stuff|things|contacts|traffic))\b[^,;.]*`)
var pronounNegativePattern = regexp.MustCompile(`\b(?:them|those|these|it|that|they)\b`)

var windowRewrites = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?(?:half(?:\s+an?)?[\s-]+hour|30\s*min\w*)\b`), "$1 30 minutes"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?quarter(?:\s+of)?(?:\s+an?)?[\s-]+hour\b`), "$1 15 minutes"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?(?:hour and a half|(?:one|1) and a half hours?|90 min\w*)\b`), "$1 90 minutes"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?couple(?:\s+of)?\s+hours\b`), "$1 2 hours"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?few\s+hours\b`), "$1 3 hours"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?(?:few|couple(?:\s+of)?)\s+min\w*\b`), "$1 10 minutes"},
	{regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?(\d+)\s*(?:hrs?)\b`), "$1 $2 hours"},
}

var wordNumbers = map[string]string{"one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "ten": "10", "fifteen": "15", "twenty": "20", "thirty": "30", "forty": "40", "forty-five": "45", "sixty": "60", "ninety": "90"}
var wordWindowPattern = regexp.MustCompile(`\b(last|past)\s+(?:the\s+)?(one|two|three|four|five|six|ten|fifteen|twenty|thirty|forty-five|forty|sixty|ninety)\s+(min\w*|hours?)\b`)

// fillerWords are function words and directive verbs that never name a
// concept, so the unknown-word readback ignores them.
var fillerWords = wordSet(`lower higher less lesser greater top bottom i me my mine we us our ours you your it its them they their those these this that there here is are was were be been being am do does did doing done have has had got get gets gotten can could would should will shall may might must want wants need needs wanna know see seen show shows showing keep keeping look looks looking like likes eye eyes tabs out up off about around over under into onto by for from near toward towards what whats which who whom whose when where why how anything something everything nothing anyone anybody someone somebody everyone everybody stuff things thing contacts contact objects targets target tracks track tracking sightings sighting spotted sighted picked detected appear right now today tonight currently please pls plz thanks thank really very just most more much many some few lot lots care cares caring mostly also too either neither so if then than as while still yet even ever priority priorities prio important urgent urgently asap immediately immediate flash routine high low feed feeds cam cams camera cameras sensor sensors imagery images image pics pic picture pictures photo photos video videos report reports reporting alert alerts notify tell let give send find spot watch watching monitor flag flagging highlight search hunt detect matter matters wait going go heading headed coming running crazy driving okay ok hey guys team sir captain skipper area waters water sea ocean horizon side ahead nearby close closer far away kind sort type types resemble resembles resembling similar possible possibly potential probable likely maybe perhaps suspected any all every each both the a an of to in on at with without and or not no nor but else only own another other others rest s d ll re ve t m isn aren don doesn didn wasn weren concern concerns noise clutter worried interested attention eyes-on lookout lifting traffic pass passing passes moving moves move floating flying fly lights light around anything-else think thinks thought mean means`)

func wordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, word := range strings.Fields(words) {
		set[word] = true
	}
	return set
}

type compiledConcept struct {
	re      *regexp.Regexp
	concept concept
	phrase  string
}

var (
	simConcepts  = compileConcepts(false)
	edgeConcepts = compileConcepts(true)
	lexiconVocab = buildVocab()
)

func phrasePattern(phrases []string) *regexp.Regexp {
	sorted := append([]string{}, phrases...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	parts := make([]string, 0, len(sorted))
	for _, phrase := range sorted {
		words := strings.Fields(phrase)
		for i, word := range words {
			words[i] = regexp.QuoteMeta(word)
		}
		last := words[len(words)-1]
		if strings.HasSuffix(last, "y") && len(last) > 3 {
			words[len(words)-1] = last[:len(last)-1] + "(?:y|ies|ys)"
		} else {
			words[len(words)-1] = last + "(?:'?s|es)?"
		}
		parts = append(parts, strings.Join(words, `[\s-]+`))
	}
	return regexp.MustCompile(`\b(?:` + strings.Join(parts, "|") + `)\b`)
}

func compileConcepts(edge bool) []compiledConcept {
	entries := make([]compiledConcept, 0, len(conceptLexicon))
	for _, entry := range conceptLexicon {
		for _, phrase := range entry.phrases {
			// Live shore mode matches edge subtypes (sailboat, speedboat, ...)
			// through catalog.Aliases, so keep those phrases for it.
			if alias, ok := catalog.Aliases[phrase]; edge && ok && catalog.EdgeSubtypes[alias] {
				continue
			}
			entries = append(entries, compiledConcept{re: phrasePattern([]string{phrase}), concept: entry, phrase: phrase})
		}
	}
	// Longest phrases first across concepts ("lng carrier" before "carrier").
	sort.SliceStable(entries, func(i, j int) bool { return len(entries[i].phrase) > len(entries[j].phrase) })
	return entries
}

// coreVocab lists the words the base parser's regexes already know.
const coreVocab = `unknown unidentified unclassified warship warships destroyer destroyers frigate frigates cruiser cruisers corvette corvettes combatant combatants navy naval military submarine submarines periscope periscopes submersible submersibles tanker tankers oiler oilers cargo container freighter freighters merchant merchants bulk carrier carriers fishing trawler trawlers fishermen sailboat sailboats sailing yacht yachts sloop catamaran catamarans schooner small boats skiff skiffs speedboat speedboats dinghy dinghies craft aircraft planes airplane airplanes fighters bombers patrol warplanes airliner airliners civilian commercial passenger private helicopter helicopters choppers rotorcraft drones quadcopters unmanned person persons people swimmers overboard divers survivors humans birds seagull seagulls pelican pelicans wildlife buoys channel markers navigation debris wreckage floating flotsam slick ships vessels surface watercraft maritime airborne approaching inbound toward towards closing incoming nearing newly appearing arriving entering changed emerging dark transponder broadcasting unregistered spoofing suspicious moving speed underway rapid racing quickly loitering stationary anchored circling lingering drifting holding ignore deprioritize exclude suppress filter skip hide disregard prioritize boost focus exclusively harbor harbour coastal littoral inshore shore minutes hours`

func buildVocab() map[string]bool {
	vocab := wordSet(coreVocab)
	for _, entry := range conceptLexicon {
		for _, phrase := range entry.phrases {
			for _, word := range strings.FieldsFunc(phrase, func(r rune) bool { return r == ' ' || r == '-' }) {
				vocab[word] = true
				vocab[word+"s"] = true
			}
		}
	}
	for word := range fillerWords {
		vocab[word] = true
	}
	return vocab
}

var tokenPattern = regexp.MustCompile(`[a-z][a-z']*[a-z]`)

// correctSpelling fixes likely typos of lexicon words (edit distance 1, or 2
// for long words) so "submarnies" and "helicoptors" still parse.
func correctSpelling(text string) (string, []ResearchResolution) {
	resolutions := make([]ResearchResolution, 0)
	seen := make(map[string]bool)
	out := tokenPattern.ReplaceAllStringFunc(text, func(word string) string {
		if len(word) < 6 || lexiconVocab[word] || strings.Contains(word, "'") {
			return word
		}
		limit := 1
		if len(word) >= 8 {
			limit = 2
		}
		best, bestDistance, tie := "", limit+1, false
		for candidate := range lexiconVocab {
			if len(candidate) < 4 || abs(len(candidate)-len(word)) > limit {
				continue
			}
			distance := editDistance(word, candidate, limit)
			if distance < bestDistance {
				best, bestDistance, tie = candidate, distance, false
			} else if distance == bestDistance && candidate != best {
				tie = tie || !samePlural(candidate, best)
				if len(candidate) < len(best) {
					best = candidate
				}
			}
		}
		if best == "" || tie || bestDistance > limit {
			return word
		}
		if !seen[word] && !fillerWords[best] {
			resolutions = append(resolutions, ResearchResolution{Input: word, Canonical: best, Reason: "spelling"})
			seen[word] = true
		}
		return best
	})
	return out, resolutions
}

func samePlural(a, b string) bool {
	return a+"s" == b || b+"s" == a
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// editDistance is the optimal-string-alignment distance, cut off at limit+1.
func editDistance(a, b string, limit int) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > limit {
			return limit + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

// normalized is free text rewritten into base-parser vocabulary.
type normalized struct {
	text        string
	onlyOthers  bool
	resolutions []ResearchResolution
}

const placeholderPrefix = "qqz"

func normalizeDirective(text string, edgeSubtypes bool) normalized {
	text = strings.ToLower(text)
	text = strings.NewReplacer("’", "'", "‘", "'", "“", " ", "”", " ", "?", " ", "!", " ", "&", " and ").Replace(text)
	text = regexp.MustCompile(`\bwhat's\b|\bwhats\b`).ReplaceAllString(text, "what is")
	text = regexp.MustCompile(`(\w)'s\b`).ReplaceAllString(text, "$1")
	out := normalized{}
	text, out.resolutions = correctSpelling(text)
	text = normalizeWindows(text)
	// "everything else can wait" narrows to the named contacts; a positive
	// "and other contacts" is left alone.
	text = elseClausePattern.ReplaceAllStringFunc(text, func(match string) string {
		if negativeRewrites.MatchString(match) || postNegativeRewrites.MatchString(match) || negativePattern.MatchString(match) {
			out.onlyOthers = true
			return ","
		}
		return match
	})
	placeholders := make([]string, 0)
	hold := func(value string) string {
		placeholders = append(placeholders, value)
		return " " + placeholderPrefix + strconv.Itoa(len(placeholders)-1) + "q "
	}
	text = negativeRewrites.ReplaceAllStringFunc(text, func(string) string { return hold("ignore") })
	text = postNegativeRewrites.ReplaceAllStringFunc(text, func(string) string { return hold("ignore") })
	for _, source := range catalog.Sources {
		for _, name := range []string{source.Name, source.ID} {
			pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(strings.ToLower(name)) + `\b`)
			text = pattern.ReplaceAllStringFunc(text, func(string) string { return hold(strings.ToLower(source.ID)) })
		}
	}
	for _, alias := range sourceAliases {
		text = alias.re.ReplaceAllStringFunc(text, func(string) string { return hold(alias.id) })
	}
	text = ownShipPattern.ReplaceAllStringFunc(text, func(match string) string {
		switch strings.Fields(match)[0] {
		case "near", "by", "around", "alongside", "close", "next", "beside":
			return " "
		}
		return hold("approaching")
	})
	text = bareOwnShipPattern.ReplaceAllString(text, " ")
	concepts := simConcepts
	if edgeSubtypes {
		concepts = edgeConcepts
	}
	for _, entry := range concepts {
		text = entry.re.ReplaceAllStringFunc(text, func(match string) string {
			classes := make([]string, 0, len(entry.concept.classes))
			for _, class := range entry.concept.classes {
				classes = append(classes, canonicalText[class])
			}
			if entry.concept.approx {
				out.resolutions = appendResolution(out.resolutions, ResearchResolution{Input: strings.TrimSpace(match), Canonical: strings.Join(entry.concept.classes, ", "), Reason: "nearest class"})
			}
			return hold(strings.Join(append(classes, entry.concept.attrs...), " or "))
		})
	}
	for _, rewrite := range genericRewrites {
		text = rewrite.re.ReplaceAllString(text, rewrite.repl)
	}
	for _, rewrite := range attrRewrites {
		text = rewrite.re.ReplaceAllStringFunc(text, func(string) string { return hold(rewrite.attr) })
	}
	for i := len(placeholders) - 1; i >= 0; i-- {
		text = strings.ReplaceAll(text, placeholderPrefix+strconv.Itoa(i)+"q", placeholders[i])
	}
	out.text = strings.Join(strings.Fields(text), " ")
	return out
}

func normalizeWindows(text string) string {
	text = wordWindowPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := wordWindowPattern.FindStringSubmatch(match)
		return parts[1] + " " + wordNumbers[parts[2]] + " " + parts[3]
	})
	for _, rewrite := range windowRewrites {
		text = rewrite.re.ReplaceAllString(text, rewrite.repl)
	}
	return text
}

// canonicalText is the phrase parse.go's synonym patterns read as each class.
var canonicalText = map[string]string{
	"unknown": "unknown contacts", "warship": "warship", "submarine": "submarine", "tanker": "tanker",
	"cargo ship": "cargo ship", "fishing vessel": "fishing vessel", "small boat": "small boat",
	"military aircraft": "military aircraft", "civil aircraft": "civilian aircraft", "helicopter": "helicopter",
	"drone": "drone", "person": "person", "bird": "bird", "buoy": "buoy", "debris": "debris",
	"marine mammal": "marine mammal",
}

func appendResolution(list []ResearchResolution, resolution ResearchResolution) []ResearchResolution {
	for _, existing := range list {
		if existing.Input == resolution.Input {
			return list
		}
	}
	return append(list, resolution)
}

// ResolutionReadback tells the operator how unfamiliar words were read.
func ResolutionReadback(resolutions []ResearchResolution) string {
	return resolutionReadback(resolutions)
}

func resolutionReadback(resolutions []ResearchResolution) string {
	mapped, spelled := make([]string, 0), make([]string, 0)
	for _, resolution := range resolutions {
		if resolution.Reason == "spelling" {
			spelled = append(spelled, fmt.Sprintf("%q as %q", resolution.Input, resolution.Canonical))
		} else {
			mapped = append(mapped, fmt.Sprintf("%q → %s", resolution.Input, resolution.Canonical))
		}
	}
	parts := make([]string, 0, 2)
	if len(mapped) > 0 {
		parts = append(parts, "Mapped "+strings.Join(mapped, "; ")+".")
	}
	if len(spelled) > 0 {
		parts = append(parts, "Read "+strings.Join(spelled, ", ")+".")
	}
	return strings.Join(parts, " ")
}
