package vtimezone

import (
	"fmt"
	"github.com/emersion/go-ical"
	"github.com/martin-sucha/timezones"
	"github.com/teambition/rrule-go"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"time"
)

func ToLocationTemplate(name string, vTimezone *ical.Component) (*timezones.Template, error) {
	lt := timezones.Template{
		Name: name,
	}

	// Find unbounded yearly repeating rules to build extend string from.
	var stdRule, dstRule extendRuleSlot
	stdRule.slotName = "standard"
	dstRule.slotName = "daylight"

	for i := range vTimezone.Children {
		isStd := vTimezone.Children[i].Name == ical.CompTimezoneStandard
		isDst := vTimezone.Children[i].Name == ical.CompTimezoneDaylight
		if !isStd && !isDst {
			return nil, fmt.Errorf("unsupported component type %q", vTimezone.Children[i].Name)
		}
		rule, err := parseRule(vTimezone.Children[i], isDst)
		if err != nil {
			return nil, err
		}
		if rule.isRepeatingForever() {
			// This rule is repeating forever, use it for extend string.
			if isStd {
				err = stdRule.set(rule)
			} else {
				err = dstRule.set(rule)
			}
			if err != nil {
				return nil, err
			}
			// We processed this rule.
			continue
		}
		err = addZones(&lt, rule)
		if err != nil {
			return nil, err
		}
	}

	err := addExtendRules(&lt, stdRule, dstRule)
	if err != nil {
		return nil, err
	}

	sort.Slice(lt.Changes, func(i, j int) bool {
		return lt.Changes[i].Start.Before(lt.Changes[j].Start)
	})

	return &lt, nil
}

type extendRuleSlot struct {
	slotName string
	r        zoneRule
	present  bool
}

func (e *extendRuleSlot) set(r zoneRule) error {
	if e.present {
		return fmt.Errorf("more than one unbounded %s rule is not supported", e.slotName)
	}
	e.r = r
	e.present = true
	return nil
}

func parseRule(vRule *ical.Component, isDST bool) (zoneRule, error) {
	var rule zoneRule
	rule.isDST = isDST

	dtStart := vRule.Props.Get("DTSTART")
	if dtStart == nil {
		return zoneRule{}, fmt.Errorf("missing DTSTART")
	}
	rule.dtStart = dtStart.Value
	if len(rule.dtStart) != len(rrule.LocalDateTimeFormat) {
		return zoneRule{}, fmt.Errorf("dtstart must be specified in local date time format")
	}

	tzOffsetFrom := vRule.Props.Get("TZOFFSETFROM")
	if tzOffsetFrom == nil {
		return zoneRule{}, fmt.Errorf("missing TZOFFSETFROM")
	}
	offsetFrom, err := parseOffset(tzOffsetFrom.Value)
	if err != nil {
		return zoneRule{}, fmt.Errorf("invalid TZOFFSETFROM: %v", err)
	}
	rule.tzOffsetFrom = offsetFrom

	tzOffsetTo := vRule.Props.Get("TZOFFSETTO")
	if tzOffsetTo == nil {
		return zoneRule{}, fmt.Errorf("missing TZOFFSETTO")
	}
	offsetTo, err := parseOffset(tzOffsetTo.Value)
	if err != nil {
		return zoneRule{}, fmt.Errorf("invalid TZOFFSETTTO: %v", err)
	}
	rule.tzOffsetTo = offsetTo

	// TZNAME is optional
	tzName := vRule.Props.Get("TZNAME")
	if tzName != nil {
		rule.tzName = tzName.Value
	}

	rule.rrule = vRule.Props.Get("RRULE")
	if rule.rrule == nil {
		return zoneRule{}, fmt.Errorf("missing RRULE")
	}

	m := make(map[string]string)
	for _, part := range strings.Split(rule.rrule.Value, ";") {
		keyValue := strings.Split(part, "=")
		if len(keyValue) != 2 {
			return zoneRule{}, fmt.Errorf("rule part does not have single =")
		}
		key, value := keyValue[0], keyValue[1]
		if len(value) == 0 {
			return zoneRule{}, fmt.Errorf("rule option %s has not value", key)
		}
		m[key] = value
	}
	rule.rruleParts = m

	return rule, nil
}

