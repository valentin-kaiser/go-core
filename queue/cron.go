package queue

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Cron expression constants.
const (
	presetYearly   = "@yearly"
	presetAnnually = "@annually"
	presetMonthly  = "@monthly"
	presetWeekly   = "@weekly"
	presetDaily    = "@daily"
	presetMidnight = "@midnight"
	presetHourly   = "@hourly"

	cronSpecYearly  = "0 0 0 1 1 *"
	cronSpecMonthly = "0 0 0 1 * *"
	cronSpecWeekly  = "0 0 0 * * 0"
	cronSpecDaily   = "0 0 0 * * *"
	cronSpecHourly  = "0 0 * * * *"

	cronFieldsWithoutSeconds = 5
	cronFieldsWithSeconds    = 6

	// Offsets of the fields relative to the minute field.
	cronHourIndex      = 1
	cronDayIndex       = 2
	cronMonthIndex     = 3
	cronDayOfWeekIndex = 4

	cronSecondMin    = 0
	cronSecondMax    = 59
	cronMinuteMin    = 0
	cronMinuteMax    = 59
	cronHourMin      = 0
	cronHourMax      = 23
	cronDayMin       = 1
	cronDayMax       = 31
	cronMonthMin     = 1
	cronMonthMax     = 12
	cronDayOfWeekMin = 0
	cronDayOfWeekMax = 6

	// cronSearchYears limits the search for the next run to prevent infinite loops.
	cronSearchYears = 4

	cronWildcard       = "*"
	cronAnyMarker      = "?"
	cronStepSeparator  = "/"
	cronRangeSeparator = "-"
	cronListSeparator  = ","
	cronStepParts      = 2
	cronRangeParts     = 2
)

// Month numbers used by Months.
const (
	monthJanuary = iota + 1
	monthFebruary
	monthMarch
	monthApril
	monthMay
	monthJune
	monthJuly
	monthAugust
	monthSeptember
	monthOctober
	monthNovember
	monthDecember
)

// Weekday numbers used by Days.
const (
	daySunday = iota
	dayMonday
	dayTuesday
	dayWednesday
	dayThursday
	dayFriday
	daySaturday
)

var (
	// Presets are predefined cron expressions for common schedules
	Presets = map[string]string{
		presetYearly:   cronSpecYearly,  // Run once a year, midnight, Jan. 1st
		presetAnnually: cronSpecYearly,  // Alias for @yearly
		presetMonthly:  cronSpecMonthly, // Run once a month, midnight, first of month
		presetWeekly:   cronSpecWeekly,  // Run once a week, midnight between Sat/Sun
		presetDaily:    cronSpecDaily,   // Run once a day, midnight
		presetMidnight: cronSpecDaily,   // Alias for @daily
		presetHourly:   cronSpecHourly,  // Run once an hour, beginning of hour
	}
	// Months is a map of month names to their numeric values
	Months = map[string]int{
		"JAN": monthJanuary, "FEB": monthFebruary, "MAR": monthMarch, "APR": monthApril, "MAY": monthMay, "JUN": monthJune,
		"JUL": monthJuly, "AUG": monthAugust, "SEP": monthSeptember, "OCT": monthOctober, "NOV": monthNovember, "DEC": monthDecember,
	}
	// Days is a map of day names to their numeric values
	Days = map[string]int{
		"SUN": daySunday, "MON": dayMonday, "TUE": dayTuesday, "WED": dayWednesday, "THU": dayThursday, "FRI": dayFriday, "SAT": daySaturday,
	}
)

// CronField represents a field in a cron expression
type CronField struct {
	Min, Max int
	Values   []int
}

// CronExpression represents a parsed cron expression
type CronExpression struct {
	Second    *CronField // Optional seconds field (0-59)
	Minute    CronField  // Minute field (0-59)
	Hour      CronField  // Hour field (0-23)
	Day       CronField  // Day field (1-31)
	Month     CronField  // Month field (1-12)
	DayOfWeek CronField  // Day of week field (0-6, 0 = Sunday)
}

// cronPosition is the point in the calendar the search for the next run has reached.
type cronPosition struct {
	year, month, day, hour, minute int
}

func (p cronPosition) isInMonthOf(t time.Time) bool {
	return p.year == t.Year() && p.month == int(t.Month())
}

func (p cronPosition) isOnDayOf(t time.Time) bool {
	return p.isInMonthOf(t) && p.day == t.Day()
}

func (p cronPosition) isInHourOf(t time.Time) bool {
	return p.isOnDayOf(t) && p.hour == t.Hour()
}

func (p cronPosition) isInMinuteOf(t time.Time) bool {
	return p.isInHourOf(t) && p.minute == t.Minute()
}

