package queue

import (
	"testing"
	"time"
)

const (
	cronEveryMinute      = "* * * * *"
	cronEverySecond      = "* * * * * *"
	cronEvery15Minutes   = "*/15 * * * *"
	cronDailyMidnight    = "0 0 * * *"
	cronLeapDay          = "0 0 29 2 *"
	cronImpossibleDate   = "0 0 30 2 *"
	cronBusinessHours    = "0 9-17/2 * * *"
	cronMinuteHourList   = "30 9,12,15 * * *"
	cronMondayMidnight   = "0 0 * * 1"
	cronFirstIfMonday    = "0 12 1 * 1"
	cronWeekend          = "0 0 * * 0,6"
	cronFridayThirteenth = "0 0 13 * 5"
	cronEvery20Seconds   = "*/20 * * * * *"
	cronFixedSecond      = "30 15 10 * * *"
	cronLastSecondOfYear = "59 59 23 31 12 *"
	cronThirtyFirst      = "0 0 31 * *"
	cronMonthNames       = "0 0 1 JAN,JUL *"
	cronMonthRange       = "0 0 1 3-4 *"
	cronSingleHour       = "0 10 * * *"
	cronMinuteFive       = "5 10 * * *"
	cronFifteenthTwoMon  = "0 0 15 2,11 *"
	cronInvalidSpec      = "not a cron spec"

	zoneOffsetSeconds = 5*60*60 + 30*60
	halfSecond        = 500 * time.Millisecond
)

