package leap

import (
	"bytes"
	"os"
	"testing"
	"time"

	leaphash "github.com/facebook/time/leaphash"
	"github.com/openshift/linuxptp-daemon/pkg/daemon/ublox"
	"github.com/stretchr/testify/assert"
)

func Test_AddLeapEvent(t *testing.T) {
	leapFile := "testdata/leap-seconds.list"
	leapTime := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	expirationTime := time.Date(2036, time.January, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, time.May, 8, 0, 0, 0, 0, time.UTC)
	leapSec := 38
	b, err := os.ReadFile(leapFile)
	assert.Equal(t, nil, err)
	leapData, err := ParseLeapFile(b)
	assert.Equal(t, nil, err)
	lm := &LeapManager{
		UbloxLsInd: make(chan ublox.TimeLs),
		Close:      make(chan bool),
		leapFile:   *leapData,
	}
	lm.AddLeapEvent(leapTime, leapSec, expirationTime, currentTime)

	buf, err := lm.RenderLeapData()
	assert.Equal(t, nil, err)
	hash := leaphash.Compute(buf.String())

	assert.Equal(t, hash, lm.leapFile.Hash)

}

func Test_ParseLeapFile(t *testing.T) {
	leapFile := "testdata/leap-seconds.list"
	b, err := os.ReadFile(leapFile)
	assert.Equal(t, nil, err)
	_, err = ParseLeapFile(b)
	assert.Equal(t, nil, err)
}

func Test_RenderLeapFile(t *testing.T) {
	leapFile := "testdata/leap-seconds.list"
	b, err := os.ReadFile(leapFile)
	assert.Equal(t, nil, err)
	leapData, err := ParseLeapFile(b)
	assert.Equal(t, nil, err)
	lm := &LeapManager{
		UbloxLsInd: make(chan ublox.TimeLs),
		Close:      make(chan bool),
		leapFile:   *leapData,
	}
	l, err := lm.RenderLeapData()

	assert.Equal(t, nil, err)
	desired, err := os.ReadFile("testdata/leap-seconds.list.rendered")
	assert.Equal(t, nil, err)
	assert.True(t, bytes.Equal(l.Bytes(), desired))
}
