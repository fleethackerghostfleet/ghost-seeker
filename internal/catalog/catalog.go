package catalog

import "encoding/json"

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
	// Lift raises a contact's weight by closing this share of the gap to 1 when it shows the attribute.
	Lift map[string]float64 `json:"lift"`
	// LiftGroups lists the class groups the attribute lifts apply to.
	LiftGroups []string `json:"liftGroups"`
}

type LibraryImage struct {
	Background bool         `json:"background,omitempty"`
	File       string       `json:"file"`
	Src        string       `json:"src"`
	Dets       []LibraryDet `json:"dets"`
}

// ShipClass is an imagery-derived ship-type classification of one detection (edgeml/src/edgeml/embed/shiptype.py).
type ShipClass struct {
	Type     string   `json:"type"`               // cargo|container|tanker|passenger|fishing|tug|sailboat|small_boat|warship|submarine|coast_guard|unknown
	Label    string   `json:"label"`              // display label, e.g. "Tanker"
	Conf     float64  `json:"conf"`               // 0..1 top-1 probability
	Aspect   string   `json:"aspect,omitempty"`   // bow|stern|beam|quarter
	Size     string   `json:"size,omitempty"`     // small|medium|large
	Features []string `json:"features,omitempty"` // e.g. "radar mast", "cranes", "helicopter deck", "gun mount"
	Markings string   `json:"markings,omitempty"` // "visible" | "not visible" (never a name/hull number)
	Model    string   `json:"model,omitempty"`    // which model produced it
}

type LibraryDet struct {
	Cls  string     `json:"cls"`
	B    Rect       `json:"b"`
	Ship *ShipClass `json:"ship,omitempty"`
}

type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var Library map[string][]LibraryImage

func init() {
	for _, model := range Models {
		model.W["unknown"] = .7
		model.W["vessel"] = model.W["cargo ship"]
		model.W["aircraft"] = model.W["military aircraft"]
	}
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
	"unknown":           {Group: "unknown", Length: .08, Aspect: .6, Label: "UNKNOWN — not classified"},
	"warship":           {Group: "vessel", Length: .34, Aspect: .3, Label: "Warship"},
	"cargo ship":        {Group: "vessel", Length: .4, Aspect: .3, Label: "Cargo ship"},
	"tanker":            {Group: "vessel", Length: .42, Aspect: .22, Label: "Tanker"},
	"fishing vessel":    {Group: "vessel", Length: .15, Aspect: .6, Label: "Fishing vessel"},
	"small boat":        {Group: "vessel", Length: .08, Aspect: .45, Label: "Small boat"},
	"vessel":            {Group: "vessel", Length: .4, Aspect: .3, Label: "Vessel — type not classified"},
	"submarine":         {Group: "vessel", Length: .13, Aspect: .35, Label: "Submarine"},
	"aircraft":          {Group: "aircraft", Length: .18, Aspect: .36, Label: "Aircraft — type not classified"},
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
		Name: "Maritime scoring model (default)", Desc: "Scores ships, aircraft and other objects of interest. Raises the priority of objects that approach, stay in one area or do not broadcast their identity (AIS).",
		W:          map[string]float64{"submarine": 1, "person": 1, "warship": .75, "military aircraft": .75, "drone": .75, "small boat": .55, "helicopter": .55, "debris": .35, "tanker": .3, "cargo ship": .3, "fishing vessel": .3, "civil aircraft": .3, "buoy": .05, "bird": .03},
		Lift:       map[string]float64{"approaching": .45, "dark": .5, "loitering": .4},
		LiftGroups: []string{"vessel", "aircraft", "other"},
	},
	"air": {
		Name: "Aircraft focus", Desc: "Gives aircraft, helicopters and drones higher scores than ships. Raises the priority of aircraft that approach or do not broadcast their identity.",
		W:          map[string]float64{"military aircraft": 1, "drone": .75, "helicopter": .75, "submarine": .75, "warship": .5, "civil aircraft": .3, "person": .3, "small boat": .2, "tanker": .15, "cargo ship": .15, "fishing vessel": .15, "debris": .1, "bird": .05, "buoy": .03},
		Lift:       map[string]float64{"approaching": .45, "dark": .8, "loitering": .4},
		LiftGroups: []string{"aircraft", "other"},
	},
	"sar": {
		Name: "Search & Rescue", Desc: "Prioritizes people in the water, debris and small craft for rescue tasking; raises drifting or non-reporting craft.",
		W:          map[string]float64{"person": 1, "debris": .55, "small boat": .5, "helicopter": .45, "fishing vessel": .35, "cargo ship": .2, "tanker": .2, "warship": .15, "submarine": .15, "military aircraft": .15, "civil aircraft": .15, "drone": .2, "buoy": .1, "bird": .03},
		Lift:       map[string]float64{"dark": .4, "loitering": .5},
		LiftGroups: []string{"vessel", "other"},
	},
}

var EdgeW = map[string]float64{"vessel": .6, "aircraft": .65, "other": .45, "clutter": .12, "unknown": .65}

var AttrLabels = map[string]string{"approaching": "approaching", "new": "new / changed", "dark": "no AIS", "moving": "moving fast", "loitering": "loitering", "transient": "single-frame / noise-like"}

var Examples = []string{
	"Small boats approaching the pier at speed; ignore birds",
	"Ships not broadcasting AIS",
	"New aircraft entering the area",
	"Any person in the water or debris",
	"Loitering vessels near the harbor entrance",
	"Only drones and helicopters",
}
