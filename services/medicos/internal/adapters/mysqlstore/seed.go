package mysqlstore

import "context"

// SeedSpecialties and SeedDoctors are loaded on every start with INSERT IGNORE:
// existing rows (including admin edits) are never overwritten. Doctor IDs match
// the ones Turnos accepts for bookings (services/turnos/.../directory/seed.go).
var SeedSpecialties = []struct{ Code, Name string }{
	{"cardiologia", "Cardiología"},
	{"clinica-medica", "Clínica médica"},
	{"dermatologia", "Dermatología"},
	{"pediatria", "Pediatría"},
}

var SeedDoctors = []struct {
	ID, FullName  string
	Specialties   []string
	AcceptsMinors bool
}{
	{"doc-001", "Laura Gómez", []string{"clinica-medica", "cardiologia"}, false},
	{"doc-002", "Martín Pereyra", []string{"pediatria", "clinica-medica"}, true},
	{"doc-003", "Sofía Ramírez", []string{"dermatologia", "clinica-medica"}, false},
	{"doc-004", "Julián Acosta", []string{"cardiologia", "pediatria"}, true},
}

func (s *Store) seed(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sp := range SeedSpecialties {
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO specialties (code, name) VALUES (?, ?)", sp.Code, sp.Name); err != nil {
			return err
		}
	}
	for _, d := range SeedDoctors {
		res, err := tx.ExecContext(ctx, "INSERT IGNORE INTO doctors (id, full_name, accepts_minors, active, profile_version) VALUES (?, ?, ?, TRUE, 1)",
			d.ID, d.FullName, d.AcceptsMinors)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // already present: keep its current specialties
		}
		for i, c := range d.Specialties {
			if _, err := tx.ExecContext(ctx, "INSERT INTO doctor_specialties (doctor_id, specialty_code, position) VALUES (?, ?, ?)", d.ID, c, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
