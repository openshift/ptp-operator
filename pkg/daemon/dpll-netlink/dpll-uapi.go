// Port definitions from <kernel-root>/include/uapi/linux/dpll.h and
// tools/net/ynl/generated/dpll-user.h

package dpll_netlink

import (
	"encoding/json"
	"fmt"
)

const DPLL_MCGRP_MONITOR = "monitor"
const DPLL_PHASE_OFFSET_DIVIDER = 1000
const DPLL_TEMP_DIVIDER = 1000
const (
	DPLL_A_TYPES = iota
	DPLL_A_ID
	DPLL_A_MODULE_NAME
	DPLL_A_PAD
	DPLL_A_CLOCK_ID
	DPLL_A_MODE
	DPLL_A_MODE_SUPPORTED
	DPLL_A_LOCK_STATUS
	DPLL_A_TEMP
	DPLL_A_TYPE

	__DPLL_A_MAX
	DPLL_A_MAX = __DPLL_A_MAX - 1
)

const (
	DPLL_A_PIN_TYPES = iota

	DPLL_A_PIN_ID
	DPLL_A_PIN_PARENT_ID
	DPLL_A_PIN_MODULE_NAME
	DPLL_A_PIN_PAD
	DPLL_A_PIN_CLOCK_ID
	DPLL_A_PIN_BOARD_LABEL
	DPLL_A_PIN_PANEL_LABEL
	DPLL_A_PIN_PACKAGE_LABEL
	DPLL_A_PIN_TYPE
	DPLL_A_PIN_DIRECTION
	DPLL_A_PIN_FREQUENCY
	DPLL_A_PIN_FREQUENCY_SUPPORTED
	DPLL_A_PIN_FREQUENCY_MIN
	DPLL_A_PIN_FREQUENCY_MAX
	DPLL_A_PIN_PRIO
	DPLL_A_PIN_STATE
	DPLL_A_PIN_CAPABILITIES
	DPLL_A_PIN_PARENT_DEVICE
	DPLL_A_PIN_PARENT_PIN
	DPLL_A_PIN_PHASE_ADJUST_MIN
	DPLL_A_PIN_PHASE_ADJUST_MAX
	DPLL_A_PIN_PHASE_ADJUST
	DPLL_A_PIN_PHASE_OFFSET
	DPLL_A_PIN_FRACTIONAL_FREQUENCY_OFFSET

	__DPLL_A_PIN_MAX
	DPLL_A_PIN_MAX = __DPLL_A_PIN_MAX - 1
)
const (
	DPLL_CMDS = iota
	DPLL_CMD_DEVICE_ID_GET
	DPLL_CMD_DEVICE_GET
	DPLL_CMD_DEVICE_SET
	DPLL_CMD_DEVICE_CREATE_NTF
	DPLL_CMD_DEVICE_DELETE_NTF
	DPLL_CMD_DEVICE_CHANGE_NTF
	DPLL_CMD_PIN_ID_GET
	DPLL_CMD_PIN_GET
	DPLL_CMD_PIN_SET
	DPLL_CMD_PIN_CREATE_NTF
	DPLL_CMD_PIN_DELETE_NTF
	DPLL_CMD_PIN_CHANGE_NTF

	__DPLL_CMD_MAX
	DPLL_CMD_MAX = (__DPLL_CMD_MAX - 1)
)

// GetLockStatus returns DPLL lock status as a string
func GetLockStatus(ls uint32) string {
	lockStatusMap := map[uint32]string{
		1: "unlocked",
		2: "locked",
		3: "locked-ho-acquired",
		4: "holdover",
	}
	status, found := lockStatusMap[ls]
	if found {
		return status
	}
	return ""
}

// GetDpllType returns DPLL type as a string
func GetDpllType(tp uint32) string {
	typeMap := map[int]string{
		1: "pps",
		2: "eec",
	}
	typ, found := typeMap[int(tp)]
	if found {
		return typ
	}
	return ""
}

// GetMode returns DPLL mode as a string
func GetMode(md uint32) string {
	modeMap := map[int]string{
		1: "manual",
		2: "automatic",
		3: "holdover",
		4: "freerun",
	}
	mode, found := modeMap[int(md)]
	if found {
		return mode
	}
	return ""
}

// DpllStatusHR represents human-readable DPLL status
type DpllStatusHR struct {
	Id            uint32
	ModuleName    string
	Mode          string
	ModeSupported string
	LockStatus    string
	ClockId       string
	Type          string
}

// GetDpllStatusHR returns human-readable DPLL status
func GetDpllStatusHR(reply *DoDeviceGetReply) DpllStatusHR {
	return DpllStatusHR{
		Id:         reply.Id,
		ModuleName: reply.ModuleName,
		Mode:       GetMode(reply.Mode),
		LockStatus: GetLockStatus(reply.LockStatus),
		ClockId:    fmt.Sprintf("0x%x", reply.ClockId),
		Type:       GetDpllType(reply.Type),
	}
}

