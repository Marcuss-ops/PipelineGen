// Package jobs — heartbeat_tracker.go provides an in-memory timestamp
// updated by the broker's Heartbeat() method on the server side. The
// health-check RunnerProbe reads this timestamp to verify the broker
// goroutine is alive (not just the jobs table).
//
// Canonical update site: internal/platform/jobs/local/broker.go::Heartbeat()
// Canonical read site:  internal/app/build_bundles_core.go::buildHealthService()
//
// Staleness threshold (60s): the heartbeat ticker runs every 25s, so a
// healthy broker updates this timestamp at least once every 25s. 60s
// gives 2 full cycles of grace before the probe fails — enough tolerance
// for slow DB writes without producing false positives during normal
// operation.
//
// Set to 0 at package init (zero-value). A 0 timestamp means "no
// heartbeat ever recorded", which the RunnerProbe adapter treats as a
// stale condition (the broker loop has not started yet).
package jobs

import (
	"math"
	"sync/atomic"
	"time"
)

// BrokerLastHeartbeat stores the Unix timestamp (in seconds) of the
// most recent successful broker heartbeat. Updated from the local
// broker's Heartbeat() method; read from the health-check RunnerProbe.
var BrokerLastHeartbeat atomic.Int64

// SetBrokerAlive records the current time as the last known-good
// broker heartbeat. Called from the server-side local broker's
// Heartbeat() after the DB write succeeds.
func SetBrokerAlive() {
	BrokerLastHeartbeat.Store(time.Now().Unix())
}

// BrokerHeartbeatAge returns the number of seconds since the last
// heartbeat, or a large value if no heartbeat has ever been recorded.
func BrokerHeartbeatAge() int64 {
	last := BrokerLastHeartbeat.Load()
	if last == 0 {
		return math.MaxInt64 // sentinel: "no heartbeat ever recorded"
	}
	return time.Now().Unix() - last
}
