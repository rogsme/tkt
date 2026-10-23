// Package cron reads the five-field schedules that chores run on, as cron
// writes them: minute, hour, day of the month, month and day of the week,
// in local time. It works out the slots around a moment, so tkt watch can
// tell whether a chore is due and when it runs next.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed schedule: the minutes, hours, days, months and
// weekdays it fires on.
type Schedule struct {
	src                           string
	minute, hour, dom, month, dow uint64
	// domStar and dowStar say the field began with *, as in * or */2. When
	// neither did, a day matches on either field, as cron has it.
	domStar, dowStar bool
}

// shorthands are the schedules cron names with a word.
var shorthands = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var (
	months   = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

// field describes one of the five fields: its range and its names.
type field struct {
	name     string
	min, max int
	names    []string
}

var fields = []field{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day of the month", 1, 31, nil},
	{"month", 1, 12, months},
	// 7 is Sunday too.
	{"day of the week", 0, 7, weekdays},
}

// Parse reads a schedule such as "0 9 * * 1,4": numbers, ranges (1-5),
// steps (*/15, 1-30/2), lists of those, the names jan to dec and sun to sat,
// and the shorthands @hourly, @daily, @weekly, @monthly and @yearly. A
// schedule that never fires, such as one on February 30, is an error.
func Parse(s string) (Schedule, error) {
	src := strings.TrimSpace(s)
	spec := src
	if short, ok := shorthands[strings.ToLower(spec)]; ok {
		spec = short
	}
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("%q needs five fields (minute, hour, day of the month, month, day of the week), such as \"0 9 * * 1,4\"", src)
	}
	sched := Schedule{src: src}
	sets := []*uint64{&sched.minute, &sched.hour, &sched.dom, &sched.month, &sched.dow}
	for i, p := range parts {
		bits, err := parseField(p, fields[i])
		if err != nil {
			return Schedule{}, fmt.Errorf("%q: %v", src, err)
		}
		*sets[i] = bits
	}
	// Sunday is 0 and 7.
	if sched.dow&(1<<7) != 0 {
		sched.dow = sched.dow&^(1<<7) | 1
	}
	sched.domStar, sched.dowStar = strings.HasPrefix(parts[2], "*"), strings.HasPrefix(parts[4], "*")
	probe := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if sched.Next(probe).IsZero() {
		return Schedule{}, fmt.Errorf("%q never fires", src)
	}
	return sched, nil
}

func parseField(s string, f field) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		rng, stepText, stepped := strings.Cut(part, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s: the step in %q must be a number above 0", f.name, part)
			}
			step = n
		}
		lo, hi := f.min, f.max
		switch {
		case rng == "*":
		case rng == "":
			return 0, fmt.Errorf("%s: %q is empty", f.name, part)
		default:
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, err
			}
			hi = lo
			switch {
			case isRange:
				if hi, err = value(b, f); err != nil {
					return 0, err
				}
			case stepped:
				// N/S runs from N to the field's end.
				hi = f.max
			}
			if hi < lo {
				return 0, fmt.Errorf("%s: %q runs backwards", f.name, part)
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

func value(s string, f field) (int, error) {
	for i, n := range f.names {
		if strings.EqualFold(s, n) {
			return i + f.min, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("%s: %q is not between %d and %d", f.name, s, f.min, f.max)
	}
	return v, nil
}

// String returns the schedule as written.
func (s Schedule) String() string { return s.src }

// horizon bounds the search for a slot: a schedule that parsed fires at
// least once in five years, February 29 included.
const horizon = 5 * 366 * 24 * time.Hour

func has(bits uint64, v int) bool { return bits&(1<<v) != 0 }

// day reports whether the schedule fires on t's day: on its day of the
// month and its weekday, or, when both fields name days, on either.
func (s Schedule) day(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// Next returns the first slot after t, in t's location, or the zero time
// when the schedule has none within five years.
func (s Schedule) Next(t time.Time) time.Time {
	loc := t.Location()
	t = t.Truncate(time.Minute).Add(time.Minute)
	end := t.Add(horizon)
	for t.Before(end) {
		y, m, d := t.Date()
		next := t.Add(time.Minute)
		switch {
		case !has(s.month, int(m)):
			next = time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
		case !s.day(t):
			next = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		case !has(s.hour, t.Hour()):
			next = time.Date(y, m, d, t.Hour()+1, 0, 0, 0, loc)
		case !has(s.minute, t.Minute()):
		default:
			return t
		}
		// Around a change of clocks, a wall time can name an earlier
		// moment; a minute's step always moves on.
		if !next.After(t) {
			next = t.Add(time.Minute)
		}
		t = next
	}
	return time.Time{}
}

// Prev returns the last slot at or before t, in t's location, or the zero
// time when the schedule has none within five years.
func (s Schedule) Prev(t time.Time) time.Time {
	loc := t.Location()
	t = t.Truncate(time.Minute)
	end := t.Add(-horizon)
	for t.After(end) {
		y, m, d := t.Date()
		prev := t.Add(-time.Minute)
		switch {
		case !has(s.month, int(m)):
			prev = time.Date(y, m, 1, 0, 0, 0, 0, loc).Add(-time.Minute)
		case !s.day(t):
			prev = time.Date(y, m, d, 0, 0, 0, 0, loc).Add(-time.Minute)
		case !has(s.hour, t.Hour()):
			prev = time.Date(y, m, d, t.Hour(), 0, 0, 0, loc).Add(-time.Minute)
		case !has(s.minute, t.Minute()):
		default:
			return t
		}
		if !prev.Before(t) {
			prev = t.Add(-time.Minute)
		}
		t = prev
	}
	return time.Time{}
}
