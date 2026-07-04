// Command scheduling is TriageOS's scheduling-service: owns departments,
// doctors, clinics, and appointments — booking-conflict checks and
// idempotent booking included (Phase 4 of
// docs/architecture/implementation-plan.md).
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
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

func main() {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, mustGetenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	internalSecret := mustGetenv("INTERNAL_SHARED_SECRET")
	gatewaySecret := mustGetenv("GATEWAY_SHARED_SECRET")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth(pool))
	mux.Handle("GET /internal/scheduling/doctors", requireInternalSecret(internalSecret, handleInternalDoctors(pool)))
	mux.Handle("GET /internal/scheduling/clinics", requireInternalSecret(internalSecret, handleInternalClinics(pool)))
	mux.Handle("POST /internal/scheduling/appointments", requireInternalSecret(internalSecret, handleInternalAppointments(pool)))
	mux.Handle("POST /api/v1/appointments", requirePatient(gatewaySecret, handlePublicAppointments(pool)))

	port := getenv("PORT", "8084")
	log.Printf("scheduling-service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
