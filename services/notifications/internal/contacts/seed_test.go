package contacts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"portal-paciente-clinica/services/notifications/internal/service"
)

func TestMinorWithoutEmailNotifiesOnlyResponsible(t *testing.T) {
	got, err := Seed{}.Emails(context.Background(), "pat-003")
	if err != nil || !reflect.DeepEqual(got, []string{"paciente1@example.test"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestUnknownPatientHasNoContact(t *testing.T) {
	if _, err := (Seed{}).Emails(context.Background(), "nope"); !errors.Is(err, service.ErrNoContact) {
		t.Fatalf("want ErrNoContact, got %v", err)
	}
}
