package ublox_test

import (
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/daemon/ublox"
	"github.com/stretchr/testify/assert"
	"regexp"
	"testing"
)

type gnssAntennaTest struct {
	cmd                 string
	expectedResult      string
	expectedAntStatus   ublox.ANT_STATUS
	expectedPowerStatus ublox.POWER_STATUS
	status              bool
}

func TestFindProtoVersion(t *testing.T) {
	/*result := `connected to tcp://localhost:2947
	ubxtool: poll MON-VER

	sent:
	UBX-MON-VER:
	  Poll request

	UBX-MON-VER:
	  swVersion EXT CORE 1.00 (3fda8e)
	  hwVersion 00190000
	  extension ROM BASE 0x118B2060
	  extension FWVER=TIM 2.20
	  extension PROTVER=29.20
	  extension MOD=ZED-F9T
	  extension GPS;GLO;GAL;BDS
	  extension SBAS;QZSS
	  extension NAVIC
	WARNING:  protVer is 10.00, should be 29.20.  Hint: use option "-P 29.20"
	`
		/*fValue, err := ublox.FindProtoVersion(result)
		assert.Nil(t, err)
		assert.Equal(t, 29.20, fValue)*/
}

/*func TestUblox_AntStatus(t *testing.T) {
	tests := []gnssAntennaTest{
		{cmd: "-v 1 -p MON-RF", expectedResult: `UBX-MON-RF:
			Poll request
			UBX-MON-RF:
			version 0 nBlocks 2 reserved1 0 0
			blockId 0 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0
			blockId 1 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0`,
			expectedAntStatus: ublox.OK, expectedPowerStatus: ublox.ON, status: true},
		{cmd: "-v 1 -p MON-RF", expectedResult: `UBX-MON-RF:
			Poll request
			UBX-MON-RF:
			version 0 nBlocks 2 reserved1 0 0
			blockId 0 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0
			blockId 1 flags x0 antStatus 1 antPower 0 postStatus 0 reserved2 0 0 0 0`,
			expectedAntStatus: ublox.NOT_OK, expectedPowerStatus: ublox.OFF, status: false},
		{cmd: "-v 1 -p MON-RF", expectedResult: `UBX-MON-RF:
			Poll request
			UBX-MON-RF:
			version 0 nBlocks 2 reserved1 0 0
			blockId 0 flags x0 antStatus 0 antPower 1 postStatus 0 reserved2 0 0 0 0
			blockId 1 flags x0 antStatus 0 antPower 1 postStatus 0 reserved2 0 0 0 0`,
			expectedAntStatus: ublox.NOT_OK, expectedPowerStatus: ublox.OFF, status: false},
		{cmd: "-v 1 -p MON-RF", expectedResult: `UBX-MON-RF:
			 Poll request
			UBX-MON-RF:
			version 0 nBlocks 2 reserved1 0 0
            blockId 0 flags x0 antStatus 2 antPower 1 postStatus 0 reserved2 0 0 0 0`,
			expectedAntStatus: ublox.OK, expectedPowerStatus: ublox.ON, status: true},
	}

	for _, tc := range tests {
		u, e := ublox.NewMockUblox(func(cmdString string) ([]byte, error) {
			return []byte(tc.expectedResult), nil
		})
		assert.Nil(t, e)
		status, antOk := u.GNSSAntStatus()
		assert.Equal(t, tc.status, antOk)
		assert.Equal(t, tc.expectedAntStatus, status.Status())
		assert.Equal(t, tc.expectedPowerStatus, status.Power())
	}

}*/

func Test_Query(t *testing.T) {
	t.Skip("Skipping test, ubxtool needs to be installed")
	u := ublox.UBlox{}
	//assert.Nil(t, e)
	assert.NotNil(t, u)
	ss, s, err := u.Query("-V", regexp.MustCompile("Version"))
	assert.NotEmpty(t, ss)
	assert.NotEmpty(t, s[0])
	assert.Nil(t, err)
}

func Test_ExtractLeapSec(t *testing.T) {
	var data = []string{
		"iTOW 376008000 version 0 reserved2 0 0 0 srcOfCurrLs 2",
		"currLs 18 srcOfLsChange 2 lsChange 0 timeToLsEvent 77643210",
		"dateOfLsGpsWn 2441 dateOfLsGpsDn 7 reserved2 0 0 0",
		"valid x3",
	}
	res := ublox.ExtractLeapSec(data)
	desired := &ublox.TimeLs{
		SrcOfCurrLs:   2,
		CurrLs:        18,
		SrcOfLsChange: 2,
		LsChange:      0,
		TimeToLsEvent: 77643210,
		DateOfLsGpsWn: 2441,
		DateOfLsGpsDn: 7,
		Valid:         3,
	}
	assert.Equal(t, desired, res, "TimeLs parsing result mismatch")
}
