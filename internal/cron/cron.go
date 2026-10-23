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

// horizonDays bounds the search for a slot: a schedule that parsed fires at
// least once in five years, February 29 included.
const horizonDays = 5 * 366

func has(bits uint64, v int) bool { return bits&(1<<v) != 0 }

// values lists the numbers a field's bits hold, in order.
func values(bits uint64, max int) []int {
	var out []int
	for v := 0; v <= max; v++ {
		if has(bits, v) {
			out = append(out, v)
		}
	}
	return out
}

// day reports whether the schedule fires on t's day: on its day of the
// month and its weekday, or, when both fields name days, on either.
func (s Schedule) day(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// slots returns the moments the schedule fires on the date of day, in its
// location. A wall time the clocks repeat in autumn fires once, as time.Date
// names it. One they skip in spring fires as they jump: time.Date puts it
// before the gap, so it moves to the first minute after.
func (s Schedule) slots(day time.Time) []time.Time {
	if !has(s.month, int(day.Month())) || !s.day(day) {
		return nil
	}
	y, m, d := day.Date()
	var out []time.Time
	for _, h := range values(s.hour, 23) {
		for _, min := range values(s.minute, 59) {
			c := time.Date(y, m, d, h, min, 0, 0, day.Location())
			if c.Hour() != h || c.Minute() != min {
				want := fmt.Sprintf("%04d%02d%02d%02d%02d", y, m, d, h, min)
				for c.Format("200601021504") < want {
					c = c.Add(time.Minute)
				}
			}
			out = append(out, c)
		}
	}
	return out
}

// Next returns the first slot after t, in t's location, or the zero time
// when the schedule has none within five years.
func (s Schedule) Next(t time.Time) time.Time {
	y, m, d := t.Date()
	for i := 0; i <= horizonDays; i++ {
		var best time.Time
		for _, c := range s.slots(time.Date(y, m, d+i, 0, 0, 0, 0, t.Location())) {
			if c.After(t) && (best.IsZero() || c.Before(best)) {
				best = c
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}

// Prev returns the last slot at or before t, in t's location, or the zero
// time when the schedule has none within five years.
func (s Schedule) Prev(t time.Time) time.Time {
	y, m, d := t.Date()
	for i := 0; i <= horizonDays; i++ {
		var best time.Time
		for _, c := range s.slots(time.Date(y, m, d-i, 0, 0, 0, 0, t.Location())) {
			if !c.After(t) && (best.IsZero() || c.After(best)) {
				best = c
			}
		}
		if !best.IsZero() {
			return best
		}
	}
	return time.Time{}
}