// https://datatracker.ietf.org/doc/html/rfc5545#section-3.3.14
func parseOffset(offset string) (time.Duration, error) {
	s := offset
	negative := false
	switch {
	case strings.HasPrefix(s, "-"):
		negative = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	if len(s) != 4 && len(s) != 6 {
		return 0, fmt.Errorf("invalid time offset: %q", offset)
	}
	hour, err := strconv.ParseUint(s[0:2], 10, 64)
	if err != nil || hour > 23 {
		return 0, fmt.Errorf("invalid hours in time offset %q: %v", offset, err)
	}
	min, err := strconv.ParseUint(s[2:4], 10, 64)
	if err != nil || min > 59 {
		return 0, fmt.Errorf("invalid minutes in time offset %q: %v", offset, err)
	}
	var sec uint64
	if len(s) == 6 {
		sec, err = strconv.ParseUint(s[4:6], 10, 64)
		if err != nil || sec > 59 {
			return 0, fmt.Errorf("invalid seconds in time offset %q: %v", offset, err)
		}
	}
	ret := time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute + time.Duration(sec)*time.Second
	if negative {
		ret = -ret
	}
	return ret, nil
}

type zoneRule struct {
	isDST        bool
	dtStart      string
	tzOffsetFrom time.Duration
	tzOffsetTo   time.Duration
	tzName       string
	rrule        *ical.Prop
	rruleParts   map[string]string
}

// isRepeatingForever checks whether the rule repeats forever.
func (z *zoneRule) isRepeatingForever() bool {
	return z.rruleParts["UNTIL"] == "" && z.rruleParts["COUNT"] == ""
}

func (z *zoneRule) roption() (*rrule.ROption, error) {
	if strings.Contains(z.rruleParts["DTSTART"], "TZID=") {
		return nil, fmt.Errorf("timezone start date cannot reference another timezone")
	}
	until := z.rruleParts["UNTIL"]
	if len(until) > 0 && len(until) != len(rrule.DateTimeFormat) {
		return nil, fmt.Errorf("until in timezone must be specified as UTC time")
	}

	fromLoc := time.FixedZone("tmp", int(z.tzOffsetFrom/time.Second))

	return rrule.StrToROptionInLocation(z.rrule.Value, fromLoc)
}

func addZones(lt *timezones.Template, rule zoneRule) error {
	roption, err := rule.roption()
	if err != nil {
		return err
	}
	r, err := rrule.NewRRule(*roption)
	if err != nil {
		return err
	}
	lt.Zones = append(lt.Zones, timezones.Zone{
		Name:   rule.tzName,
		Offset: rule.tzOffsetTo,
		IsDST:  rule.isDST,
	})
	zoneIndex := len(lt.Zones) - 1
	next := r.Iterator()
	for t, ok := next(); ok; t, ok = next() {
		lt.Changes = append(lt.Changes, timezones.Change{
			Start:     t,
			ZoneIndex: zoneIndex,
		})
	}
	return nil
}

func addExtendRules(lt *timezones.Template, stdRule, dstRule extendRuleSlot) error {
	// TODO check that extend rules start after other rules
	if !stdRule.present && dstRule.present {
		return fmt.Errorf("daylight saving rule without standard rule is not supported")
	}

	var sb strings.Builder
	err := addExtendName(&sb, stdRule.r.tzName)
	if err != nil {
		return err
	}
	addExtendOffset(&sb, -stdRule.r.tzOffsetTo)
	if !dstRule.present {
		lt.Extend = sb.String()
		return nil
	}
	err = addExtendName(&sb, dstRule.r.tzName)
	if err != nil {
		return err
	}
	addExtendOffset(&sb, -dstRule.r.tzOffsetTo)
	// We intentionally write dstRule first because the first rule is from standard time to daylight.
	err = addExtendRule(&sb, dstRule.r)
	if err != nil {
		return err
	}
	err = addExtendRule(&sb, stdRule.r)
	if err != nil {
		return err
	}
	lt.Extend = sb.String()
	return nil
}

func addExtendName(sb *strings.Builder, name string) error {
	if strings.Contains(name, ">") {
		return fmt.Errorf("zone name contains >: %q", name)
	}
	sb.WriteString("<")
	sb.WriteString(name)
	sb.WriteString(">")
	return nil
}

func addExtendOffset(sb *strings.Builder, offset time.Duration) {
	if offset < 0 {
		sb.WriteString("-")
		offset = -offset
	}
	hour, offset := offset/time.Hour, offset%time.Hour
	min, offset := offset/time.Minute, offset%time.Minute
	sec := offset / time.Second
	_, _ = fmt.Fprintf(sb, "%02d:%02d:%02d", hour, min, sec) // write to strings.Builder never fails
}

const (
	ruleFieldFreq = 1 << iota
	ruleFieldInterval
	ruleFieldBySecond
	ruleFieldByMinute
	ruleFieldByHour
	ruleFieldByDay
	ruleFieldByMonthDay
	ruleFieldByYearDay
	ruleFieldByWeekNo
	ruleFieldByMonth
	ruleFieldBySetPos
	ruleFieldWkst
)

var fieldFlagsByName = map[string]uint64{
	"FREQ":       ruleFieldFreq,
	"INTERVAL":   ruleFieldInterval,
	"BYSECOND":   ruleFieldBySecond,
	"BYMINUTE":   ruleFieldByMinute,
	"BYHOUR":     ruleFieldByHour,
	"BYDAY":      ruleFieldByDay,
	"BYMONTHDAY": ruleFieldByMonthDay,
	"BYYEARDAY":  ruleFieldByYearDay,
	"BYWEEKNO":   ruleFieldByWeekNo,
	"BYMONTH":    ruleFieldByMonth,
	"BYSETPOS":   ruleFieldBySetPos,
	"WKST":       ruleFieldWkst,
}

var weekDayMap = map[string]int{
	"SU": 0,
	"MO": 1,
	"TU": 2,
	"WE": 3,
	"TH": 4,
	"FR": 5,
	"SA": 6,
}

func addExtendRule(sb *strings.Builder, rule zoneRule) error {
	// UNTIL and COUNT is not present, we verified that in isRepeatingForever.
	if freq := rule.rruleParts["FREQ"]; freq != "YEARLY" {
		return fmt.Errorf("unsupported extend rule freq %q", freq)
	}
	if interval := rule.rruleParts["INTERVAL"]; interval != "" && interval != "1" {
		return fmt.Errorf("unsupported extend rule interval %q", interval)
	}

	var flags uint64
	for fieldName := range rule.rruleParts {
		fieldFlag, ok := fieldFlagsByName[fieldName]
		if !ok {
			return fmt.Errorf("unsupported RRULE field %q", fieldName)
		}
		flags |= fieldFlag
	}

	flags &^= ruleFieldFreq | ruleFieldInterval

	switch flags {
	case ruleFieldByMonth | ruleFieldByDay:
		byDay := rule.rruleParts["BYDAY"]
		if strings.Contains(byDay, ",") {
			return fmt.Errorf("only a single element is supported in BYDAY")
		}
		// Write Mm.n.d rule
		if len(byDay) < 2 {
			return fmt.Errorf("BYDAY rule must include day and offset: %q", byDay)
		}
		offset, err := strconv.ParseInt(byDay[:len(byDay)-2], 10, 64)
		if err != nil {
			return fmt.Errorf("parse BYDAY rule %q: %v", byDay, err)
		}
		var week int
		switch {
		case offset == -1:
			week = 5
		case offset > 0 && offset < 5:
			week = int(offset)
		default:
			return fmt.Errorf("parse BYDAY rule %q: unsupported offset %d", byDay, offset)
		}
		weekDay, ok := weekDayMap[byDay[len(byDay)-2:]]
		if !ok {
			return fmt.Errorf("parse BYDAY rule %q: unknown week day %q", byDay, byDay[len(byDay)-2:])
		}

		byMonth := rule.rruleParts["BYMONTH"]
		if strings.Contains(byDay, ",") {
			return fmt.Errorf("only a single element is supported in BYMONTH")
		}
		month, err := strconv.ParseUint(byMonth, 10, 64)
		if err != nil {
			return fmt.Errorf("parse BYMONTH rule %q: %v", byMonth, err)
		}
		if month < 1 || month > 12 {
			return fmt.Errorf("parse BYMONTH rule %q: month out of range", byMonth)
		}

		_, _ = fmt.Fprintf(sb, "M%d.%d.%d", month, week, weekDay)
	default:
		strFlags := make([]string, 0, bits.OnesCount64(flags))
		for fieldName, flag := range fieldFlagsByName {
			if (flags & flag) > 0 {
				strFlags = append(strFlags, fieldName)
			}
		}
		sort.Strings(strFlags)
		return fmt.Errorf("unsupported combination of rule properties: %s", strings.Join(strFlags, ", "))
	}

	fromLoc := time.FixedZone("tmp", int(rule.tzOffsetFrom/time.Second))
	dtStart, err := time.ParseInLocation(rrule.LocalDateTimeFormat, rule.dtStart, fromLoc)
	if err != nil {
		return fmt.Errorf("parse DTSTART: %v", err)
	}

	hour, minute, second := dtStart.Clock()
	_, _ = fmt.Fprintf(sb, "/%02d:%02d:%02d", hour, minute, second)
	return nil
}
