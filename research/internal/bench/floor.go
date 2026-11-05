package bench

import "fmt"

// CheckFloor is the safety floor shared by the regression test and offline tuning.
func CheckFloor(r Result) error {
	if r.CriticalMiss > .01 || r.NDCG < .95 || r.P1Recall < .85 || r.EdgeMissP1 > .01 {
		return fmt.Errorf("%s below floor: critical miss %.3f, NDCG %.3f, P1 recall %.3f, edge miss P1 %.3f", r.Model, r.CriticalMiss, r.NDCG, r.P1Recall, r.EdgeMissP1)
	}
	return nil
}
