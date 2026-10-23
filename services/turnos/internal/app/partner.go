package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sort"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Capacidad publicada a otros grupos (ADR-008): reservas con duenio,
// idempotencia por clave y cobro por reserva.

var ErrIdempotencyConflict = errors.New("idempotency key reused with different data")

// PartnerCancelReason es el reasonCode del evento cuando cancela un consumidor externo.
const PartnerCancelReason = "partner_request"

// Pricing es el precio por reserva vigente para consumidores externos.
type Pricing struct {
	Cents    int64
	Currency string
}

// PartnerBooking registra que un consumidor externo creo el turno: duenio,
// clave de idempotencia, huella de los datos y precio cobrado en ese momento.
type PartnerBooking struct {
	AppointmentID  string
	Consumer       string
	IdempotencyKey string
	Fingerprint    string
	PriceCents     int64
	Currency       string
	BookedAt       time.Time
}

type UsageItem struct {
	AppointmentID string
	BookedAt      time.Time
	PriceCents    int64
	Currency      string
	Status        domain.Status
}

type UsageTotal struct {
	Currency    string
	Bookings    int
	AmountCents int64
}

// Usage es el resumen de cobro de un consumidor para el periodo [From, To).
type Usage struct {
	Consumer string
	From, To time.Time
	Items    []UsageItem
	Totals   []UsageTotal
}

// IdempotencyLockKey identifica la fila de bloqueo de una clave de consumidor.
// Se bloquea junto con medico y paciente, en el mismo orden estable, para que
// dos solicitudes con la misma clave no creen dos turnos.
func IdempotencyLockKey(consumer, key string) string {
	sum := sha256.Sum256([]byte(consumer + "\x00" + key))
	return "idem:" + hex.EncodeToString(sum[:])
}

// fingerprint resume los datos de la reserva; el instante se normaliza a UTC
// para que la misma hora en otra zona se considere la misma solicitud.
func fingerprint(patientID, doctorID string, startAt time.Time) string {
	sum := sha256.Sum256([]byte(patientID + "\x00" + doctorID + "\x00" + startAt.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

// BookForPartner reserva en nombre de un consumidor externo. Repetir la clave
// con los mismos datos devuelve el mismo turno (replayed); con otros datos,
// ErrIdempotencyConflict. Un rechazo de negocio no queda asociado a la clave.
func (s *Service) BookForPartner(ctx context.Context, consumer, key, patientID, doctorID string, startAt time.Time) (domain.Appointment, bool, error) {
	pb := PartnerBooking{Consumer: consumer, IdempotencyKey: key, Fingerprint: fingerprint(patientID, doctorID, startAt),
		PriceCents: s.Price.Cents, Currency: s.Price.Currency}
	// Una repeticion no vuelve a evaluar directorio ni anticipacion: el turno ya existe.
	prev, err := s.Store.PartnerBookingByKey(ctx, consumer, key)
	switch {
	case err == nil:
		if prev.Fingerprint != pb.Fingerprint {
			return domain.Appointment{}, false, ErrIdempotencyConflict
		}
		a, err := s.Store.Get(ctx, prev.AppointmentID)
		return a, err == nil, err
	case !errors.Is(err, ErrNotFound):
		return domain.Appointment{}, false, err
	}
	a, replayed, err := s.book(ctx, patientID, doctorID, startAt, &pb)
	if err == nil && !replayed {
		slog.InfoContext(ctx, "partner booking billed", "event", "partner.booking_billed", "consumer", consumer,
			"appointment_id", a.ID, "price_cents", pb.PriceCents, "currency", pb.Currency)
	}
	return a, replayed, err
}

// owned devuelve ErrNotFound si el turno no existe o pertenece a otro
// consumidor, sin revelar cual de los dos casos ocurrio.
func (s *Service) owned(ctx context.Context, consumer, id string) error {
	pb, err := s.Store.PartnerBookingOf(ctx, id)
	if errors.Is(err, ErrNotFound) || (err == nil && pb.Consumer != consumer) {
		return ErrNotFound
	}
	return err
}

func (s *Service) GetForPartner(ctx context.Context, consumer, id string) (domain.Appointment, error) {
	if err := s.owned(ctx, consumer, id); err != nil {
		return domain.Appointment{}, err
	}
	return s.Store.Get(ctx, id)
}

func (s *Service) CancelForPartner(ctx context.Context, consumer, id string) (domain.Appointment, error) {
	if err := s.owned(ctx, consumer, id); err != nil {
		return domain.Appointment{}, err
	}
	return s.Cancel(ctx, id, PartnerCancelReason)
}

// Usage resume las reservas cobradas al consumidor en el mes que empieza en
// month (UTC). Cada reserva conserva el precio vigente al crearla y una
// cancelacion posterior no la descuenta.
func (s *Service) Usage(ctx context.Context, consumer string, month time.Time) (Usage, error) {
	from := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	u := Usage{Consumer: consumer, From: from, To: from.AddDate(0, 1, 0)}
	items, err := s.Store.PartnerUsage(ctx, consumer, u.From, u.To)
	if err != nil {
		return Usage{}, err
	}
	u.Items = items
	byCurrency := map[string]*UsageTotal{}
	for _, it := range items {
		t, ok := byCurrency[it.Currency]
		if !ok {
			t = &UsageTotal{Currency: it.Currency}
			byCurrency[it.Currency] = t
		}
		t.Bookings++
		t.AmountCents += it.PriceCents
	}
	for _, t := range byCurrency {
		u.Totals = append(u.Totals, *t)
	}
	sort.Slice(u.Totals, func(i, j int) bool { return u.Totals[i].Currency < u.Totals[j].Currency })
	return u, nil
}

func (s *Service) Doctors(ctx context.Context) ([]domain.Doctor, error) {
	return s.Directory.Doctors(ctx)
}
