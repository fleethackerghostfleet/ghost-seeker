package catalog

// Box is a normalized (0..1) bounding box. Top-down boxes also carry the
// oriented hull geometry used by the renderer.
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Detection is one object reported by the edge detector.
type Detection struct {
	Class string          `json:"cls"`
	Box   Box             `json:"b"`
	Conf  float64         `json:"conf"`
	Attrs map[string]bool `json:"attrs"`
}

// Report is the metadata an edge node uploads alongside a kept frame.
type Report struct {
	Source     string      `json:"source"`
	CapturedAt int64       `json:"captured_at"` // unix ms
	Frame      int         `json:"frame"`
	EdgeScore  float64     `json:"edge_score"`
	Detections []Detection `json:"detections"`
	// Frames captured and dropped by edge triage since the previous upload.
	Captured int `json:"captured"`
	Dropped  int `json:"dropped"`
	// Latencies measured on the edge: inference and uplink transit.
	EdgeMs   int `json:"edge_ms"`
	UplinkMs int `json:"uplink_ms"`
}
