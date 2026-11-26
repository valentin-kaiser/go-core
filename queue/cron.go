package queue

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

var (
	// Presets are predefined cron expressions for common schedules
	Presets = map[string]string{
		"@yearly":   "0 0 0 1 1 *", // Run once a year, midnight, Jan. 1st
		"@annually": "0 0 0 1 1 *", // Alias for @yearly
		"@monthly":  "0 0 0 1 * *", // Run once a month, midnight, first of month
		"@weekly":   "0 0 0 * * 0", // Run once a week, midnight between Sat/Sun
		"@daily":    "0 0 0 * * *", // Run once a day, midnight
		"@midnight": "0 0 0 * * *", // Alias for @daily
		"@hourly":   "0 0 * * * *", // Run once an hour, beginning of hour
	}
	// Months is a map of month names to their numeric values
	Months = map[string]int{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	}
	// Days is a map of day names to their numeric values
	Days = map[string]int{
		"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
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
	return err
}

// ParseCronSpec parses a cron specification
func (s *TaskScheduler) ParseCronSpec(cronSpec string) (*CronExpression, error) {
	if predefined, exists := Presets[cronSpec]; exists {
		return s.ParseCronSpec(predefined)
	}

	fields := strings.Fields(cronSpec)

	if len(fields) != 5 && len(fields) != 6 {
		return nil, apperror.NewError("cron expression must have exactly 5 fields (minute hour day month day-of-week) or 6 fields (second minute hour day month day-of-week)")
	}

	expr := &CronExpression{}
	var err error
	fieldOffset := 0

	if len(fields) == 6 {
		secondField, err := s.parseCronField(fields[0], 0, 59)
		if err != nil {
			return nil, fmt.Errorf("invalid second field: %v", err)
		}
		expr.Second = &secondField
		fieldOffset = 1
	}

	expr.Minute, err = s.parseCronField(fields[fieldOffset], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %v", err)
	}

	expr.Hour, err = s.parseCronField(fields[fieldOffset+1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %v", err)
	}

	expr.Day, err = s.parseCronFieldWithNames(fields[fieldOffset+2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid day field: %v", err)
	}

	expr.Month, err = s.parseCronFieldWithNames(fields[fieldOffset+3], 1, 12, Months)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %v", err)
	}

	expr.DayOfWeek, err = s.parseCronFieldWithNames(fields[fieldOffset+4], 0, 6, Days)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-week field: %v", err)
	}

	// Sort all field values for efficient searching
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

	if field == "*" {
		for i := low; i <= high; i++ {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}

	if strings.Contains(field, "/") {
		return s.parseStepField(field, low, high, cronField)
	}

	if strings.Contains(field, "-") {
		return s.parseRangeField(field, low, high, cronField)
	}

	if strings.Contains(field, ",") {
		return s.parseListField(field, low, high, cronField)
	}

	return s.parseSingleField(field, low, high, cronField)
}

// parseCronFieldWithNames parses a cron field that supports named values
func (s *TaskScheduler) parseCronFieldWithNames(field string, low, high int, nameMap map[string]int) (CronField, error) {
	cronField := CronField{Min: low, Max: high}

	// Handle ? character (equivalent to *)
	if field == "?" {
		field = "*"
	}

	if field == "*" {
		for i := low; i <= high; i++ {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}

	normalizedField := s.normalizeFieldNames(field, nameMap)
	if strings.Contains(normalizedField, "/") {
		return s.parseStepField(normalizedField, low, high, cronField)
	}

	if strings.Contains(normalizedField, "-") {
		return s.parseRangeField(normalizedField, low, high, cronField)
	}

	if strings.Contains(normalizedField, ",") {
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
	parts := strings.Split(field, "/")
	if len(parts) != 2 {
		return cronField, apperror.NewError("invalid step format")
	}

	step, err := strconv.Atoi(parts[1])
	if err != nil || step <= 0 {
		return cronField, apperror.NewError("invalid step value")
	}

	var start, end int
	if parts[0] == "*" {
		start, end = low, high
		for i := start; i <= end; i += step {
			cronField.Values = append(cronField.Values, i)
		}
		return cronField, nil
	}
	if parts[0] != "*" && strings.Contains(parts[0], "-") {
		rangeParts := strings.Split(parts[0], "-")
		if len(rangeParts) != 2 {
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
	if parts[0] != "*" && !strings.Contains(parts[0], "-") {
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
	parts := strings.Split(field, "-")
	if len(parts) != 2 {
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
	parts := strings.SplitSeq(field, ",")
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

// calculateNextCronRunOptimized efficiently calculates the next run time
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
		return time.Time{}, err
	}

	// Start with the time after the given time
	var t time.Time
	if expr.Second != nil {
		t = after.Truncate(time.Second).Add(time.Second)
	}
	if expr.Second == nil {
		t = after.Truncate(time.Minute).Add(time.Minute)
	}

	// Limit search to prevent infinite loops (max 4 years)
	maxYear := t.Year() + 4

	for year := t.Year(); year <= maxYear; year++ {
		startMonth := 1
		if year == t.Year() {
			startMonth = int(t.Month())
		}
		if year != t.Year() {
			// For new years, start with the first valid month
			startMonth = s.findFirstValidValue(expr.Month)
		}

		// Find next valid month
		nextMonth, foundMonth := s.findNextValidValue(expr.Month, startMonth)
		if !foundMonth {
			continue
		}

		for month := nextMonth; month <= 12; {
			if !s.fieldMatches(expr.Month, month) {
				nextValidMonth, found := s.findNextValidValue(expr.Month, month+1)
				if !found {
					break
				}
				month = nextValidMonth
				continue
			}

			startDay := 1
			if year == t.Year() && month == int(t.Month()) {
				startDay = t.Day()
			}
			if year != t.Year() || month != int(t.Month()) {
				// For new months, start with the first valid day
				startDay = s.findFirstValidValue(expr.Day)
			}

			// Get the number of days in this month
			daysInMonth := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()

			nextDay, foundDay := s.findNextValidValue(expr.Day, startDay)
			if !foundDay || nextDay > daysInMonth {
				// No valid day this month, try next month
				nextValidMonth, found := s.findNextValidValue(expr.Month, month+1)
				if !found {
					break
				}
				month = nextValidMonth
				continue
			}

			for day := nextDay; day <= daysInMonth; {
				if !s.fieldMatches(expr.Day, day) {
					nextValidDay, found := s.findNextValidValue(expr.Day, day+1)
					if !found || nextValidDay > daysInMonth {
						break
					}
					day = nextValidDay
					continue
				}

				// Check if this date matches the day of week constraint
				testDate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, t.Location())
				if !s.fieldMatches(expr.DayOfWeek, int(testDate.Weekday())) {
					day++
					continue
				}

				startHour := 0
				if year == t.Year() && month == int(t.Month()) && day == t.Day() {
					startHour = t.Hour()
				}
				if year != t.Year() || month != int(t.Month()) || day != t.Day() {
					// For new days, start with the first valid hour
					startHour = s.findFirstValidValue(expr.Hour)
				}

				nextHour, foundHour := s.findNextValidValue(expr.Hour, startHour)
				if !foundHour {
					nextValidDay, found := s.findNextValidValue(expr.Day, day+1)
					if !found || nextValidDay > daysInMonth {
						break
					}
					day = nextValidDay
					continue
				}

				for hour := nextHour; hour <= 23; {
					if !s.fieldMatches(expr.Hour, hour) {
						nextValidHour, found := s.findNextValidValue(expr.Hour, hour+1)
						if !found {
							break
						}
						hour = nextValidHour
						continue
					}

					startMinute := s.findFirstValidValue(expr.Minute)
					if year == t.Year() && month == int(t.Month()) && day == t.Day() && hour == t.Hour() {
						startMinute = t.Minute()
					}

					nextMinute, foundMinute := s.findNextValidValue(expr.Minute, startMinute)
					if !foundMinute {
						nextValidHour, found := s.findNextValidValue(expr.Hour, hour+1)
						if !found {
							break
						}
						hour = nextValidHour
						continue
					}

					for minute := nextMinute; minute <= 59; {
						if !s.fieldMatches(expr.Minute, minute) {
							nextValidMinute, found := s.findNextValidValue(expr.Minute, minute+1)
							if !found {
								break
							}
							minute = nextValidMinute
							continue
						}

						if expr.Second != nil {
							startSecond := s.findFirstValidValue(*expr.Second)
							if year == t.Year() && month == int(t.Month()) && day == t.Day() &&
								hour == t.Hour() && minute == t.Minute() {
								startSecond = t.Second()
							}

							nextSecond, foundSecond := s.findNextValidValue(*expr.Second, startSecond)
							if !foundSecond {
								nextValidMinute, found := s.findNextValidValue(expr.Minute, minute+1)
								if !found {
									break
								}
								minute = nextValidMinute
								continue
							}

							for second := nextSecond; second <= 59; {
								if !s.fieldMatches(*expr.Second, second) {
									nextValidSecond, found := s.findNextValidValue(*expr.Second, second+1)
									if !found {
										break
									}
									second = nextValidSecond
									continue
								}
								return time.Date(year, time.Month(month), day, hour, minute, second, 0, t.Location()), nil
							}
						}

						if expr.Second == nil {
							return time.Date(year, time.Month(month), day, hour, minute, 0, 0, t.Location()), nil
						}

						nextValidMinute, found := s.findNextValidValue(expr.Minute, minute+1)
						if !found {
							break
						}
						minute = nextValidMinute
					}

					nextValidHour, found := s.findNextValidValue(expr.Hour, hour+1)
					if !found {
						break
					}
					hour = nextValidHour
				}

				nextValidDay, found := s.findNextValidValue(expr.Day, day+1)
				if !found || nextValidDay > daysInMonth {
					break
				}
				day = nextValidDay
			}

			nextValidMonth, found := s.findNextValidValue(expr.Month, month+1)
			if !found {
				break
			}
			month = nextValidMonth
		}
	}

	return time.Time{}, apperror.NewError("could not find next run time within reasonable limits")
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
