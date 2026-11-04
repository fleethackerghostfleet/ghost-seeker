package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed colregs_lights.json
var lightRulesJSON []byte

//go:embed nightlights.json
var nightJSON []byte

type LightPattern struct {
	Layout   string   `json:"layout"`
	Colours  []string `json:"colours"`
	Signal   string   `json:"signal"`
	Select   []string `json:"select"`
	Isolated bool     `json:"isolated"`
}

type LightRule struct {
	ID       string       `json:"id"`
	Pattern  LightPattern `json:"pattern"`
	Flashing *bool        `json:"flashing"`
	Ref      string       `json:"ref"`
	Meaning  string       `json:"meaning"`
	Line     string       `json:"line"`
	Severity string       `json:"severity"`
	Boost    float64      `json:"priority_boost"`
}

type ObservedLight struct {
	Colour   string  `json:"colour"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Flashing bool    `json:"flashing"`
}

type NightDetection struct {
	Cls            string          `json:"cls"`
	B              Rect            `json:"b"`
	Lights         []ObservedLight `json:"lights"`
	Signal         string          `json:"signal"`
	Assisted       bool            `json:"signal_assisted"`
	Interpretation struct {
		RuleID string `json:"rule_id"`
	} `json:"interpretation"`
}

type NightImage struct {
	File  string           `json:"file"`
	Src   string           `json:"src"`
	Night *bool            `json:"night"`
	Lum   float64          `json:"lum"`
	Basis string           `json:"basis"`
	Dets  []NightDetection `json:"dets"`
}

var LightRules []LightRule
var NightLibrary map[string][]NightImage

func init() {
	if err := json.Unmarshal(lightRulesJSON, &LightRules); err != nil {
		panic(err)
	}
	var data struct {
		Images []NightImage `json:"nightLibrary"`
	}
	if err := json.Unmarshal(nightJSON, &data); err != nil {
		panic(err)
	}
	NightLibrary = make(map[string][]NightImage)
	for _, image := range data.Images {
		NightLibrary[image.Src] = append(NightLibrary[image.Src], image)
	}
}
