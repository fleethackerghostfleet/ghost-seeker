package command

// BuildMetadata returns the stable, JSON-able metadata record sent ahead of
// an image. Fields unavailable on the simulated sensor are omitted.
func BuildMetadata(item Item) map[string]any {
	record := map[string]any{
		"capture":  map[string]any{"time": item.TS.UTC().Format("2006-01-02T15:04:05.000Z"), "source": item.SourceID, "frame": item.Frame, "record": item.DBID, "imageSize": item.KB * 1024},
		"model":    map[string]any{"detections": metadataDetections(item)},
		"scoring":  map[string]any{"edge": item.Edge, "central": item.Central, "tier": item.Tier, "manualTier": item.Manual, "topReasons": metadataReasons(item)},
		"operator": map[string]any{"manualTier": item.Manual, "reviewed": item.Reviewed, "humanReviewed": item.HumanReviewed},
		"transfer": map[string]any{"metadata": map[string]any{"bytes": item.Upload.MetaBytes, "sent": item.Upload.MetaSent, "done": item.Upload.MetaDone, "doneAt": item.Upload.MetaDoneAt}, "image": map[string]any{"bytes": item.Upload.Bytes, "sent": item.Upload.Sent, "done": item.Upload.Done}},
	}
	if item.Filtered != "" {
		record["scoring"].(map[string]any)["filtered"] = item.Filtered
	}
	if item.DupOf != "" {
		record["scoring"].(map[string]any)["dupOf"] = item.DupOf
	}
	if item.Decision != "" {
		record["operator"].(map[string]any)["decision"] = item.Decision
	}
	return record
}

func metadataReasons(item Item) []string {
	var result []string
	for _, score := range item.Det {
		for _, note := range score.Notes {
			for _, label := range append(append([]string{}, note.Classes...), note.Attrs...) {
				if len(result) < 3 {
					result = append(result, label)
				}
			}
		}
	}
	return result
}

func metadataDetections(item Item) []map[string]any {
	result := make([]map[string]any, 0, len(item.Dets))
	for _, d := range item.Dets {
		attrs := map[string]any{}
		if d.Attrs.Approaching {
			attrs["approaching"] = true
		}
		if d.Attrs.New {
			attrs["new"] = true
		}
		if d.Attrs.Dark {
			attrs["dark"] = true
		}
		if d.Attrs.Moving {
			attrs["moving"] = true
		}
		if d.Attrs.Loitering {
			attrs["loitering"] = true
		}
		v := map[string]any{"class": d.Class, "confidence": d.Conf, "box": d.Box, "attrs": attrs}
		if d.Signal != nil {
			v["signal"] = d.Signal
		}
		if d.Ship != nil {
			v["shipClass"] = d.Ship
		}
		result = append(result, v)
	}
	return result
}
