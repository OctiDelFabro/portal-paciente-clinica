package repository

import "context"

func (r *Repo) ListPatients(ctx context.Context, pending bool, page int) ([]Patient, error) {
	rows, err := r.DB.QueryContext(ctx, "SELECT "+columns+" FROM patients WHERE role = 'patient' AND (? = FALSE OR enabled = FALSE) ORDER BY created_at, id LIMIT 51 OFFSET ?", pending, (page-1)*50)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Patient{}
	for rows.Next() {
		var p Patient
		// Scan by ID via the same row scanner shape, without holding a second
		// connection; scanPatient accepts both sql.Row and sql.Rows.
		p, err = scanPatient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *Repo) SetVerification(ctx context.Context, id string, enabled bool) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE patients SET enabled = ? WHERE id = ? AND role = 'patient'", enabled, id)
	return err
}
func (r *Repo) SetResponsible(ctx context.Context, id, responsible string) error {
	_, err := r.DB.ExecContext(ctx, "UPDATE patients SET responsible_id = NULLIF(?, '') WHERE id = ? AND role = 'patient'", responsible, id)
	return err
}
