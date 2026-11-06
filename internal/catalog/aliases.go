package catalog

import "strings"

// Aliases maps everyday words for an object type to the edge sidecar label (zeroshot.py SUBTYPES) or core detector
// label it means. Lookups match the whole phrase only, so a qualified phrase ("red whale") is never broadened.
var Aliases = map[string]string{
	"whale": "marine mammal", "dolphin": "marine mammal", "porpoise": "marine mammal", "orca": "marine mammal",
	"killer whale": "marine mammal", "humpback": "marine mammal", "humpback whale": "marine mammal",
	"sea lion": "marine mammal", "seal": "marine mammal", "manatee": "marine mammal", "cetacean": "marine mammal",

	"sail boat": "sailboat", "sailing boat": "sailboat", "sailing yacht": "sailboat", "sailing vessel": "sailboat",
	"yacht": "sailboat", "sloop": "sailboat", "catamaran": "sailboat", "schooner": "sailboat",

	"speed boat": "speedboat", "motorboat": "speedboat", "motor boat": "speedboat", "powerboat": "speedboat",
	"power boat": "speedboat", "go-fast": "speedboat", "go fast boat": "speedboat", "jet ski": "speedboat",

	"ferry": "passenger ship", "passenger ferry": "passenger ship", "car ferry": "passenger ship",
	"cruise ship": "passenger ship", "cruise liner": "passenger ship", "ocean liner": "passenger ship",
	"passenger vessel": "passenger ship", "passenger boat": "passenger ship",

	"coastguard": "coast guard", "coast guard cutter": "coast guard", "coast guard boat": "coast guard",
	"coast guard vessel": "coast guard", "coast guard ship": "coast guard", "cutter": "coast guard",

	"container vessel": "container ship", "containership": "container ship", "box ship": "container ship",

	"freighter": "cargo ship", "bulk carrier": "cargo ship", "ore carrier": "cargo ship", "merchant ship": "cargo ship",
	"merchant vessel": "cargo ship", "cargo boat": "cargo ship",

	"oil tanker": "tanker", "tanker ship": "tanker", "lng carrier": "tanker", "gas carrier": "tanker",
	"chemical tanker": "tanker", "supertanker": "tanker",

	"trawler": "fishing boat", "fishing trawler": "fishing boat", "seiner": "fishing boat", "longliner": "fishing boat",
	"fishing ship": "fishing boat",

	"destroyer": "warship", "frigate": "warship", "corvette": "warship", "cruiser": "warship", "battleship": "warship",
	"navy ship": "warship", "naval ship": "warship", "naval vessel": "warship", "aircraft carrier": "warship",

	"sub": "submarine", "periscope": "submarine",

	"plane": "aircraft", "airplane": "aircraft", "aeroplane": "aircraft", "jet": "aircraft", "airliner": "aircraft",
	"chopper": "helicopter", "helo": "helicopter",
	"uav": "drone", "quadcopter": "drone", "unmanned aircraft": "drone",

	"seagull": "bird", "gull": "bird", "pelican": "bird", "albatross": "bird", "cormorant": "bird", "seabird": "bird",

	"navigation buoy": "buoy", "marker buoy": "buoy", "channel marker": "buoy",

	"swimmer": "person", "man overboard": "person in water", "person in the water": "person in water", "mob": "person in water",
}

// CanonicalAlias resolves a whole phrase, or its regular plural, through Aliases.
func CanonicalAlias(phrase string) (string, bool) {
	phrase = strings.Join(strings.Fields(strings.ToLower(phrase)), " ")
	candidates := []string{phrase}
	switch {
	case strings.HasSuffix(phrase, "ies"):
		candidates = append(candidates, strings.TrimSuffix(phrase, "ies")+"y")
	case strings.HasSuffix(phrase, "es"):
		candidates = append(candidates, strings.TrimSuffix(phrase, "es"), strings.TrimSuffix(phrase, "s"))
	case strings.HasSuffix(phrase, "s"):
		candidates = append(candidates, strings.TrimSuffix(phrase, "s"))
	}
	for _, candidate := range candidates {
		if canonical, ok := Aliases[candidate]; ok {
			return canonical, true
		}
	}
	return "", false
}
