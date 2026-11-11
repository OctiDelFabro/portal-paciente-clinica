// Package domain holds the pure rules of Medicos: doctor profiles, the search
// query limits (ADR-006) and the derived "next available" projection. It
// imports nothing from the project.
package domain

import (
	"sort"
	"strings"
	"time"
)

// MinLeadTime mirrors the Turnos booking rule (24 h lead time). Turnos owns it;
// Medicos only uses it so nextAvailableAt never points to an unbookable time.
const MinLeadTime = 24 * time.Hour

// Search limits from ADR-006.
const (
	MaxPageSize = 50
	MaxWindow   = 1000
	DefaultSize = 10
	// Sort orders; the empty one ranks by relevance.
	SortRelevant      = ""
	SortName          = "name"
	SortNextAvailable = "nextAvailable"
)

// InvalidError reports input that breaks a domain rule; HTTP maps it to 422.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(msg string) error { return &InvalidError{Msg: msg} }

type Specialty struct {
	Code string
	Name string
}

// Doctor is the authoritative profile stored in MySQL.
type Doctor struct {
	ID             string
	FullName       string
	Specialties    []string
	AcceptsMinors  bool
	Active         bool
	ProfileVersion int64
}

// Profile is the editable part of a Doctor.
type Profile struct {
	FullName      string
	Specialties   []string
	AcceptsMinors bool
	Active        bool
}

func (p Profile) Validate() error {
	name := strings.TrimSpace(p.FullName)
	if name == "" || len(name) > 160 {
		return invalid("fullName is required and must have at most 160 characters")
	}
	if len(p.Specialties) == 0 || len(p.Specialties) > 10 {
		return invalid("specialties must contain between 1 and 10 codes")
	}
	seen := map[string]bool{}
	for _, s := range p.Specialties {
		if s == "" || seen[s] {
			return invalid("specialties must be non-empty and unique")
		}
		seen[s] = true
	}
	return nil
}

// Interval is a booked time range of a doctor, as published by Turnos.
type Interval struct{ Start, End time.Time }

// NextFree returns the first instant at or after from not covered by busy.
// It ignores office hours, which belong to Turnos (see ADR-006 deviations), so
// it is only a sort key: the portal shows Turnos' GET /doctors/{id}/next-available.
func NextFree(from time.Time, busy []Interval) time.Time {
	sorted := append([]Interval(nil), busy...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	t := from
	for _, b := range sorted {
		if b.Start.After(t) {
			break
		}
		if b.End.After(t) {
			t = b.End
		}
	}
	return t
}

// SearchDoc is one document of the doctors index (ADR-006 mapping).
type SearchDoc struct {
	DoctorID        string     `json:"doctorId"`
	FullName        string     `json:"fullName"`
	Specialties     []string   `json:"specialties"`
	AcceptsMinors   bool       `json:"acceptsMinors"`
	Active          bool       `json:"active"`
	NextAvailableAt *time.Time `json:"nextAvailableAt,omitempty"`
	ProfileVersion  int64      `json:"profileVersion"`
}

// Query is a validated search request. Page starts at 1.
type Query struct {
	Q             string
	Specialty     string
	AcceptsMinors *bool
	Sort          string
	Page          int
	Size          int
}

func (q Query) From() int { return (q.Page - 1) * q.Size }

func (q Query) Validate() error {
	if q.Size < 1 || q.Size > MaxPageSize {
		return invalid("size must be between 1 and 50")
	}
	if q.Page < 1 {
		return invalid("page must be 1 or greater")
	}
	if q.From()+q.Size > MaxWindow {
		return invalid("page * size must not exceed 1000; refine the filters")
	}
	switch q.Sort {
	case SortRelevant, SortName, SortNextAvailable:
	default:
		return invalid("sort must be name or nextAvailable")
	}
	return nil
}

// Page is one page of search results.
type Page struct {
	Items []SearchDoc
	Total int
}
