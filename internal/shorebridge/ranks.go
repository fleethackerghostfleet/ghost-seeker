package shorebridge

// AssignTiers applies the live shore picture's relative priority bands.
// Input order breaks ties; callers must supply a stable order for offline sets.
func AssignTiers(objects []*Object) { assignTiers(objects) }
