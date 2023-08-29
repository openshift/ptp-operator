package protocol

import (
	"fmt"
	"github.com/golang/glog"
	"strconv"
	"strings"

	"github.com/facebook/time/ptp/protocol"
)

// extend fbprotocol.ClockClass according to https://www.itu.int/rec/T-REC-G.8275.1-202211-I/en section 6.4 table 3
const (
	ClockClassFreerun       protocol.ClockClass = 248
	ClockClassUninitialized protocol.ClockClass = 0
	ClockClassOutOfSpec     protocol.ClockClass = 140
)

type GrandmasterSettings struct {
	ClockQuality     protocol.ClockQuality
	TimePropertiesDS TimePropertiesDS
}

type TimePropertiesDS struct {
	CurrentUtcOffset      int32
	CurrentUtcOffsetValid bool
	Leap59                bool
	Leap61                bool
	TimeTraceable         bool
	FrequencyTraceable    bool
	PtpTimescale          bool
	TimeSource            protocol.TimeSource
}

func (g *GrandmasterSettings) String() string {
	if g == nil {
		glog.Error("returned empty grandmasterSettings")
		return ""

	}
	result := fmt.Sprintf(" clockClass              %d\n", g.ClockQuality.ClockClass)
	result += fmt.Sprintf(" clockAccuracy           0x%x\n", g.ClockQuality.ClockAccuracy)
	result += fmt.Sprintf(" offsetScaledLogVariance 0x%x\n", g.ClockQuality.OffsetScaledLogVariance)
	result += fmt.Sprintf(" currentUtcOffset        %d\n", g.TimePropertiesDS.CurrentUtcOffset)
	result += fmt.Sprintf(" leap61                  %d\n", btoi(g.TimePropertiesDS.Leap61))
	result += fmt.Sprintf(" leap59                  %d\n", btoi(g.TimePropertiesDS.Leap59))
	result += fmt.Sprintf(" currentUtcOffsetValid   %d\n", btoi(g.TimePropertiesDS.CurrentUtcOffsetValid))
	result += fmt.Sprintf(" ptpTimescale            %d\n", btoi(g.TimePropertiesDS.PtpTimescale))
	result += fmt.Sprintf(" timeTraceable           %d\n", btoi(g.TimePropertiesDS.TimeTraceable))
	result += fmt.Sprintf(" frequencyTraceable      %d\n", btoi(g.TimePropertiesDS.FrequencyTraceable))
	result += fmt.Sprintf(" timeSource              0x%x\n", uint(g.TimePropertiesDS.TimeSource))
	return result
}

// Keys returns variables names in order of pmc command results
func (g *GrandmasterSettings) Keys() []string {
	return []string{"clockClass", "clockAccuracy", "offsetScaledLogVariance",
		"currentUtcOffset", "leap61", "leap59", "currentUtcOffsetValid",
		"ptpTimescale", "timeTraceable", "frequencyTraceable", "timeSource"}
}

func (g *GrandmasterSettings) ValueRegEx() map[string]string {
	return map[string]string{
		"clockClass":              `(\d+)`,
		"clockAccuracy":           `(0x[\da-f]+)`,
		"offsetScaledLogVariance": `(0x[\da-f]+)`,
		"currentUtcOffset":        `(\d+)`,
		"currentUtcOffsetValid":   `([01])`,
		"leap59":                  `([01])`,
		"leap61":                  `([01])`,
		"timeTraceable":           `([01])`,
		"frequencyTraceable":      `([01])`,
		"ptpTimescale":            `([01])`,
		"timeSource":              `(0x[\da-f]+)`,
	}
}

func (g *GrandmasterSettings) RegEx() string {
	result := ""
	for _, k := range g.Keys() {
		result += `[[:space:]]+` + k + `[[:space:]]+` + g.ValueRegEx()[k]
	}
	return result
}

func (g *GrandmasterSettings) Update(key string, value string) {
	switch key {
	case "clockClass":
		g.ClockQuality.ClockClass = protocol.ClockClass(stou8(value))
	case "clockAccuracy":
		g.ClockQuality.ClockAccuracy = protocol.ClockAccuracy(stou8h(value))
	case "offsetScaledLogVariance":
		g.ClockQuality.OffsetScaledLogVariance = stou16h(value)
	case "currentUtcOffset":
		g.TimePropertiesDS.CurrentUtcOffset = stoi32(value)
	case "currentUtcOffsetValid":
		g.TimePropertiesDS.CurrentUtcOffsetValid = stob(value)
	case "leap59":
		g.TimePropertiesDS.Leap59 = stob(value)
	case "leap61":
		g.TimePropertiesDS.Leap61 = stob(value)
	case "timeTraceable":
		g.TimePropertiesDS.TimeTraceable = stob(value)
	case "frequencyTraceable":
		g.TimePropertiesDS.FrequencyTraceable = stob(value)
	case "ptpTimescale":
		g.TimePropertiesDS.PtpTimescale = stob(value)
	case "timeSource":
		g.TimePropertiesDS.TimeSource = protocol.TimeSource(stou8h(value))
	}
}

func btoi(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func stob(s string) bool {
	if s == "1" {
		return true
	}
	return false
}

func stou8(s string) uint8 {
	uint64Value, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	return uint8(uint64Value)
}

func stou8h(s string) uint8 {
	uint64Value, err := strconv.ParseUint(strings.Replace(s, "0x", "", 1), 16, 8)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	return uint8(uint64Value)
}

func stou16h(s string) uint16 {
	uint64Value, err := strconv.ParseUint(strings.Replace(s, "0x", "", 1), 16, 16)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	return uint16(uint64Value)
}

func stoi32(s string) int32 {
	int64Value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	return int32(int64Value)
}