func utc(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func TestCalculateNextCronRunCharacterization(t *testing.T) {
	zone := time.FixedZone("UTC+5:30", zoneOffsetSeconds)

	tests := []struct {
		name    string
		spec    string
		after   time.Time
		want    time.Time
		wantErr bool
	}{
		{name: "every minute advances one minute", spec: cronEveryMinute, after: utc(2024, time.March, 10, 12, 30, 15), want: utc(2024, time.March, 10, 12, 31, 0)},
		{name: "every minute rolls over the hour", spec: cronEveryMinute, after: utc(2024, time.March, 10, 12, 59, 30), want: utc(2024, time.March, 10, 13, 0, 0)},
		{name: "every minute rolls over the day", spec: cronEveryMinute, after: utc(2024, time.March, 10, 23, 59, 0), want: utc(2024, time.March, 11, 0, 0, 0)},
		{name: "every minute rolls over the month", spec: cronEveryMinute, after: utc(2024, time.January, 31, 23, 59, 0), want: utc(2024, time.February, 1, 0, 0, 0)},
		{name: "every minute rolls over the year", spec: cronEveryMinute, after: utc(2024, time.December, 31, 23, 59, 0), want: utc(2025, time.January, 1, 0, 0, 0)},
		{name: "fractional seconds are dropped for five fields", spec: cronEveryMinute, after: utc(2024, time.March, 10, 12, 30, 59).Add(halfSecond), want: utc(2024, time.March, 10, 12, 31, 0)},
		{name: "step is strictly after a matching minute", spec: cronEvery15Minutes, after: utc(2024, time.March, 10, 12, 15, 0), want: utc(2024, time.March, 10, 12, 30, 0)},
		{name: "step rolls into the next hour", spec: cronEvery15Minutes, after: utc(2024, time.March, 10, 12, 46, 0), want: utc(2024, time.March, 10, 13, 0, 0)},
		{name: "hour range with step finds next hour", spec: cronBusinessHours, after: utc(2024, time.March, 10, 10, 0, 0), want: utc(2024, time.March, 10, 11, 0, 0)},
		{name: "hour range with step moves to next day after last hour", spec: cronBusinessHours, after: utc(2024, time.March, 10, 17, 0, 0), want: utc(2024, time.March, 11, 9, 0, 0)},
		{name: "lists pick the next listed hour", spec: cronMinuteHourList, after: utc(2024, time.March, 10, 12, 30, 0), want: utc(2024, time.March, 10, 15, 30, 0)},
		{name: "lists move to the next day after the last entry", spec: cronMinuteHourList, after: utc(2024, time.March, 10, 15, 30, 0), want: utc(2024, time.March, 11, 9, 30, 0)},
		{name: "single hour is missed and moves to next day", spec: cronSingleHour, after: utc(2024, time.March, 10, 23, 30, 0), want: utc(2024, time.March, 11, 10, 0, 0)},
		{name: "minute already passed in the only hour moves to next day", spec: cronMinuteFive, after: utc(2024, time.March, 10, 10, 6, 0), want: utc(2024, time.March, 11, 10, 5, 0)},
		{name: "minute matching exactly now moves to next day", spec: cronMinuteFive, after: utc(2024, time.March, 10, 10, 5, 0), want: utc(2024, time.March, 11, 10, 5, 0)},
		{name: "daily crosses leap day", spec: cronDailyMidnight, after: utc(2024, time.February, 28, 12, 0, 0), want: utc(2024, time.February, 29, 0, 0, 0)},
		{name: "daily skips leap day in common year", spec: cronDailyMidnight, after: utc(2023, time.February, 28, 12, 0, 0), want: utc(2023, time.March, 1, 0, 0, 0)},
		{name: "leap day in the current leap year", spec: cronLeapDay, after: utc(2024, time.January, 1, 0, 0, 0), want: utc(2024, time.February, 29, 0, 0, 0)},
		{name: "leap day after it passed finds the next leap year", spec: cronLeapDay, after: utc(2024, time.March, 1, 0, 0, 0), want: utc(2028, time.February, 29, 0, 0, 0)},
		{name: "leap day from a common year", spec: cronLeapDay, after: utc(2025, time.January, 1, 0, 0, 0), want: utc(2028, time.February, 29, 0, 0, 0)},
		{name: "leap day search reaches the last year of the window", spec: cronLeapDay, after: utc(2028, time.March, 1, 0, 0, 0), want: utc(2032, time.February, 29, 0, 0, 0)},
		{name: "leap day search fails across a skipped leap year", spec: cronLeapDay, after: utc(2096, time.March, 1, 0, 0, 0), wantErr: true},
		{name: "date that never exists fails", spec: cronImpossibleDate, after: utc(2024, time.January, 1, 0, 0, 0), wantErr: true},
		{name: "day 31 skips short months", spec: cronThirtyFirst, after: utc(2024, time.April, 15, 0, 0, 0), want: utc(2024, time.May, 31, 0, 0, 0)},
		{name: "day 31 after matching midnight skips two short months", spec: cronThirtyFirst, after: utc(2024, time.January, 31, 0, 0, 0), want: utc(2024, time.March, 31, 0, 0, 0)},
		{name: "month names in a list", spec: cronMonthNames, after: utc(2024, time.February, 1, 0, 0, 0), want: utc(2024, time.July, 1, 0, 0, 0)},
		{name: "month range wraps to the next year", spec: cronMonthRange, after: utc(2024, time.May, 1, 0, 0, 0), want: utc(2025, time.March, 1, 0, 0, 0)},
		{name: "month list wraps to the next year from its last month", spec: cronFifteenthTwoMon, after: utc(2024, time.November, 15, 0, 0, 0), want: utc(2025, time.February, 15, 0, 0, 0)},
		{name: "weekday is next Monday from a Sunday", spec: cronMondayMidnight, after: utc(2024, time.March, 10, 0, 0, 0), want: utc(2024, time.March, 11, 0, 0, 0)},
		{name: "weekday is next week from the matching Monday", spec: cronMondayMidnight, after: utc(2024, time.March, 11, 0, 0, 0), want: utc(2024, time.March, 18, 0, 0, 0)},
		{name: "weekday list picks Saturday after Friday", spec: cronWeekend, after: utc(2024, time.March, 15, 12, 0, 0), want: utc(2024, time.March, 16, 0, 0, 0)},
		{name: "day of month and weekday must both match", spec: cronFirstIfMonday, after: utc(2024, time.January, 1, 12, 0, 0), want: utc(2024, time.April, 1, 12, 0, 0)},
		{name: "friday the thirteenth", spec: cronFridayThirteenth, after: utc(2024, time.January, 1, 0, 0, 0), want: utc(2024, time.September, 13, 0, 0, 0)},
		{name: "six fields every second", spec: cronEverySecond, after: utc(2024, time.March, 10, 12, 30, 15), want: utc(2024, time.March, 10, 12, 30, 16)},
		{name: "six fields second step drops fractional second", spec: cronEvery20Seconds, after: utc(2024, time.March, 10, 12, 0, 0).Add(halfSecond), want: utc(2024, time.March, 10, 12, 0, 20)},
		{name: "six fields second step rolls into next minute", spec: cronEvery20Seconds, after: utc(2024, time.March, 10, 12, 0, 40), want: utc(2024, time.March, 10, 12, 1, 0)},
		{name: "six fields fixed time later the same day", spec: cronFixedSecond, after: utc(2024, time.March, 10, 10, 15, 29), want: utc(2024, time.March, 10, 10, 15, 30)},
		{name: "six fields fixed time already reached moves to next day", spec: cronFixedSecond, after: utc(2024, time.March, 10, 10, 15, 30), want: utc(2024, time.March, 11, 10, 15, 30)},
		{name: "six fields last second of the year moves to next year", spec: cronLastSecondOfYear, after: utc(2024, time.December, 31, 23, 59, 59), want: utc(2025, time.December, 31, 23, 59, 59)},
		{name: "yearly preset", spec: "@yearly", after: utc(2024, time.June, 15, 8, 0, 0), want: utc(2025, time.January, 1, 0, 0, 0)},
		{name: "monthly preset", spec: "@monthly", after: utc(2024, time.March, 15, 8, 0, 0), want: utc(2024, time.April, 1, 0, 0, 0)},
		{name: "weekly preset runs on Sunday", spec: "@weekly", after: utc(2024, time.March, 13, 8, 0, 0), want: utc(2024, time.March, 17, 0, 0, 0)},
		{name: "daily preset", spec: "@daily", after: utc(2024, time.March, 13, 8, 0, 0), want: utc(2024, time.March, 14, 0, 0, 0)},
		{name: "hourly preset", spec: "@hourly", after: utc(2024, time.March, 13, 8, 30, 0), want: utc(2024, time.March, 13, 9, 0, 0)},
		{name: "result keeps the location of the input", spec: cronDailyMidnight, after: time.Date(2024, time.March, 10, 23, 45, 0, 0, zone), want: time.Date(2024, time.March, 11, 0, 0, 0, 0, zone)},
		{name: "weekday is evaluated in the location of the input", spec: cronMondayMidnight, after: time.Date(2024, time.March, 10, 23, 45, 0, 0, zone), want: time.Date(2024, time.March, 11, 0, 0, 0, 0, zone)},
		{name: "invalid specification fails", spec: cronInvalidSpec, after: utc(2024, time.March, 10, 0, 0, 0), wantErr: true},
	}

	scheduler := NewTaskScheduler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scheduler.calculateNextCronRun(tt.spec, tt.after)
			if (err != nil) != tt.wantErr {
				t.Fatalf("calculateNextCronRun(%q, %v) error = %v, wantErr %v", tt.spec, tt.after, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !got.Equal(tt.want) || got.Location().String() != tt.want.Location().String() {
				t.Errorf("calculateNextCronRun(%q, %v) = %v, want %v", tt.spec, tt.after, got, tt.want)
			}
		})
	}
}
