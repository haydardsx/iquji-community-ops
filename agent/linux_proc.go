package main

import (
	"bufio"
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

func cpuPercent() float64 {
	read := func() (idle float64, total float64) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0
		}
		line := strings.SplitN(string(data), "\n", 2)[0]
		fields := strings.Fields(strings.TrimPrefix(line, "cpu"))
		values := make([]float64, len(fields))
		for i, field := range fields {
			value, _ := strconv.ParseFloat(field, 64)
			values[i] = value
			total += value
		}
		if len(values) > 3 {
			idle = values[3]
		}
		if len(values) > 4 {
			idle += values[4]
		}
		return idle, total
	}
	firstIdle, firstTotal := read()
	time.Sleep(250 * time.Millisecond)
	secondIdle, secondTotal := read()
	idle := secondIdle - firstIdle
	total := secondTotal - firstTotal
	if total <= 0 {
		return 0
	}
	return clampPercent(((total - idle) / total) * 100)
}

func memoryPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	values := map[string]float64{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, _ := strconv.ParseFloat(fields[1], 64)
		values[key] = value
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total <= 0 {
		return 0
	}
	return ((total - available) / total) * 100
}

func diskPercent() float64 {
	output, err := run("df", "-Pk", "/")
	if err != nil {
		return 0
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return 0
	}
	return parsePercent(fields[4])
}

func dockerDiskBytes() float64 {
	var total float64
	for _, row := range dockerJSONRows("system", "df", "--format", "{{json .}}") {
		total += parseBytes(row.String("Size"))
	}
	return total
}
