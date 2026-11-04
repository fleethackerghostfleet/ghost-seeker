// Package catalog holds the shared sensor, object-class and model definitions
// used by the edge simulator and the command server.
package catalog

// Source is an edge sensor that reports triaged imagery.
type Source struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Scene     string `json:"scene"`
	Mode      string `json:"mode"`
	Loc       string `json:"loc"`
	Hardware  string `json:"hw"`
	EdgeModel string `json:"em"`
	Link      string `json:"link"`
	Rate      string `json:"rate"`
}

var Sources = []Source{
	{"EO-P7", "EO Mast Cam · Pier 7", "harbor", "eo", "Harbor, Pier 7", "Jetson Orin Nano", "YOLOv8-n int8 · 3 coarse classes", "Private 5G · 40 Mbps", "2 fps"},
	{"IR-HE", "IR Thermal · Harbor Entrance", "harbor", "ir", "Harbor entrance channel", "Jetson Orin Nano", "YOLOv8-n int8 · 3 coarse classes", "Private 5G · 40 Mbps", "2 fps"},
	{"BY-12", "Buoy Sensor B-12 · Offshore", "open", "eo", "Offshore, 30 nm E", "Coral Edge TPU", "MobileNet-SSD · 3 coarse classes", "Iridium · 64 kbps", "1 frame / 5 s"},
	{"UAV-3", "UAV-3 Gimbal · Patrol Orbit", "aerial", "eo", "Patrol orbit, Sector 4", "Qualcomm RB5", "YOLOv8-n int8 · 3 coarse classes", "MANET · 2 Mbps", "1 fps"},
	{"SKY-1", "Sky Cam · Air Station Approach", "sky", "eo", "Runway 28 approach", "Jetson Orin Nano", "YOLOv8-n int8 · 3 coarse classes", "Fiber · 1 Gbps", "2 fps"},
	{"SAR-7", "Commercial SAR Pass · Sector 7", "sat", "sar", "Sector 7 strip", "On-orbit FPGA", "CFAR + tiny CNN", "Ground-station downlink", "per pass"},
}

// Attrs are the behavior flags an edge model may report on a detection.
var Attrs = map[string]bool{"approaching": true, "moving": true, "dark": true, "loitering": true, "new": true}

func SourceByID(id string) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// Class describes an object class: its coarse group, nominal size and label.
type Class struct {
	Group  string  `json:"g"`
	Length float64 `json:"-"`
	Aspect float64 `json:"-"`
	Label  string  `json:"lab"`
}

var ClassOrder = []string{"warship", "cargo ship", "tanker", "fishing vessel", "small boat", "submarine", "military aircraft", "civil aircraft", "helicopter", "drone", "person", "debris", "bird", "buoy"}

var Classes = map[string]Class{
	"warship":           {"vessel", .34, .3, "Warship"},
	"cargo ship":        {"vessel", .4, .3, "Cargo ship"},
	"tanker":            {"vessel", .42, .22, "Tanker"},
	"fishing vessel":    {"vessel", .15, .6, "Fishing vessel"},
	"small boat":        {"vessel", .08, .45, "Small boat"},
	"submarine":         {"vessel", .13, .35, "Submarine"},
	"military aircraft": {"aircraft", .14, .42, "Military aircraft"},
	"civil aircraft":    {"aircraft", .18, .36, "Civil aircraft"},
	"helicopter":        {"aircraft", .11, .5, "Helicopter"},
	"drone":             {"other", .05, .6, "Drone / UAS"},
	"person":            {"other", .035, .8, "Person in water"},
	"debris":            {"other", .06, .5, "Debris"},
	"bird":              {"clutter", .035, .5, "Bird"},
	"buoy":              {"clutter", .025, 1.6, "Buoy"},
}

// Airborne classes are drawn above the horizon in side-view scenes.
var Airborne = map[string]bool{"military aircraft": true, "civil aircraft": true, "helicopter": true, "drone": true, "bird": true}

// Attrs are behavior flags attached to each detection by the edge tracker.
var AttrOrder = []string{"approaching", "new", "dark", "moving", "loitering"}

// Model is a command-level prioritization model: a weight per object class.
type Model struct {
	Name    string             `json:"name"`
	Desc    string             `json:"desc"`
	Weights map[string]float64 `json:"-"`
}

var ModelOrder = []string{"maritime", "air", "sar"}

var Models = map[string]Model{
	"maritime": {"Maritime OOI v1 (default)", "Default command model. Scores ships, aircraft and other objects of interest; boosts contacts that are approaching or not broadcasting AIS.",
		map[string]float64{"submarine": 1, "warship": .95, "military aircraft": .9, "drone": .85, "person": .8, "helicopter": .7, "small boat": .55, "tanker": .55, "cargo ship": .5, "fishing vessel": .35, "civil aircraft": .35, "debris": .3, "buoy": .08, "bird": .04}},
	"air": {"Air Picture Focus", "Weights airborne contacts (military, rotary, UAS) above surface traffic.",
		map[string]float64{"military aircraft": 1, "drone": .95, "helicopter": .85, "civil aircraft": .6, "submarine": .6, "warship": .55, "small boat": .3, "person": .4, "tanker": .25, "cargo ship": .25, "fishing vessel": .15, "debris": .1, "buoy": .04, "bird": .08}},
	"sar": {"Search & Rescue", "Prioritizes people in the water, debris and small craft for rescue tasking.",
		map[string]float64{"person": 1, "debris": .85, "small boat": .7, "fishing vessel": .55, "helicopter": .5, "buoy": .2, "warship": .2, "cargo ship": .25, "tanker": .25, "submarine": .2, "military aircraft": .2, "civil aircraft": .2, "drone": .3, "bird": .03}},
}

// EdgeWeights are the coarse per-group weights the low-compute edge model uses.
var EdgeWeights = map[string]float64{"vessel": .6, "aircraft": .65, "other": .45, "clutter": .12}

// EdgeKeepThreshold is the edge triage score below which frames are dropped on the sensor.
const EdgeKeepThreshold = 0.2
