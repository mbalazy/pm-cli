//go:build !darwin && !linux

package storage

import "time"

// processStartTime has no portable implementation outside darwin/linux (the two
// platforms pm runs on: the mac checkouts and the VPS runner). Reporting "not
// available" makes every caller degrade to bare pid liveness, i.e. exactly the
// behaviour that shipped before the second criterion existed.
func processStartTime(int) (time.Time, bool) {
	return time.Time{}, false
}