func (p cronPosition) at(location *time.Location, second int) time.Time {
	return time.Date(p.year, time.Month(p.month), p.day, p.hour, p.minute, second, 0, location)
}

// ValidateCronSpec validates a cron specification.
//
// Purpose:
// This function checks whether the provided cron specification is valid.
// It supports both standard cron formats (5 or 6 fields) and predefined expressions.
//
// Supported formats:
// - Standard cron expressions with 5 fields (minute, hour, day, month, day of week).
// - Extended cron expressions with 6 fields (second, minute, hour, day, month, day of week).
// - Predefined expressions such as "@yearly", "@daily", "@hourly", etc., which are mapped to standard cron strings.
//
// Validation rules:
// - Ensures the cron syntax is correct.
// - Maps predefined expressions to their equivalent cron strings.
// - Delegates parsing and validation to the parseCronSpec function.
func (s *TaskScheduler) ValidateCronSpec(spec string) error {
	_, err := s.ParseCronSpec(spec)
	if err != nil {
		return apperror.NewErrorf("validating cron specification %q failed", spec).AddError(err)
	}

	return nil
}

// ParseCronSpec parses a cron specification
func (s *TaskScheduler) ParseCronSpec(cronSpec string) (*CronExpression, error) {
	if predefined, exists := Presets[cronSpec]; exists {
		return s.ParseCronSpec(predefined)
	}

	fields := strings.Fields(cronSpec)

	if len(fields) != cronFieldsWithoutSeconds && len(fields) != cronFieldsWithSeconds {
		return nil, apperror.NewError("cron expression must have exactly 5 fields (minute hour day month day-of-week) or 6 fields (second minute hour day month day-of-week)")
	}

	expr := &CronExpression{}
	var err error
	fieldOffset := 0

	if len(fields) == cronFieldsWithSeconds {
		secondField, err := s.parseCronField(fields[0], cronSecondMin, cronSecondMax)
		if err != nil {
			return nil, apperror.NewError("parsing second field failed").AddError(err)
		}
		expr.Second = &secondField
		fieldOffset = 1
	}

	expr.Minute, err = s.parseCronField(fields[fieldOffset], cronMinuteMin, cronMinuteMax)
	if err != nil {
		return nil, apperror.NewError("parsing minute field failed").AddError(err)
	}

	expr.Hour, err = s.parseCronField(fields[fieldOffset+cronHourIndex], cronHourMin, cronHourMax)
	if err != nil {
		return nil, apperror.NewError("parsing hour field failed").AddError(err)
	}

	expr.Day, err = s.parseCronFieldWithNames(fields[fieldOffset+cronDayIndex], cronDayMin, cronDayMax, nil)
	if err != nil {
		return nil, apperror.NewError("parsing day field failed").AddError(err)
	}

	expr.Month, err = s.parseCronFieldWithNames(fields[fieldOffset+cronMonthIndex], cronMonthMin, cronMonthMax, Months)
	if err != nil {
		return nil, apperror.NewError("parsing month field failed").AddError(err)
	}

	expr.DayOfWeek, err = s.parseCronFieldWithNames(fields[fieldOffset+cronDayOfWeekIndex], cronDayOfWeekMin, cronDayOfWeekMax, Days)
	if err != nil {
		return nil, apperror.NewError("parsing day-of-week field failed").AddError(err)
	}

	s.sort(&expr.Minute)
	s.sort(&expr.Hour)
	s.sort(&expr.Day)
	s.sort(&expr.Month)
	s.sort(&expr.DayOfWeek)
	if expr.Second != nil {
		s.sort(expr.Second)
	}

	return expr, nil
}

// sort sorts the values in a cron field for efficient searching
func (s *TaskScheduler) sort(field *CronField) {
	sort.Ints(field.Values)
}