// DoPinGetReply is used with the DoPinGet method.
type DoPinGetReplyHR struct {
	Id                        uint32            `json:"id"`
	ClockId                   uint64            `json:"clockId"`
	BoardLabel                string            `json:"boardLabel"`
	PanelLabel                string            `json:"panelLabel"`
	PackageLabel              string            `json:"packageLabel"`
	Type                      string            `json:"type"`
	Frequency                 uint64            `json:"frequency"`
	FrequencySupported        FrequencyRange    `json:"frequencySupported"`
	Capabilities              string            `json:"capabilities"`
	ParentDevice              PinParentDeviceHR `json:"pinParentDevice"`
	ParentPin                 PinParentPinHR    `json:"pinParentPin"`
	PhaseAdjustMin            int32             `json:"phaseAdjustMin"`
	PhaseAdjustMax            int32             `json:"phaseAdjustMax"`
	PhaseAdjust               int32             `json:"phaseAdjust"`
	FractionalFrequencyOffset int               `json:"fractionalFrequencyOffset"`
	ModuleName                string            `json:"moduleName"`
}

// PinParentDevice contains nested netlink attributes.
type PinParentDeviceHR struct {
	ParentId    uint32 `json:"parentId"`
	Direction   string `json:"direction"`
	Prio        uint32 `json:"prio"`
	State       string `json:"state"`
	PhaseOffset int64  `json:"phaseOffset"`
}

// PinParentPin contains nested netlink attributes.
type PinParentPinHR struct {
	ParentId uint32 `json:"parentId"`
	State    string `json:"parentState"`
}

// GetPinState returns DPLL pin state as a string
func GetPinState(s uint32) string {
	stateMap := map[int]string{
		1: "connected",
		2: "disconnected",
		3: "selectable",
	}
	r, found := stateMap[int(s)]
	if found {
		return r
	}
	return ""
}

// GetPinType returns DPLL pin type as a string
func GetPinType(tp uint32) string {
	typeMap := map[int]string{
		1: "mux",
		2: "ext",
		3: "synce-eth-port",
		4: "int-oscillator",
		5: "gnss",
	}
	typ, found := typeMap[int(tp)]
	if found {
		return typ
	}
	return ""
}

// GetPinDirection returns DPLL pin direction as a string
func GetPinDirection(d uint32) string {
	directionMap := map[int]string{
		1: "input",
		2: "output",
	}
	dir, found := directionMap[int(d)]
	if found {
		return dir
	}
	return ""
}

// GetPinCapabilities returns DPLL pin capabilities as a csv
func GetPinCapabilities(c uint32) string {
	cMap := map[int]string{
		0: "",
		1: "direction-can-change",
		2: "priority-can-change",
		3: "direction-can-change,priority-can-change",
		4: "state-can-change",
		5: "state-can-change,direction-can-change",
		6: "state-can-change,priority-can-change",
		7: "state-can-change,direction-can-change,priority-can-change",
	}
	cap, found := cMap[int(c)]
	if found {
		return cap
	}
	return ""
}

// GetPinInfoHR returns human-readable pin status
func GetPinInfoHR(reply *DoPinGetReply) ([]byte, error) {
	hr := DoPinGetReplyHR{
		Id:                 reply.Id,
		ClockId:            reply.ClockId,
		BoardLabel:         reply.BoardLabel,
		PanelLabel:         reply.PanelLabel,
		PackageLabel:       reply.PackageLabel,
		Type:               GetPinType(reply.Type),
		Frequency:          reply.Frequency,
		FrequencySupported: reply.FrequencySupported,
		Capabilities:       GetPinCapabilities(reply.Capabilities),
		ParentDevice: PinParentDeviceHR{
			ParentId:    reply.ParentDevice.ParentId,
			Direction:   GetPinDirection(reply.ParentDevice.Direction),
			Prio:        reply.ParentDevice.Prio,
			State:       GetPinState(reply.ParentDevice.State),
			PhaseOffset: reply.ParentDevice.PhaseOffset,
		},
		ParentPin: PinParentPinHR{
			ParentId: reply.ParentPin.ParentId,
			State:    GetPinState(reply.ParentPin.State),
		},
		PhaseAdjustMin:            reply.PhaseAdjustMin,
		PhaseAdjustMax:            reply.PhaseAdjustMax,
		PhaseAdjust:               reply.PhaseAdjust,
		FractionalFrequencyOffset: reply.FractionalFrequencyOffset,
		ModuleName:                reply.ModuleName,
	}
	return json.Marshal(hr)
}
