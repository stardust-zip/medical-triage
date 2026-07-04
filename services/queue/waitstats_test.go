package main

import (
	"testing"
	"time"
)

func TestComputeWaitStatsBelowSLA(t *testing.T) {
	now := time.Now()
	createdAt := now.Add(-1 * time.Minute)

	minutes, breached := computeWaitStats(createdAt, now, 3)

	if breached {
		t.Fatal("expected sla_breached = false for a 1-minute-old item with a 3-minute SLA")
	}
	if minutes < 0.9 || minutes > 1.1 {
		t.Fatalf("minutes_waiting = %v, want ~1.0", minutes)
	}
}

func TestComputeWaitStatsAtSLABoundary(t *testing.T) {
	now := time.Now()
	createdAt := now.Add(-3 * time.Minute)

	_, breached := computeWaitStats(createdAt, now, 3)

	if !breached {
		t.Fatal("expected sla_breached = true when waiting time equals the SLA exactly")
	}
}

func TestComputeWaitStatsPastSLA(t *testing.T) {
	now := time.Now()
	createdAt := now.Add(-10 * time.Minute)

	_, breached := computeWaitStats(createdAt, now, 3)

	if !breached {
		t.Fatal("expected sla_breached = true for an item well past the SLA")
	}
}
