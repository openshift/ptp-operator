package v1

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPtpConfigValidator_MinOffsetThresholdAccepted(t *testing.T) {
	profileName := "test-profile"
	ptpConfig := &PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ptpconfig",
			Namespace: "openshift-ptp",
		},
		Spec: PtpConfigSpec{
			Profile: []PtpProfile{
				{
					Name: &profileName,
					PtpClockThreshold: &PtpClockThreshold{
						HoldOverTimeout:    5,
						MaxOffsetThreshold: 100,
						MinOffsetThreshold: -100,
					},
				},
			},
		},
	}

	validator := &ptpConfigValidator{}
	ctx := context.Background()

	warnings, err := validator.ValidateCreate(ctx, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)

	warnings, err = validator.ValidateUpdate(ctx, ptpConfig, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
}

func newTestPtpConfigWithSettings(settings map[string]string) *PtpConfig {
	profileName := "test-profile"
	return &PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ptpconfig",
			Namespace: "openshift-ptp",
		},
		Spec: PtpConfigSpec{
			Profile: []PtpProfile{
				{
					Name:        &profileName,
					PtpSettings: settings,
				},
			},
		},
	}
}

func TestPtpConfigValidator_OSClockThresholdFieldsAccepted(t *testing.T) {
	// The OS-clock E3 settings are typed *int64 fields on PtpClockThreshold. The
	// admission webhook must accept them (they are validated for type/int by the
	// CRD schema) and preserve them through create/update.
	inSync := int64(50)
	outOfSync := int64(200)
	samples := int64(8)
	profileName := "test-profile"
	ptpConfig := &PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ptpconfig",
			Namespace: "openshift-ptp",
		},
		Spec: PtpConfigSpec{
			Profile: []PtpProfile{
				{
					Name: &profileName,
					PtpClockThreshold: &PtpClockThreshold{
						HoldOverTimeout:             5,
						MaxOffsetThreshold:          100,
						SysOffsetInSyncThreshold:    &inSync,
						SysOffsetOutOfSyncThreshold: &outOfSync,
						SysOffsetSamples:            &samples,
					},
				},
			},
		},
	}

	validator := &ptpConfigValidator{}
	ctx := context.Background()

	warnings, err := validator.ValidateCreate(ctx, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, int64(50), *ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetInSyncThreshold)
	assert.Equal(t, int64(200), *ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetOutOfSyncThreshold)
	assert.Equal(t, int64(8), *ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetSamples)

	warnings, err = validator.ValidateUpdate(ctx, ptpConfig, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestPtpConfigValidator_OSClockThresholdFieldsUnsetAccepted(t *testing.T) {
	// When unset (nil), the fields are accepted and the daemon applies defaults
	// (maxOffsetThreshold for thresholds, 10 for samples).
	profileName := "test-profile"
	ptpConfig := &PtpConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ptpconfig",
			Namespace: "openshift-ptp",
		},
		Spec: PtpConfigSpec{
			Profile: []PtpProfile{
				{
					Name: &profileName,
					PtpClockThreshold: &PtpClockThreshold{
						HoldOverTimeout:    5,
						MaxOffsetThreshold: 100,
					},
				},
			},
		},
	}

	validator := &ptpConfigValidator{}
	ctx := context.Background()

	warnings, err := validator.ValidateCreate(ctx, ptpConfig)
	assert.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Nil(t, ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetInSyncThreshold)
	assert.Nil(t, ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetOutOfSyncThreshold)
	assert.Nil(t, ptpConfig.Spec.Profile[0].PtpClockThreshold.SysOffsetSamples)
}

func TestPtpConfigValidator_ExistingProfilesUnaffected(t *testing.T) {
	validator := &ptpConfigValidator{}
	ctx := context.Background()

	t.Run("nil PtpSettings accepted", func(t *testing.T) {
		ptpConfig := newTestPtpConfigWithSettings(nil)

		warnings, err := validator.ValidateCreate(ctx, ptpConfig)
		assert.NoError(t, err)
		assert.Empty(t, warnings)
	})

	t.Run("pre-existing key without sysOffset* keys accepted", func(t *testing.T) {
		ptpConfig := newTestPtpConfigWithSettings(map[string]string{"inSyncConditionThreshold": "500"})

		warnings, err := validator.ValidateCreate(ctx, ptpConfig)
		assert.NoError(t, err)
		assert.Empty(t, warnings)
	})
}

func TestPtpConfigValidator_PtpSettingsAccepted(t *testing.T) {
	validator := &ptpConfigValidator{}
	ctx := context.Background()

	validSettings := []map[string]string{
		{"stdoutFilter": ".*ptp4l.*"},
		{"logReduce": "enhanced 30s 10"},
		{"haProfiles": "profile-a,profile-b"},
		{"clockType": "T-BC"},
		{"inSyncConditionTimes": "4"},
		{"clockId": "8888888888"},
		{"controllingProfile": "profile-a"},
		{"upstreamPort": "eth0"},
		{"leadingInterface": "eth0"},
	}

	for i, settings := range validSettings {
		t.Run(fmt.Sprintf("accepted %d", i), func(t *testing.T) {
			ptpConfig := newTestPtpConfigWithSettings(settings)

			warnings, err := validator.ValidateCreate(ctx, ptpConfig)
			assert.NoError(t, err)
			assert.Empty(t, warnings)

			warnings, err = validator.ValidateUpdate(ctx, ptpConfig, ptpConfig)
			assert.NoError(t, err)
			assert.Empty(t, warnings)
		})
	}
}

func TestPtpConfigValidator_PtpSettingsRejected(t *testing.T) {
	validator := &ptpConfigValidator{}
	ctx := context.Background()

	invalidSettings := []struct {
		name string
		key  string
		val  string
	}{
		{"invalid stdoutFilter", "stdoutFilter", "["},
		{"invalid logReduce mode", "logReduce", "verbose"},
		{"logReduce invalid duration", "logReduce", "enhanced notaduration"},
		{"logReduce invalid threshold", "logReduce", "enhanced 30s -5"},
		{"invalid haProfiles", "haProfiles", "not a profile"},
		{"invalid clockType", "clockType", "OCX"},
		{"invalid inSyncConditionTimes", "inSyncConditionTimes", "-1"},
		{"invalid clockId", "clockId", "not-a-clock-id"},
		{"unknown setting rejected", "sysOffsetThreshold", "100"},
	}

	for _, tt := range invalidSettings {
		t.Run(tt.name, func(t *testing.T) {
			ptpConfig := newTestPtpConfigWithSettings(map[string]string{tt.key: tt.val})

			_, err := validator.ValidateCreate(ctx, ptpConfig)
			assert.Error(t, err)
		})
	}
}
