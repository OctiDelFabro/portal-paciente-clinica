// Package domain contiene las reglas de reserva y cancelacion de turnos
// (SPEC RF-02 y RF-03). No depende de HTTP, MySQL ni RabbitMQ.
package domain

import (
	"errors"
	"time"
)

// MinLeadTime es la anticipacion minima para reservar o cancelar.
const MinLeadTime = 24 * time.Hour

type Status string

const (
	StatusBooked    Status = "booked"
	StatusCancelled Status = "cancelled"
)

var (
	ErrLeadTime         = errors.New("lead time shorter than 24 hours")
	ErrOverlap          = errors.New("slot overlaps an active appointment of the doctor or the patient")
	ErrAlreadyCancelled = errors.New("appointment already cancelled")
	ErrPatientNotActive = errors.New("patient not enabled by reception")
	ErrInvalidDuration  = errors.New("doctor has no valid appointment duration")
)

type Appointment struct {
	ID           string
	PatientID    string
	DoctorID     string
	StartAt      time.Time
	EndAt        time.Time
	Status       Status
	Version      int
	CancelReason string
}

// Overlaps aplica la interseccion de intervalos semiabiertos [inicio, fin):
// un turno que termina a las 10:00 no se superpone con otro que empieza a las 10:00.
func (a Appointment) Overlaps(start, end time.Time) bool {
	return a.Status == StatusBooked && a.StartAt.Before(end) && start.Before(a.EndAt)
}

// Doctor reune los datos de agenda que Turnos necesita del profesional.
type Doctor struct {
	ID       string
	Duration time.Duration
}

// Patient reune el estado de habilitacion definido en RF-13.
type Patient struct {
	ID      string
	Enabled bool
}

// Book valida una nueva reserva contra los turnos activos del medico y del
// paciente que se superponen con el intervalo pedido. El llamador debe obtener
// esos turnos bajo el bloqueo de ambos recursos para que la decision sea valida.
func Book(id string, patient Patient, doctor Doctor, startAt, now time.Time, existing []Appointment) (Appointment, error) {
	if !patient.Enabled {
		return Appointment{}, ErrPatientNotActive
	}
	if doctor.Duration <= 0 {
		return Appointment{}, ErrInvalidDuration
	}
	if startAt.Sub(now) < MinLeadTime {
		return Appointment{}, ErrLeadTime
	}
	endAt := startAt.Add(doctor.Duration)
	for _, other := range existing {
		if (other.DoctorID == doctor.ID || other.PatientID == patient.ID) && other.Overlaps(startAt, endAt) {
			return Appointment{}, ErrOverlap
		}
	}
	return Appointment{ID: id, PatientID: patient.ID, DoctorID: doctor.ID, StartAt: startAt.UTC(), EndAt: endAt.UTC(), Status: StatusBooked, Version: 1}, nil
}

// Cancel aplica la anticipacion minima de paciente y recepcion. El bloqueo de
// agenda por el medico (RF-07) es una excepcion que no pasa por esta regla.
func (a Appointment) Cancel(reason string, now time.Time) (Appointment, error) {
	if a.Status == StatusCancelled {
		return a, ErrAlreadyCancelled
	}
	if a.StartAt.Sub(now) < MinLeadTime {
		return a, ErrLeadTime
	}
	a.Status = StatusCancelled
	a.CancelReason = reason
	a.Version++
	return a, nil
}
