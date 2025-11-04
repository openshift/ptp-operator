package v2alpha1

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWPCHardwareConfigValidation(t *testing.T) {
	// Load the test data file
	testFile := filepath.Join("testdata", "wpc-hwconfig.yaml")
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read test file %s: %v", testFile, err)
	}

	// Unmarshal into HardwareConfig
	var hwConfig HardwareConfig
	if err := yaml.Unmarshal(data, &hwConfig); err != nil {
		t.Fatalf("Failed to unmarshal YAML: %v", err)
	}

	// Validate the ClockChain
	if hwConfig.Spec.Profile.ClockChain == nil {
		t.Fatal("ClockChain is nil")
	}

	// Run validation
	if err := hwConfig.Spec.Profile.ClockChain.Validate(); err != nil {
		t.Errorf("Validation failed: %v", err)
	}

	// Additional checks
	t.Run("Structure validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if len(cc.Structure) == 0 {
			t.Error("Structure should contain at least one subsystem")
		}

		// Check subsystem names are defined
		for i, subsystem := range cc.Structure {
			if subsystem.Name == "" {
				t.Errorf("Subsystem[%d] name is empty", i)
			}
			if subsystem.DPLL.NetworkInterface == "" {
				t.Errorf("Subsystem[%d] networkInterface is empty", i)
			}
		}
	})

	t.Run("Sources validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.Behavior == nil {
			t.Skip("No behavior section defined")
		}

		// Check that all sources reference valid subsystems
		subsystemNames := make(map[string]bool)
		for _, subsystem := range cc.Structure {
			subsystemNames[subsystem.Name] = true
		}

		for _, source := range cc.Behavior.Sources {
			if source.Subsystem == "" {
				t.Errorf("Source %s has empty subsystem", source.Name)
			}
			if !subsystemNames[source.Subsystem] {
				t.Errorf("Source %s references non-existent subsystem: %s", source.Name, source.Subsystem)
			}
		}
	})

	t.Run("DesiredStates validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.Behavior == nil {
			t.Skip("No behavior section defined")
		}

		// Check that all desired states reference valid subsystems
		subsystemNames := make(map[string]bool)
		for _, subsystem := range cc.Structure {
			subsystemNames[subsystem.Name] = true
		}

		for _, condition := range cc.Behavior.Conditions {
			for _, desiredState := range condition.DesiredStates {
				if desiredState.DPLL != nil && desiredState.DPLL.Subsystem != "" {
					if !subsystemNames[desiredState.DPLL.Subsystem] {
						t.Errorf("Condition %s references non-existent subsystem: %s",
							condition.Name, desiredState.DPLL.Subsystem)
					}
				}
			}
		}
	})

	t.Run("ESync references validation", func(t *testing.T) {
		cc := hwConfig.Spec.Profile.ClockChain

		if cc.CommonDefinitions == nil {
			t.Skip("No common definitions section")
		}

		// Collect eSync names
		esyncNames := make(map[string]bool)
		for _, esync := range cc.CommonDefinitions.ESyncDefinitions {
			esyncNames[esync.Name] = true
		}

		// Check that all eSync references are valid
		for _, subsystem := range cc.Structure {
			allPins := make(map[string]PinConfig)
			for label, config := range subsystem.DPLL.PhaseInputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.PhaseOutputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.FrequencyInputs {
				allPins[label] = config
			}
			for label, config := range subsystem.DPLL.FrequencyOutputs {
				allPins[label] = config
			}

			for label, config := range allPins {
				if config.ESyncConfigName != "" && !esyncNames[config.ESyncConfigName] {
					t.Errorf("Pin %s in subsystem %s references non-existent eSync: %s",
						label, subsystem.Name, config.ESyncConfigName)
				}
			}
		}
	})
}

func TestClockChainValidation_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		config  *ClockChain
		wantErr bool
		errMsg  string
	}{
		{
			name: "empty structure",
			config: &ClockChain{
				Structure: []Subsystem{},
			},
			wantErr: true,
			errMsg:  "structure must contain at least one subsystem",
		},
		{
			name: "duplicate subsystem names",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "test", DPLL: DPLL{NetworkInterface: "eth0"}},
					{Name: "test", DPLL: DPLL{NetworkInterface: "eth1"}},
				},
			},
			wantErr: true,
			errMsg:  "duplicate subsystem name",
		},
		{
			name: "source referencing non-existent subsystem",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
				Behavior: &Behavior{
					Sources: []SourceConfig{
						{
							Name:       "source1",
							Subsystem:  "nonexistent",
							SourceType: "gnss",
							BoardLabel: "GNSS",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "references non-existent subsystem",
		},
		{
			name: "desired state referencing non-existent subsystem",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
				Behavior: &Behavior{
					Sources: []SourceConfig{
						{
							Name:       "source1",
							Subsystem:  "subsys1",
							SourceType: "gnss",
							BoardLabel: "GNSS",
						},
					},
					Conditions: []Condition{
						{
							Name: "test condition",
							Triggers: []SourceState{
								{SourceName: "source1", ConditionType: "locked"},
							},
							DesiredStates: []DesiredState{
								{
									DPLL: &DPLLDesiredState{
										Subsystem:  "nonexistent",
										BoardLabel: "test",
									},
								},
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "references non-existent subsystem",
		},
		{
			name: "valid minimal config",
			config: &ClockChain{
				Structure: []Subsystem{
					{Name: "subsys1", DPLL: DPLL{NetworkInterface: "eth0"}},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error containing %q, got nil", tt.errMsg)
				} else if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
