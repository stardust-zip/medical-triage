// Command queue is TriageOS's queue-service: owns human_triage_queue, the
// SLA timeout sweep, and the nurse-dashboard WebSocket hub (Phase 3 of
// docs/architecture/implementation-plan.md).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	globalHub           = newHub()
	queueChangedMessage = []byte(`{"event":"queue_changed"}`)
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetenv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var %s", key)
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("invalid integer env var %s=%q", key, v)
	}
	return n
}

func main() {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustGetenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	internalSecret := mustGetenv("INTERNAL_SHARED_SECRET")
	gatewaySecret := mustGetenv("GATEWAY_SHARED_SECRET")
	slaMinutes := getenvInt("QUEUE_SLA_MINUTES", 3)
	sweepInterval := time.Duration(getenvInt("QUEUE_SLA_SWEEP_INTERVAL_SECONDS", 30)) * time.Second

	staff := requireStaff(gatewaySecret, "NURSE", "ADMIN", "OWNER")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(pool))
	mux.Handle("POST /internal/queue/items", requireInternalSecret(internalSecret, handleCreateItem(pool)))
	mux.HandleFunc("GET /api/v1/queue/pending", staff(handlePendingQueue(pool, slaMinutes)))
	mux.HandleFunc("POST /api/v1/queue/resolve", staff(handleResolveQueue(pool)))
	mux.HandleFunc("POST /api/v1/queue/check-timeouts", staff(handleCheckTimeouts(pool, slaMinutes)))
	mux.HandleFunc("GET /ws/queue", staff(handleQueueWS(globalHub)))

	go runSLASweepTicker(ctx, pool, slaMinutes, sweepInterval)

	port := getenv("PORT", "8083")
	log.Printf("queue-service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// runSLASweepTicker is the "real schedule (internal ticker/goroutine), not
// only on-demand" requirement from Phase 3 — the on-demand HTTP endpoint
// still exists for a nurse to trigger manually, this just means the sweep
// also happens on its own without anyone polling it.
func runSLASweepTicker(ctx context.Context, pool *pgxpool.Pool, slaMinutes int, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		orgIDs, err := listOrgIDs(ctx, pool)
		if err != nil {
			log.Printf("SLA sweep: failed to list organizations: %v", err)
			continue
		}

		for _, orgID := range orgIDs {
			count, err := markTimedOutItems(ctx, pool, orgID, slaMinutes)
			if err != nil {
				log.Printf("SLA sweep: org=%s failed: %v", orgID, err)
				continue
			}
			if count > 0 {
				log.Printf("SLA sweep: org=%s marked %d item(s) TIMEOUT", orgID, count)
				globalHub.broadcast(orgID, queueChangedMessage)
			}
		}
	}
}