// parseCronField parses a single field in a cron expression
func (s *TaskScheduler) parseCronField(field string, low, high int) (CronField, error) {
	cronField := CronField{Min: low, Max: high}

	if field == cronWildcard {
		for i := low; i <= high; i++ {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}

	if strings.Contains(field, cronStepSeparator) {
		return s.parseStepField(field, low, high, cronField)
	}

	if strings.Contains(field, cronRangeSeparator) {
		return s.parseRangeField(field, low, high, cronField)
	}

	if strings.Contains(field, cronListSeparator) {
		return s.parseListField(field, low, high, cronField)
	}

	return s.parseSingleField(field, low, high, cronField)
}

// parseCronFieldWithNames parses a cron field that supports named values
func (s *TaskScheduler) parseCronFieldWithNames(field string, low, high int, nameMap map[string]int) (CronField, error) {
	cronField := CronField{Min: low, Max: high}

	// Handle ? character (equivalent to *)
	if field == cronAnyMarker {
		field = cronWildcard
	}

	if field == cronWildcard {
		for i := low; i <= high; i++ {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}

	normalizedField := s.normalizeFieldNames(field, nameMap)
	if strings.Contains(normalizedField, cronStepSeparator) {
		return s.parseStepField(normalizedField, low, high, cronField)
	}

	if strings.Contains(normalizedField, cronRangeSeparator) {
		return s.parseRangeField(normalizedField, low, high, cronField)
	}

	if strings.Contains(normalizedField, cronListSeparator) {
		return s.parseListField(normalizedField, low, high, cronField)
	}

	return s.parseSingleField(normalizedField, low, high, cronField)
}

// normalizeFieldNames converts named values to numeric values
func (s *TaskScheduler) normalizeFieldNames(field string, nameMap map[string]int) string {
	if nameMap == nil {
		return field
	}

	result := field
	for name, value := range nameMap {
		result = strings.ReplaceAll(result, name, strconv.Itoa(value))
	}
	return result
}

// parseStepField parses a step field (e.g., "*/5", "1-10/2")
func (s *TaskScheduler) parseStepField(field string, low, high int, cronField CronField) (CronField, error) {
	parts := strings.Split(field, cronStepSeparator)
	if len(parts) != cronStepParts {
		return cronField, apperror.NewError("invalid step format")
	}

	step, err := strconv.Atoi(parts[1])
	if err != nil || step <= 0 {
		return cronField, apperror.NewError("invalid step value")
	}

	var start, end int
	if parts[0] == cronWildcard {
		start, end = low, high
		for i := start; i <= end; i += step {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}
	if parts[0] != cronWildcard && strings.Contains(parts[0], cronRangeSeparator) {
		rangeParts := strings.Split(parts[0], cronRangeSeparator)
		if len(rangeParts) != cronRangeParts {
			return cronField, apperror.NewError("invalid range format")
		}
		start, err = strconv.Atoi(rangeParts[0])
		if err != nil || start < low || start > high {
			return cronField, apperror.NewError("invalid range start")
		}
		end, err = strconv.Atoi(rangeParts[1])
		if err != nil || end < low || end > high || end < start {
			return cronField, apperror.NewError("invalid range end")
		}
		for i := start; i <= end; i += step {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}
	if parts[0] != cronWildcard && !strings.Contains(parts[0], cronRangeSeparator) {
		start, err = strconv.Atoi(parts[0])
		if err != nil || start < low || start > high {
			return cronField, apperror.NewError("invalid step start")
		}
		end = high
		for i := start; i <= end; i += step {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}
	return cronField, nil
}

// parseRangeField parses a range field (e.g., "1-5")
func (s *TaskScheduler) parseRangeField(field string, low, high int, cronField CronField) (CronField, error) {
	parts := strings.Split(field, cronRangeSeparator)
	if len(parts) != cronRangeParts {
		return cronField, apperror.NewError("invalid range format")
	}

	start, err := strconv.Atoi(parts[0])
	if err != nil || start < low || start > high {
		return cronField, apperror.NewError("invalid range start")
	}

	end, err := strconv.Atoi(parts[1])
	if err != nil || end < low || end > high || end < start {
		return cronField, apperror.NewError("invalid range end")
	}

	for i := start; i <= end; i++ {
		cronField.Values = append(cronField.Values, i)
	}
	return cronField, nil
}

// parseListField parses a list field (e.g., "1,3,5")
func (s *TaskScheduler) parseListField(field string, low, high int, cronField CronField) (CronField, error) {
	parts := strings.SplitSeq(field, cronListSeparator)
	for part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < low || value > high {
			return cronField, apperror.NewError("invalid value in list")
		}
		cronField.Values = append(cronField.Values, value)
	}
	return cronField, nil
}

// parseSingleField parses a single field (e.g., "5")
func (s *TaskScheduler) parseSingleField(field string, low, high int, cronField CronField) (CronField, error) {
	value, err := strconv.Atoi(field)
	if err != nil || value < low || value > high {
		return cronField, apperror.NewError("invalid single value")
	}
	cronField.Values = append(cronField.Values, value)
	return cronField, nil
}

// calculateNextCronRun calculates the next run time
//
// Algorithm Overview:
// 1. Works from largest time unit (year/month) down to smallest (second)
// 2. For each field, finds the next valid value mathematically
// 3. Uses pre-sorted field values for efficient searching
// 4. Handles day-of-week constraints properly
// 5. Limits search scope to prevent infinite loops (max 4 years)
//
// Time Complexity: O(Y*M*D*H*M*S) where each factor represents the number of
// valid values in that field, significantly better than brute-force O(total_time_units)
func (s *TaskScheduler) calculateNextCronRun(cronSpec string, after time.Time) (time.Time, error) {
	expr, err := s.ParseCronSpec(cronSpec)
	if err != nil {
		return time.Time{}, apperror.NewErrorf("parsing cron specification %q failed", cronSpec).AddError(err)
	}

	var from time.Time
	if expr.Second != nil {
		from = after.Truncate(time.Second).Add(time.Second)
	} else {
		from = after.Truncate(time.Minute).Add(time.Minute)
	}

	maxYear := from.Year() + cronSearchYears
	for year := from.Year(); year <= maxYear; year++ {
		next, found := s.nextRunInYear(expr, from, year)
		if found {
			return next, nil
		}
	}

	return time.Time{}, apperror.NewError("finding next run time within reasonable limits failed")
}

func (s *TaskScheduler) nextRunInYear(expr *CronExpression, from time.Time, year int) (time.Time, bool) {
	startMonth := s.findFirstValidValue(expr.Month)
	if year == from.Year() {
		startMonth = int(from.Month())
	}

	month, ok := s.findNextValidValue(expr.Month, startMonth)
	for ; ok; month, ok = s.findNextValidValue(expr.Month, month+1) {
		next, found := s.nextRunInMonth(expr, from, cronPosition{year: year, month: month})
		if found {
			return next, true
		}
	}
	return time.Time{}, false
}

func (s *TaskScheduler) nextRunInMonth(expr *CronExpression, from time.Time, pos cronPosition) (time.Time, bool) {
	startDay := s.findFirstValidValue(expr.Day)
	if pos.isInMonthOf(from) {
		startDay = from.Day()
	}
	daysInMonth := time.Date(pos.year, time.Month(pos.month)+1, 0, 0, 0, 0, 0, time.UTC).Day()

	day, ok := s.findNextValidValue(expr.Day, startDay)
	for ; ok && day <= daysInMonth; day, ok = s.findNextValidValue(expr.Day, day+1) {
		pos.day = day
		weekday := int(time.Date(pos.year, time.Month(pos.month), day, 0, 0, 0, 0, from.Location()).Weekday())
		if !s.fieldMatches(expr.DayOfWeek, weekday) {
			continue
		}
		next, found := s.nextRunInDay(expr, from, pos)
		if found {
			return next, true
		}
	}
	return time.Time{}, false
}

func (s *TaskScheduler) nextRunInDay(expr *CronExpression, from time.Time, pos cronPosition) (time.Time, bool) {
	startHour := s.findFirstValidValue(expr.Hour)
	if pos.isOnDayOf(from) {
		startHour = from.Hour()
	}

	hour, ok := s.findNextValidValue(expr.Hour, startHour)
	for ; ok; hour, ok = s.findNextValidValue(expr.Hour, hour+1) {
		pos.hour = hour
		next, found := s.nextRunInHour(expr, from, pos)
		if found {
			return next, true
		}
	}
	return time.Time{}, false
}

func (s *TaskScheduler) nextRunInHour(expr *CronExpression, from time.Time, pos cronPosition) (time.Time, bool) {
	startMinute := s.findFirstValidValue(expr.Minute)
	if pos.isInHourOf(from) {
		startMinute = from.Minute()
	}

	minute, ok := s.findNextValidValue(expr.Minute, startMinute)
	for ; ok; minute, ok = s.findNextValidValue(expr.Minute, minute+1) {
		pos.minute = minute
		next, found := s.nextRunInMinute(expr, from, pos)
		if found {
			return next, true
		}
	}
	return time.Time{}, false
}

func (s *TaskScheduler) nextRunInMinute(expr *CronExpression, from time.Time, pos cronPosition) (time.Time, bool) {
	if expr.Second == nil {
		return pos.at(from.Location(), 0), true
	}

	startSecond := s.findFirstValidValue(*expr.Second)
	if pos.isInMinuteOf(from) {
		startSecond = from.Second()
	}

	second, found := s.findNextValidValue(*expr.Second, startSecond)
	if !found {
		return time.Time{}, false
	}
	return pos.at(from.Location(), second), true
}

// fieldMatches checks if a value matches a cron field
func (s *TaskScheduler) fieldMatches(field CronField, value int) bool {
	return slices.Contains(field.Values, value)
}

// findNextValidValue finds the next valid value in a cron field that is >= the given value
// Assumes Values slice is sorted
func (s *TaskScheduler) findNextValidValue(field CronField, value int) (int, bool) {
	for _, v := range field.Values {
		if v >= value {
			return v, true
		}
	}
	return 0, false
}

// findFirstValidValue finds the first (smallest) valid value in a cron field
// Assumes Values slice is sorted
// Used to optimize starting points when moving to new time periods
func (s *TaskScheduler) findFirstValidValue(field CronField) int {
	if len(field.Values) == 0 {
		return field.Min
	}
	return field.Values[0]
}
