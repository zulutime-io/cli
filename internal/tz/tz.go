package tz

import (
	"strings"
	"time"
)

const Default = "Europe/Amsterdam"

// Load returns the org timezone, falling back to Europe/Amsterdam then Local.
func Load(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		name = Default
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// Parse accepts RFC3339 / RFC3339Nano / date-only (YYYY-MM-DD).
// Date-only is treated as midnight in loc (not UTC), so it does not shift when formatted.
func Parse(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errEmpty
	}
	if loc == nil {
		loc = time.Local
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, nil
	}
	return time.Time{}, errBad
}

// Format formats t in loc as "2006-01-02 15:04".
func Format(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

// FormatDate formats a calendar date in loc.
func FormatDate(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02")
}

type parseError string

func (e parseError) Error() string { return string(e) }

const (
	errEmpty parseError = "empty time"
	errBad   parseError = "invalid time"
)
