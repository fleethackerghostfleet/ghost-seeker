package command

import (
	"sort"
	"strings"

	"github.com/fleethackerghostfleet/ghost-seeker/internal/catalog"
)

const promptRules = `You convert a maritime watch officer's plain-language collection directive into JSON scoring rules for an imagery feed.
Output ONLY one JSON object matching the schema. Never add explanations.

Fields:
- rules: one entry per clause the operator states.
  - type: "boost" for things to prioritize / look for / flag / watch; "suppress" for things to ignore / disregard / skip / deprioritize.
  - classes: ONLY values from CLASSES. Pick the closest class ("trawler" -> "fishing vessel", "go-fast", "skiff", "jet ski", "yacht", "sailboat" -> "small boat", "periscope" or "feather" -> "submarine", "life raft", "floating mine" or "floating object" -> "debris", "people in the water" -> "person", "seagull" -> "bird", "helo" -> "helicopter", "uav" -> "drone", "whale", "dolphin", "orca" -> "marine mammal"). Generic "ships", "vessels" or "masts on the horizon" mean every surface class: warship, cargo ship, tanker, fishing vessel, small boat, submarine. Generic "aircraft" means military aircraft, civil aircraft, helicopter, drone. Leave out things that match no class (whitecaps, sun glint, own ship).
  - attrs: ONLY values from ATTRS, and only when the operator describes that behavior: approaching (inbound, closing), new (newly appeared, entering), dark (no AIS, running dark, unidentified), moving (fast, high speed), loitering (stationary, circling, drifting). Colors, hull markings and equipment are not attrs.
  - only: true only when the operator says "only" / "exclusively" / "nothing else" for a boost rule.
  - sources: sensor ids from SOURCES, only when the operator names that sensor. Unit names like "USV-2" are not sources.
  - withinMin: look-back window in minutes, only for "last N minutes" / "past hour"; otherwise 0.
- durationMin: how long the directive should stay active, in minutes, when stated ("for 6 hours" -> 360, "90 mins" -> 90, "rest of the watch" -> 240, "overnight" -> 720); null if not stated.
Urgency words (flash, urgent, immediate, priority, asap, drop everything) and transmit-posture commands (go quiet, radio silence, emcon, resume normal) do not create rules by themselves. If nothing maps to a class, attr or source, return an empty rules list.
Operators use typos and slang; interpret them. Do not invent classes, attrs or durations the operator did not ask for.
Nearest classes for things with no exact class: missile boat, fast attack craft, patrol boat, gunboat, Type 022 or landing craft -> "small boat" and "warship"; bogey or bandit -> the four aircraft classes with attr "dark"; loitering munition or kamikaze drone -> "drone" (no attr); unidentified or unknown contacts -> "unknown" (not attr "dark").
When the operator names no object type ("what's new from the drone", "anything near shore in the last 20 minutes", "everything approaching"), leave classes empty and keep only the attrs, sources or withinMin they stated.
Sensor phrases: drone/UAV feed -> UAV-3; harbor entrance, thermal or IR camera -> IR-HE; pier or mast camera -> EO-P7; buoy sensor or B-12 -> BY-12; sky cam or air station -> SKY-1; satellite or SAR pass -> SAR-7; "near shore", "in the harbor" or "over the harbor" -> EO-P7 and IR-HE. "Arriving", "entering" or "just appeared" is attr "new", not "approaching".`

var promptExamples = []struct {
	text   string
	answer string
}{
	{"eyes out for red fishin boats approaching, urgent, ignore seagulls", `{"rules":[{"type":"boost","classes":["fishing vessel"],"attrs":["approaching"],"only":false,"sources":[],"withinMin":0},{"type":"suppress","classes":["bird"],"attrs":[],"only":false,"sources":[],"withinMin":0}],"durationMin":null}`},
	{"any persicopes = flash, for 2 hrs", `{"rules":[{"type":"boost","classes":["submarine"],"attrs":[],"only":false,"sources":[],"withinMin":0}],"durationMin":120}`},
	{"go quiet", `{"rules":[],"durationMin":null}`},
	{"what's new from the drone in the last half hour?", `{"rules":[{"type":"boost","classes":[],"attrs":["new"],"only":false,"sources":["UAV-3"],"withinMin":30}],"durationMin":null}`},
	{"anything that looks like a missile boat heading our way", `{"rules":[{"type":"boost","classes":["small boat","warship"],"attrs":["approaching"],"only":false,"sources":[],"withinMin":0}],"durationMin":null}`},
}

func llmSystemPrompt() string {
	classes := make([]string, 0, len(catalog.Classes))
	for class := range catalog.Classes {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	sources := make([]string, 0, len(catalog.Sources))
	for _, source := range catalog.Sources {
		sources = append(sources, source.ID+" ("+source.Name+")")
	}
	return promptRules + "\n\nCLASSES: " + strings.Join(classes, ", ") + "\nATTRS: " + strings.Join(attrNames(), ", ") + "\nSOURCES: " + strings.Join(sources, ", ")
}

func attrNames() []string {
	names := make([]string, 0, len(attributePatterns))
	for _, pattern := range attributePatterns {
		names = append(names, pattern.name)
	}
	return names
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func llmMessages(text string) []chatMessage {
	messages := []chatMessage{{Role: "system", Content: llmSystemPrompt()}}
	for _, example := range promptExamples {
		messages = append(messages, chatMessage{Role: "user", Content: example.text}, chatMessage{Role: "assistant", Content: example.answer})
	}
	return append(messages, chatMessage{Role: "user", Content: text})
}

// llmSchema constrains decoding to the Rule shape parse.go produces, with
// classes, attrs and sources limited to canonical catalog values.
func llmSchema() map[string]any {
	classes := make([]string, 0, len(catalog.Classes))
	for class := range catalog.Classes {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	sources := make([]string, 0, len(catalog.Sources))
	for _, source := range catalog.Sources {
		sources = append(sources, source.ID)
	}
	enumList := func(values []string, max int) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": values}, "maxItems": max}
	}
	rule := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type":      map[string]any{"type": "string", "enum": []string{"boost", "suppress"}},
			"classes":   enumList(classes, len(classes)),
			"attrs":     enumList(attrNames(), len(attributePatterns)),
			"only":      map[string]any{"type": "boolean"},
			"sources":   enumList(sources, len(sources)),
			"withinMin": map[string]any{"type": "integer", "minimum": 0, "maximum": 1440},
		},
		"required": []string{"type", "classes", "attrs", "only", "sources", "withinMin"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"rules":       map[string]any{"type": "array", "items": rule, "maxItems": 8},
			"durationMin": map[string]any{"anyOf": []any{map[string]any{"type": "integer", "minimum": 1, "maximum": 10080}, map[string]any{"type": "null"}}},
		},
		"required": []string{"rules", "durationMin"},
	}
}
