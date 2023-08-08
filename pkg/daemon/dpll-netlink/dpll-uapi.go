// Port definitions from <kernel-root>/include/uapi/linux/dpll.h and
// tools/net/ynl/generated/dpll-user.h

package dpll_netlink

import "fmt"

const DPLL_MCGRP_MONITOR = "monitor"
const (
	DPLL_A_TYPES = iota
	DPLL_A_ID
	DPLL_A_MODULE_NAME
	DPLL_A_CLOCK_ID
	DPLL_A_MODE
	DPLL_A_MODE_SUPPORTED
	DPLL_A_LOCK_STATUS
	DPLL_A_TEMP
	DPLL_A_TYPE
	DPLL_A_PIN_ID
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
	DPLL_A_PIN_DPLL_CAPS
	DPLL_A_PIN_PARENT

	__DPLL_A_MAX
	DPLL_A_MAX = __DPLL_A_MAX - 1
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
func GetLockStatus(ls uint8) string {
	lockStatusMap := map[uint8]string{
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
func GetDpllType(tp uint8) string {
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
func GetMode(md uint8) string {
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
		// TODO: ModeSupported
		LockStatus: GetLockStatus(reply.LockStatus),
		ClockId:    fmt.Sprintf("0x%x", reply.ClockId),
		Type:       GetDpllType(reply.Type),
	}
}
