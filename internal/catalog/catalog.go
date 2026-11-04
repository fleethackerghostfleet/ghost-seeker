package catalog

import (
	_ "embed"
	"encoding/json"
)

//go:embed imagery.json
var imageryJSON []byte

type Source struct {
	Bands []string `json:"bands,omitempty"`
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Scene string   `json:"scene"`
	Mode  string   `json:"mode"`
	Loc   string   `json:"loc"`
	HW    string   `json:"hw"`
	EM    string   `json:"em"`
	Link  string   `json:"link"`
	Rate  string   `json:"rate"`
}

type Class struct {
	Group  string  `json:"g"`
	Length float64 `json:"L"`
	Aspect float64 `json:"ar"`
	Label  string  `json:"lab"`
}

type Model struct {
	Name string             `json:"name"`
	Desc string             `json:"desc"`
	W    map[string]float64 `json:"w"`
}

type LibraryImage struct {
	File string       `json:"file"`
	Src  string       `json:"src"`
	Dets []LibraryDet `json:"dets"`
}

type LibraryDet struct {
	Cls string `json:"cls"`
	B   Rect   `json:"b"`
}

type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var Library map[string][]LibraryImage

func init() {
	var images []LibraryImage
	if err := json.Unmarshal(imageryJSON, &images); err != nil {
		panic(err)
	}
	Library = make(map[string][]LibraryImage)
	for _, image := range images {
		Library[image.Src] = append(Library[image.Src], image)
	}
}

var Sources = []Source{
	{ID: "EO-P7", Name: "EO Mast Cam · Pier 7", Scene: "harbor", Mode: "eo", Bands: []string{"EO", "IR"}, Loc: "Harbor, Pier 7", HW: "Jetson Orin Nano", EM: "YOLOv8-n int8 · 3 coarse classes", Link: "Private 5G · 40 Mbps", Rate: "2 fps"},
	{ID: "IR-HE", Name: "IR Thermal · Harbor Entrance", Scene: "harbor", Mode: "ir", Loc: "Harbor entrance channel", HW: "Jetson Orin Nano", EM: "YOLOv8-n int8 · 3 coarse classes", Link: "Private 5G · 40 Mbps", Rate: "2 fps"},
	{ID: "BY-12", Name: "Buoy Sensor B-12 · Offshore", Scene: "open", Mode: "eo", Loc: "Offshore, 30 nm E", HW: "Coral Edge TPU", EM: "MobileNet-SSD · 3 coarse classes", Link: "Iridium · 64 kbps", Rate: "1 frame / 5 s"},
	{ID: "UAV-3", Name: "UAV-3 Gimbal · Patrol Orbit", Scene: "aerial", Mode: "eo", Loc: "Patrol orbit, Sector 4", HW: "Qualcomm RB5", EM: "YOLOv8-n int8 · 3 coarse classes", Link: "MANET · 2 Mbps", Rate: "1 fps"},
	{ID: "SKY-1", Name: "Sky Cam · Air Station Approach", Scene: "sky", Mode: "eo", Loc: "Runway 28 approach", HW: "Jetson Orin Nano", EM: "YOLOv8-n int8 · 3 coarse classes", Link: "Fiber · 1 Gbps", Rate: "2 fps"},
	{ID: "SAR-7", Name: "Commercial SAR Pass · Sector 7", Scene: "sat", Mode: "sar", Loc: "Sector 7 strip", HW: "On-orbit FPGA", EM: "CFAR + tiny CNN", Link: "Ground-station downlink", Rate: "per pass"},
}

