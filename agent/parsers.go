package main

import (
	"math"
	"strconv"
	"strings"
)

func parsePercent(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	return parsed
}

func parseBytes(value string) float64 {
	text := strings.TrimSpace(strings.SplitN(value, "/", 2)[0])
	if text == "" {
		return 0
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0
	}
	numberText := fields[0]
	unit := "B"
	if len(fields) > 1 {
		unit = fields[1]
	} else {
		for i, char := range numberText {
			if (char < '0' || char > '9') && char != '.' {
				unit = numberText[i:]
				numberText = numberText[:i]
				break
			}
		}
	}
	number, err := strconv.ParseFloat(numberText, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0
	}
	multipliers := map[string]float64{
		"b":   1,
		"kb":  1000,
		"mb":  1000 * 1000,
		"gb":  1000 * 1000 * 1000,
		"tb":  1000 * 1000 * 1000 * 1000,
		"kib": 1024,
		"mib": 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
	}
	return math.Round(number * multipliers[strings.ToLower(unit)])
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func labelValue(labels string, key string) string {
	for _, part := range strings.Split(labels, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
