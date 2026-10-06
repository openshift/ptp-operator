package ptphelper

import (
	"regexp"
	"testing"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProfileNameMatchPattern(t *testing.T) {
	pattern := ProfileNameMatchPattern("temp", "temp")
	re, err := regexp.Compile(`#profile:\s*` + pattern)
	if err != nil {
		t.Fatalf("compile pattern: %v", err)
	}

	if !re.MatchString("#profile: temp") {
		t.Errorf("pattern %q should match unqualified profile", pattern)
	}
	if !re.MatchString("#profile: temp_temp") {
		t.Errorf("pattern %q should match qualified profile", pattern)
	}
	if re.MatchString("#profile: other") {
		t.Errorf("pattern %q should not match unrelated profile", pattern)
	}
}

func TestProfileNameLogPattern(t *testing.T) {
	// GetPodLogsRegex appends \s*^ when isLiteralText is false.
	const matchOnlyFullLines = `\s*^`
	re, err := regexp.Compile(ProfileNameLogPattern("temp", "temp") + matchOnlyFullLines)
	if err != nil {
		t.Fatalf("compile pattern: %v", err)
	}

	unqualified := "I0819 10:48:18.897 daemon.go:410] Profile Name: temp\nnext line\n"
	qualified := "I0819 10:48:18.897 daemon.go:410] Profile Name: temp_temp\nnext line\n"
	if !re.MatchString(unqualified) {
		t.Errorf("log pattern should match unqualified Profile Name line")
	}
	if !re.MatchString(qualified) {
		t.Errorf("log pattern should match qualified Profile Name line")
	}

	oldRe, err := regexp.Compile("Profile Name: " + ProfileNameMatchPattern("temp", "temp") + matchOnlyFullLines)
	if err != nil {
		t.Fatalf("compile old pattern: %v", err)
	}
	if oldRe.MatchString(unqualified) {
		t.Errorf("pattern without (?m) should not match a klog Profile Name line")
	}
}

func TestGetProfileName(t *testing.T) {
	t.Run("returns CR profile name without qualifying", func(t *testing.T) {
		profileName := pkg.PtpTempPolicyName
		config := &ptpv1.PtpConfig{
			ObjectMeta: metav1.ObjectMeta{Name: pkg.PtpTempPolicyName},
			Spec: ptpv1.PtpConfigSpec{
				Profile: []ptpv1.PtpProfile{{Name: &profileName}},
			},
		}

		got, err := GetProfileName(config, true)
		if err != nil {
			t.Fatalf("GetProfileName: %v", err)
		}
		if got != pkg.PtpTempPolicyName {
			t.Errorf("GetProfileName() = %q, want %q", got, pkg.PtpTempPolicyName)
		}
	})

	t.Run("receiverOnly skips tbc-tt", func(t *testing.T) {
		tt := "tbc-tt"
		tr := "tbc-tr"
		config := &ptpv1.PtpConfig{
			ObjectMeta: metav1.ObjectMeta{Name: pkg.PTPWPCTBCPolicyName},
			Spec: ptpv1.PtpConfigSpec{
				Profile: []ptpv1.PtpProfile{
					{Name: &tt},
					{Name: &tr},
				},
			},
		}

		got, err := GetProfileName(config, true)
		if err != nil {
			t.Fatalf("GetProfileName: %v", err)
		}
		if got != "tbc-tr" {
			t.Errorf("GetProfileName() = %q, want %q", got, "tbc-tr")
		}
	})
}