var Classes = map[string]Class{
	"warship":           {Group: "vessel", Length: .34, Aspect: .3, Label: "Warship"},
	"cargo ship":        {Group: "vessel", Length: .4, Aspect: .3, Label: "Cargo ship"},
	"tanker":            {Group: "vessel", Length: .42, Aspect: .22, Label: "Tanker"},
	"fishing vessel":    {Group: "vessel", Length: .15, Aspect: .6, Label: "Fishing vessel"},
	"small boat":        {Group: "vessel", Length: .08, Aspect: .45, Label: "Small boat"},
	"submarine":         {Group: "vessel", Length: .13, Aspect: .35, Label: "Submarine"},
	"military aircraft": {Group: "aircraft", Length: .14, Aspect: .42, Label: "Military aircraft"},
	"civil aircraft":    {Group: "aircraft", Length: .18, Aspect: .36, Label: "Civil aircraft"},
	"helicopter":        {Group: "aircraft", Length: .11, Aspect: .5, Label: "Helicopter"},
	"drone":             {Group: "other", Length: .05, Aspect: .6, Label: "Drone / UAS"},
	"person":            {Group: "other", Length: .035, Aspect: .8, Label: "Person in water"},
	"debris":            {Group: "other", Length: .06, Aspect: .5, Label: "Debris"},
	"bird":              {Group: "clutter", Length: .035, Aspect: .5, Label: "Bird"},
	"buoy":              {Group: "clutter", Length: .025, Aspect: 1.6, Label: "Buoy"},
}

var Air = map[string]bool{"military aircraft": true, "civil aircraft": true, "helicopter": true, "drone": true, "bird": true}

var Pools = map[string]map[string]float64{
	"harbor": {"cargo ship": 3, "tanker": 2, "small boat": 4, "fishing vessel": 2, "warship": 1.4, "person": .5, "bird": 3, "buoy": 2, "helicopter": .8, "drone": .8},
	"open":   {"cargo ship": 3, "tanker": 2, "warship": 1.2, "submarine": .5, "fishing vessel": 2, "small boat": 2, "debris": 1, "bird": 2, "military aircraft": .8, "helicopter": .6},
	"aerial": {"small boat": 4, "cargo ship": 2, "fishing vessel": 3, "warship": 1, "debris": 1.2, "person": .7, "submarine": .4, "tanker": 1},
	"sky":    {"civil aircraft": 3, "military aircraft": 2, "helicopter": 1.5, "drone": 1.5, "bird": 4},
	"sat":    {"cargo ship": 3, "tanker": 2, "warship": 1, "fishing vessel": 2, "small boat": 1.5},
}

var Models = map[string]Model{
	"maritime": {
		Name: "Maritime OOI v1 (default)", Desc: "Default command model. Scores ships, aircraft and other objects of interest; boosts contacts that are approaching or not broadcasting AIS.",
		W: map[string]float64{"submarine": 1, "warship": .95, "military aircraft": .9, "drone": .85, "person": .8, "helicopter": .7, "small boat": .55, "tanker": .55, "cargo ship": .5, "fishing vessel": .35, "civil aircraft": .35, "debris": .3, "buoy": .08, "bird": .04},
	},
	"air": {
		Name: "Air Picture Focus", Desc: "Weights airborne contacts (military, rotary, UAS) above surface traffic.",
		W: map[string]float64{"military aircraft": 1, "drone": .95, "helicopter": .85, "civil aircraft": .6, "submarine": .6, "warship": .55, "small boat": .3, "person": .4, "tanker": .25, "cargo ship": .25, "fishing vessel": .15, "debris": .1, "buoy": .04, "bird": .08},
	},
	"sar": {
		Name: "Search & Rescue", Desc: "Prioritizes people in the water, debris and small craft for rescue tasking.",
		W: map[string]float64{"person": 1, "debris": .85, "small boat": .7, "fishing vessel": .55, "helicopter": .5, "buoy": .2, "warship": .2, "cargo ship": .25, "tanker": .25, "submarine": .2, "military aircraft": .2, "civil aircraft": .2, "drone": .3, "bird": .03},
	},
}

var EdgeW = map[string]float64{"vessel": .6, "aircraft": .65, "other": .45, "clutter": .12}

var AttrLabels = map[string]string{"approaching": "approaching", "new": "new / changed", "dark": "no AIS", "moving": "moving fast", "loitering": "loitering"}

var Examples = []string{
	"Small boats approaching the pier at speed; ignore birds",
	"Ships not broadcasting AIS",
	"New aircraft entering the area",
	"Any person in the water or debris",
	"Loitering vessels near the harbor entrance",
	"Only drones and helicopters",
}
