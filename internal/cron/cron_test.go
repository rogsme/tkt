package cron

import (
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextAndPrev(t *testing.T) {
	cases := []struct {
		spec, from, next, prev string
	}{
		// 2026-10-22 is a Thursday.
		{"0 9 * * 1,4", "2026-10-22 08:59", "2026-10-22 09:00", "2026-10-19 09:00"},
		{"0 9 * * 1,4", "2026-10-22 09:00", "2026-10-26 09:00", "2026-10-22 09:00"},
		{"0 12,23 * * *", "2026-10-22 12:30", "2026-10-22 23:00", "2026-10-22 12:00"},
		{"*/15 * * * *", "2026-10-22 10:07", "2026-10-22 10:15", "2026-10-22 10:00"},
		{"30 2 1 * *", "2026-12-15 00:00", "2027-01-01 02:30", "2026-12-01 02:30"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00", "2024-02-29 00:00"},
		{"0 8 * * mon-fri", "2026-10-24 10:00", "2026-10-26 08:00", "2026-10-23 08:00"},
		{"0 0 1 jan *", "2026-06-01 00:00", "2027-01-01 00:00", "2026-01-01 00:00"},
		{"@daily", "2026-10-22 10:00", "2026-10-23 00:00", "2026-10-22 00:00"},
		{"@hourly", "2026-10-22 10:01", "2026-10-22 11:00", "2026-10-22 10:00"},
		// Sunday is 0 and 7.
		{"0 9 * * 7", "2026-10-22 10:00", "2026-10-25 09:00", "2026-10-18 09:00"},
		{"0 9 * * 0", "2026-10-22 10:00", "2026-10-25 09:00", "2026-10-18 09:00"},
		// With both day fields set, either one fires, as in cron: the 13th,
		// or a Friday.
		{"0 0 13 * 5", "2026-10-10 00:00", "2026-10-13 00:00", "2026-10-09 00:00"},
		// With one of them a star, both must hold.
		{"0 0 */2 * 5", "2026-10-10 00:00", "2026-10-23 00:00", "2026-10-09 00:00"},
		{"5-10/5 1 * * *", "2026-10-22 01:06", "2026-10-22 01:10", "2026-10-22 01:05"},
		{"50/5 * * * *", "2026-10-22 01:56", "2026-10-22 02:50", "2026-10-22 01:55"},
	}
	for _, c := range cases {
		s, err := Parse(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := s.Next(at(c.from)).Format("2006-01-02 15:04"); got != c.next {
			t.Errorf("%s: Next(%s) = %s, want %s", c.spec, c.from, got, c.next)
		}
		if got := s.Prev(at(c.from)).Format("2006-01-02 15:04"); got != c.prev {
			t.Errorf("%s: Prev(%s) = %s, want %s", c.spec, c.from, got, c.prev)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for spec, want := range map[string]string{
		"0 9 * *":         "five fields",
		"60 9 * * *":      "minute: \"60\" is not between 0 and 59",
		"0 24 * * *":      "hour",
		"0 9 0 * *":       "day of the month",
		"0 9 * 13 *":      "month",
		"0 9 * * 8":       "day of the week",
		"0 9 * * fri-mon": "runs backwards",
		"*/0 * * * *":     "step",
		"0 9 ,1 * *":      "is empty",
		"0 0 30 2 *":      "never fires",
		"@sometimes":      "five fields",
	} {
		_, err := Parse(spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want an error about %s", spec, err, want)
		}
	}
}

func TestSlotsKeepLocalTime(t *testing.T) {
	loc, err := time.LoadLocation("America/Montevideo")
	if err != nil {
		t.Skip("no time zone database")
	}
	s, _ := Parse("0 9 * * *")
	from := time.Date(2026, 10, 22, 8, 0, 0, 0, loc)
	if got := s.Next(from); !got.Equal(time.Date(2026, 10, 22, 9, 0, 0, 0, loc)) {
		t.Errorf("Next = %v", got)
	}
}

func TestChangesOfClocks(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database")
	}
	// On 2026-11-01 the clocks go back from 02:00 to 01:00, so 01:30 comes
	// twice: the slot fires once.
	s, _ := Parse("30 1 * * *")
	first := s.Next(time.Date(2026, 10, 31, 12, 0, 0, 0, loc))
	if first.Day() != 1 || first.Hour() != 1 || first.Minute() != 30 {
		t.Fatalf("Next = %v", first)
	}
	if again := s.Next(first); again.Day() != 2 {
		t.Errorf("Next(%v) = %v, the same slot again", first, again)
	}
	if late := s.Prev(time.Date(2026, 11, 1, 3, 0, 0, 0, loc)); !late.Equal(first) {
		t.Errorf("Prev after the change = %v, want %v", late, first)
	}
	// On 2026-03-08 they go forward from 02:00 to 03:00, so 02:30 never
	// comes: the slot fires as the clocks jump, and a weekly one keeps its
	// week.
	s, _ = Parse("30 2 * * *")
	jump := s.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, loc))
	if jump.Day() != 8 || jump.Hour() != 3 {
		t.Errorf("Next on the day of the change = %v", jump)
	}
	if got := s.Prev(time.Date(2026, 3, 8, 12, 0, 0, 0, loc)); !got.Equal(jump) {
		t.Errorf("Prev on the day of the change = %v, want %v", got, jump)
	}
	weekly, _ := Parse("30 2 * * 0")
	if got := weekly.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, loc)); got.Day() != 8 {
		t.Errorf("the weekly slot moved to %v", got)
	}
}
