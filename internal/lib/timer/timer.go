package timer

import (
	"time"
)

func ParseTime(timeStr string) (time.Time, error) {
	return ParseTimeInLocation(timeStr, time.Local)
}

func ParseTimeInLocation(timeStr string, loc *time.Location) (time.Time, error) {
	now := time.Now().In(loc)
	parsedTime, err := time.Parse("15:04", timeStr)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(now.Year(), now.Month(), now.Day(), parsedTime.Hour(), parsedTime.Minute(), 0, 0, loc), nil
}

func LoadLocation(timezone string) (*time.Location, error) {
	if timezone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	return loc, nil
}
