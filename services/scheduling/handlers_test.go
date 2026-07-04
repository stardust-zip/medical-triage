package main

import (
	"net/http"
	"testing"
)

func TestAppointmentHTTPResultForNewBooking(t *testing.T) {
	status, body := appointmentHTTPResult("appt-1", "2026-04-10T08:00:00+07:00", false)

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want %d", status, http.StatusCreated)
	}
	if !body.Success || body.AppointmentID != "appt-1" {
		t.Fatalf("body = %+v", body)
	}
}

func TestAppointmentHTTPResultForReusedIdempotentBooking(t *testing.T) {
	status, body := appointmentHTTPResult("appt-1", "2026-04-10T08:00:00+07:00", true)

	// A retried request must not look like a fresh booking was created.
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (reused booking is not a 201)", status, http.StatusOK)
	}
	if !body.Success || body.AppointmentID != "appt-1" {
		t.Fatalf("body = %+v", body)
	}
}
