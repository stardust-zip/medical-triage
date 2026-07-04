package main

// Pure SLA-wait computation, split out from the DB-touching handlers so it's
// unit-testable without a live Postgres connection (services/queue has no
// DB in CI — see .github/workflows/ci.yml's go job — so DB-touching code
// only gets confidence from a real run, not `go test`; keeping the actual
// arithmetic pure means it isn't one of those pieces).

import (
	"math"
	"time"
)

func computeWaitStats(createdAt, now time.Time, slaMinutes int) (minutesWaiting float64, slaBreached bool) {
	minutesWaiting = math.Round(now.Sub(createdAt).Minutes()*100) / 100
	slaBreached = minutesWaiting >= float64(slaMinutes)
	return minutesWaiting, slaBreached
}
