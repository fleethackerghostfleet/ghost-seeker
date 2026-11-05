package nlpriority

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

func validateSavedMatches(r revision, items []Item) error {
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	allowed := map[string]bool{}
	for _, rule := range r.Rules {
		allowed[rule.Concept] = true
	}
	for _, label := range BaseVocabulary {
		allowed[label] = true
	}
	for id, m := range r.Matches {
		it, ok := byID[id]
		if !ok || m.ID != id || len(it.Embedding) == 0 || it.ModelID == "" {
			return errors.New("saved classification lacks its original image embedding")
		}
		if math.IsNaN(m.Similarity) || math.IsInf(m.Similarity, 0) || m.Similarity < -1.001 || m.Similarity > 1.001 || math.IsNaN(m.Margin) || math.IsInf(m.Margin, 0) {
			return errors.New("invalid saved classification score")
		}
		if m.Accepted && !allowed[m.Label] {
			return errors.New("saved classification is outside its vocabulary revision")
		}
	}
	return nil
}

func validateItems(items []Item, now time.Time) error {
	seen := map[string]bool{}
	modelID := ""
	dimension := 0
	for i := range items {
		it := &items[i]
		if !safeID.MatchString(it.ID) || seen[it.ID] {
			return errors.New("item IDs must be unique and contain only letters, numbers, dot, dash or underscore")
		}
		seen[it.ID] = true
		if it.Confidence < 0 || it.Confidence > 1 || math.IsNaN(float64(it.Confidence)) || it.Unknownness < 0 || it.Unknownness > 1 || math.IsNaN(float64(it.Unknownness)) {
			return errors.New("confidence and unknownness must be in [0,1]")
		}
		if it.TomerScore != nil && (math.IsNaN(*it.TomerScore) || math.IsInf(*it.TomerScore, 0) || *it.TomerScore < 0 || *it.TomerScore > 1) {
			return errors.New("tomerScore must be in [0,1]")
		}
		if len(it.Label) > 128 || len(it.Subtype) > 128 || len(it.LabelOverride) > 128 || len(it.Source) > 128 || len(it.ImageURL) > 2<<20 {
			return errors.New("item text field exceeds limit")
		}
		if strings.Contains(it.Label, "__nl_") || strings.Contains(it.Subtype, "__nl_") || strings.Contains(it.LabelOverride, "__nl_") {
			return errors.New("reserved research concept marker in source label")
		}
		if len(it.Embedding) > 0 {
			if len(it.Embedding) < 2 || len(it.Embedding) > 2048 || it.ModelID == "" {
				return errors.New("embedding requires modelId and 2–2048 dimensions")
			}
			if modelID == "" {
				modelID = it.ModelID
				dimension = len(it.Embedding)
			}
			if it.ModelID != modelID || len(it.Embedding) != dimension {
				return errors.New("incompatible image encoders in queue")
			}
			var norm float64
			for _, v := range it.Embedding {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					return errors.New("nonfinite embedding")
				}
				norm += float64(v) * float64(v)
			}
			if norm < 1e-12 {
				return errors.New("zero image embedding")
			}
		}
		if it.CapturedAt == 0 {
			it.CapturedAt = now.UnixMilli()
		}
		if it.ThumbBytes == 0 {
			it.ThumbBytes = 16384
		}
		if it.FullBytes == 0 {
			it.FullBytes = 131072
		}
		if it.ThumbBytes < 256 || it.ThumbBytes > 8<<20 || it.FullBytes < it.ThumbBytes || it.FullBytes > 64<<20 {
			return errors.New("invalid image payload sizes")
		}
	}
	return nil
}
