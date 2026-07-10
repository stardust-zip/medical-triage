package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// GET /internal/scheduling/doctors, GET /internal/scheduling/clinics
//
// Called server-to-server by the monolith's triage pipeline
// (services/triage/triage/agent.py::get_doctors_by_department / get_clinics_by_department) when
// the resolve_and_get_booking_info tool needs booking options for a
// department — never routed through api-gateway, protected by
// X-Internal-Secret instead.
// ---------------------------------------------------------------------------

type doctorResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Specialty      string `json:"specialty"`
	DepartmentCode string `json:"department_code"`
}

func handleInternalDoctors(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := r.URL.Query().Get("org_id")
		departmentCode := r.URL.Query().Get("department_code")
		if orgID == "" || departmentCode == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "org_id and department_code are required")
			return
		}

		rows, err := getDoctorsByDepartment(r.Context(), pool, orgID, departmentCode)
		if err != nil {
			log.Printf("get doctors failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not fetch doctors")
			return
		}

		doctors := make([]doctorResponse, 0, len(rows))
		for _, d := range rows {
			doctors = append(doctors, doctorResponse{
				ID: d.ID, Name: d.Name, Specialty: d.Specialty, DepartmentCode: d.DepartmentCode,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"doctors": doctors})
	}
}

type clinicResponse struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func handleInternalClinics(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := r.URL.Query().Get("org_id")
		departmentCode := r.URL.Query().Get("department_code")
		if orgID == "" || departmentCode == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "org_id and department_code are required")
			return
		}

		rows, err := getClinicsByDepartment(r.Context(), pool, orgID, departmentCode)
		if err != nil {
			log.Printf("get clinics failed: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not fetch clinics")
			return
		}

		clinics := make([]clinicResponse, 0, len(rows))
		for _, c := range rows {
			clinics = append(clinics, clinicResponse{Name: c.Name, Address: c.Address})
		}
		writeJSON(w, http.StatusOK, map[string]any{"clinics": clinics})
	}
}

// ---------------------------------------------------------------------------
// Appointment booking — shared by two entry points with different trust
// boundaries: the public, patient-facing POST /api/v1/appointments (gateway
// routed) and an internal one the monolith's book_appointment chat tool
// calls directly. Both funnel into the same bookAppointment helper so the
// idempotency/double-booking behavior is identical either way.
// ---------------------------------------------------------------------------

type appointmentResponse struct {
	Success       bool   `json:"success"`
	AppointmentID string `json:"appointment_id"`
	Message       string `json:"message"`
}

func bookAppointment(
	w http.ResponseWriter,
	r *http.Request,
	pool *pgxpool.Pool,
	orgID, patientSessionID, doctorID, departmentCode, appointmentTime string,
	idempotencyKey *string,
) {
	id, reused, err := createAppointment(
		r.Context(), pool, orgID, patientSessionID, doctorID, departmentCode, appointmentTime, idempotencyKey,
	)
	if errors.Is(err, errDoubleBooked) {
		writeJSONError(w, http.StatusConflict, "DOUBLE_BOOKED", "this doctor already has an appointment at that time")
		return
	}
	if err != nil {
		log.Printf("create appointment failed: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "DB_ERROR", "could not book appointment")
		return
	}

	status, body := appointmentHTTPResult(id, appointmentTime, reused)
	writeJSON(w, status, body)
}

// appointmentHTTPResult is the pure "given a successful booking outcome,
// what does the HTTP response look like" step, split out from
// bookAppointment so it's unit-testable without a database (see
// handlers_test.go) — mirrors why services/queue/waitstats.go exists as its
// own file.
func appointmentHTTPResult(id, appointmentTime string, reused bool) (int, appointmentResponse) {
	if reused {
		return http.StatusOK, appointmentResponse{
			Success:       true,
			AppointmentID: id,
			Message:       "Lịch hẹn này đã được đặt trước đó vào " + appointmentTime + ".",
		}
	}
	return http.StatusCreated, appointmentResponse{
		Success:       true,
		AppointmentID: id,
		Message: "Lịch hẹn đã được đặt thành công vào " + appointmentTime +
			". Vui lòng đến đúng giờ và mang theo CMND/CCCD.",
	}
}

type publicAppointmentRequest struct {
	DoctorID        string `json:"doctor_id"`
	DepartmentCode  string `json:"department_code"`
	AppointmentTime string `json:"appointment_time"`
}

// handlePublicAppointments backs POST /api/v1/appointments, reached only
// through api-gateway with a verified patient session. Per §5.1 of the
// implementation plan, the Idempotency-Key header is required here — a
// client silently retrying a booking is exactly the case Phase 4 exists to
// make safe.
func handlePublicAppointments(pool *pgxpool.Pool) func(http.ResponseWriter, *http.Request, patientContext) {
	return func(w http.ResponseWriter, r *http.Request, ctx patientContext) {
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Idempotency-Key header is required")
			return
		}

		var body publicAppointmentRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.DoctorID == "" || body.DepartmentCode == "" || body.AppointmentTime == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"doctor_id, department_code and appointment_time are required")
			return
		}

		bookAppointment(
			w, r, pool,
			ctx.OrgID, ctx.PatientSessionID, body.DoctorID, body.DepartmentCode, body.AppointmentTime,
			&idempotencyKey,
		)
	}
}

type internalAppointmentRequest struct {
	OrgID            string  `json:"org_id"`
	PatientSessionID string  `json:"patient_session_id"`
	DoctorID         string  `json:"doctor_id"`
	DepartmentCode   string  `json:"department_code"`
	AppointmentTime  string  `json:"appointment_time"`
	IdempotencyKey   *string `json:"idempotency_key"`
}

// handleInternalAppointments backs the book_appointment chat tool — the
// monolith calls this directly (X-Internal-Secret) rather than through
// api-gateway, since it's a server-to-server call, not a browser request.
func handleInternalAppointments(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body internalAppointmentRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			body.OrgID == "" || body.PatientSessionID == "" || body.DoctorID == "" ||
			body.DepartmentCode == "" || body.AppointmentTime == "" {
			writeJSONError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"org_id, patient_session_id, doctor_id, department_code and appointment_time are required")
			return
		}

		bookAppointment(
			w, r, pool,
			body.OrgID, body.PatientSessionID, body.DoctorID, body.DepartmentCode, body.AppointmentTime,
			body.IdempotencyKey,
		)
	}
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func handleHealth(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
	}
}
