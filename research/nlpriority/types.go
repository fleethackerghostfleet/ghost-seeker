// Package nlpriority is an isolated research adapter around the production
// scorer and scheduler. Production packages must never import it.
package nlpriority

type Rule struct {
	Concept string  `json:"concept"`
	Action  string  `json:"action"`
	Weight  float64 `json:"weight"`
}

type Draft struct {
	Rules       []Rule              `json:"rules"`
	Vocabulary  []string            `json:"vocabulary"`
	Readback    string              `json:"readback"`
	Path        string              `json:"path"`
	Operation   string              `json:"operation,omitempty"`
	Resolutions []ConceptResolution `json:"resolutions,omitempty"`
}

// Item retains the original inference values. Experimental classifications
// are applied to scoring copies only, never written over source evidence.
type Item struct {
	ID            string    `json:"id"`
	Label         string    `json:"label"`
	Confidence    float32   `json:"confidence"`
	Subtype       string    `json:"subtype,omitempty"`
	Unknownness   float32   `json:"unknownness"`
	Embedding     []float32 `json:"embedding,omitempty"`
	ModelID       string    `json:"modelId,omitempty"`
	ImageURL      string    `json:"imageUrl,omitempty"`
	CapturedAt    int64     `json:"capturedAtUnixMs"`
	Source        string    `json:"source,omitempty"`
	LabelOverride string    `json:"labelOverride,omitempty"`
	OverrideBy    string    `json:"overrideBy,omitempty"`
	TomerScore    *float64  `json:"tomerScore,omitempty"`
	ThumbBytes    int64     `json:"thumbBytes,omitempty"`
	FullBytes     int64     `json:"fullBytes,omitempty"`
}

type Match struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Similarity   float64 `json:"similarity"`
	Margin       float64 `json:"margin"`
	Accepted     bool    `json:"accepted"`
	Experimental bool    `json:"experimental"`
	Reason       string  `json:"reason,omitempty"`
}

type Reason struct {
	Scorer string  `json:"scorer"`
	Value  float64 `json:"value"`
	Note   string  `json:"note"`
}

type RankedItem struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Subtype           string   `json:"subtype,omitempty"`
	ExperimentalLabel string   `json:"experimentalLabel,omitempty"`
	Similarity        float64  `json:"similarity,omitempty"`
	Abstention        string   `json:"abstention,omitempty"`
	Priority          float64  `json:"priority"`
	Suppressed        bool     `json:"suppressed"`
	Reasons           []Reason `json:"reasons"`
	ImageURL          string   `json:"imageUrl,omitempty"`
	Unknownness       float32  `json:"unknownness"`
	LabelOverride     string   `json:"labelOverride,omitempty"`
}

type Delivery struct {
	ID       string `json:"id"`
	Form     string `json:"form"`
	Bytes    int64  `json:"bytes"`
	Tick     int    `json:"tick"`
	Complete bool   `json:"complete"`
}

type Evaluation struct {
	Items            []RankedItem `json:"items"`
	Delivery         []Delivery   `json:"delivery"`
	BandwidthBPS     float64      `json:"bandwidthBps"`
	SimulatedSeconds int          `json:"simulatedSeconds"`
}

type Preview struct {
	PreviewID string     `json:"previewId"`
	Revision  uint64     `json:"revision"`
	Engine    string     `json:"engine"`
	Draft     Draft      `json:"draft"`
	Before    Evaluation `json:"before"`
	After     Evaluation `json:"after"`
	Warnings  []string   `json:"warnings"`
	ElapsedMS int64      `json:"elapsedMs"`
	createdAt int64
}

type State struct {
	Revision         uint64       `json:"revision"`
	Engine           string       `json:"engine"`
	Rules            []Rule       `json:"rules"`
	Vocabulary       []string     `json:"vocabulary"`
	Items            []RankedItem `json:"items"`
	HistoryDepth     int          `json:"historyDepth"`
	LLMConfigured    bool         `json:"llmConfigured"`
	VisionConfigured bool         `json:"visionConfigured"`
}

var BaseVocabulary = []string{"cargo ship", "tanker", "container ship", "fishing boat", "sailboat", "speedboat", "passenger ship", "warship", "submarine", "coast guard", "buoy", "aircraft", "marine mammal"}
