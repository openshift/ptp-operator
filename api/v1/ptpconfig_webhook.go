/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type PtpRole int

const (
	Master PtpRole = 1
	Slave  PtpRole = 0
)
const PTP_SEC_FOLDER = "/etc/ptp-secret-mount/"

// log is for logging in this package.
var ptpconfiglog = logf.Log.WithName("ptpconfig-resource")
var profileRegEx = regexp.MustCompile(`^([\w\-_]+)(,\s*([\w\-_]+))*$`)
var clockTypes = []string{"T-GM", "T-BC"}

// webhookClient is used by the webhook to query existing PtpConfigs
var webhookClient client.Client

func (r *PtpConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	// Store the client for use in validation
	webhookClient = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-ptp-openshift-io-v1-ptpconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=ptp.openshift.io,resources=ptpconfigs,verbs=create;update,versions=v1,name=vptpconfig.kb.io,admissionReviewVersions=v1

type Ptp4lConfSection struct {
	options map[string]string
}

type Ptp4lConf struct {
	sections map[string]Ptp4lConfSection
}

// GetOption retrieves an option value from a specific section
func (p *Ptp4lConf) GetOption(section, key string) string {
	if sec, ok := p.sections[section]; ok {
		if val, ok := sec.options[key]; ok {
			return val
		}
	}
	return ""
}

func (output *Ptp4lConf) PopulatePtp4lConf(config *string, ptp4lopts *string) error {
	var string_config string
	if config != nil {
		string_config = *config
	}
	lines := strings.Split(string_config, "\n")
	var currentSection string
	output.sections = make(map[string]Ptp4lConfSection)

	for _, line := range lines {
		if strings.HasPrefix(line, "[") {
			currentSection = line
			currentLine := strings.Split(line, "]")

			if len(currentLine) < 2 {
				return errors.New("Section missing closing ']'")
			}

			currentSection = fmt.Sprintf("%s]", currentLine[0])
			section := Ptp4lConfSection{options: map[string]string{}}
			output.sections[currentSection] = section
		} else if currentSection != "" {
			split := strings.IndexByte(line, ' ')
			if split > 0 {
				section := output.sections[currentSection]
				section.options[line[:split]] = strings.TrimSpace(line[split+1:])
				output.sections[currentSection] = section
			}
		} else {
			return errors.New("config option not in section")
		}
	}
	_, exist := output.sections["[global]"]
	if !exist {
		output.sections["[global]"] = Ptp4lConfSection{options: map[string]string{}}
	}

	return nil
}

func (r *PtpConfig) validate() error {
	profiles := r.Spec.Profile

	for _, profile := range profiles {
		conf := &Ptp4lConf{}
		conf.PopulatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts)

		// Validate that interface field only set in ordinary clock
		if profile.Interface != nil && *profile.Interface != "" {
			for section := range conf.sections {
				if section != "[global]" {
					if section != ("[" + *profile.Interface + "]") {
						return errors.New("interface section " + section + " not allowed when specifying interface section")
					}
				}
			}
		}

		// Validate spp settings per section when auth is configured
		// - [global]: spp must be -1 (for UDS communication)
		// - interfaces: spp must not be -1 (auth enabled on network)
		if err := validateSppPerSection(conf); err != nil {
			return fmt.Errorf("failed to validate spp settings per section: %w", err)
		}

		if profile.PtpSchedulingPolicy != nil && *profile.PtpSchedulingPolicy == "SCHED_FIFO" {
			if profile.PtpSchedulingPriority == nil {
				return errors.New("PtpSchedulingPriority must be set for SCHED_FIFO PtpSchedulingPolicy")
			}
		}

		if profile.PtpSettings != nil {
			for k, v := range profile.PtpSettings {
				switch {
				case k == "stdoutFilter":
					_, err := regexp.Compile(v)
					if err != nil {
						return errors.New("stdoutFilter='" + v + "' is invalid; " + err.Error())
					}
				case k == "logReduce":
					logReduceMode := "false"
					logReduceSettings := strings.Fields(v)
					if len(logReduceSettings) >= 1 {
						logReduceMode = strings.ToLower(logReduceSettings[0])
					}
					if logReduceMode != "true" && logReduceMode != "false" && logReduceMode != "basic" && logReduceMode != "enhanced" {
						return errors.New("logReduce mode '" + logReduceMode + "' is invalid; mode must be in 'true', 'false, 'basic', or 'enhanced'")
					}
					if logReduceMode == "enhanced" {
						if len(logReduceSettings) >= 2 {
							if _, err := time.ParseDuration(logReduceSettings[1]); err != nil {
								return errors.New("logReduce time " + logReduceSettings[1] + "' is invalid; must be a valid time duration (e.g. '30s')")
							}
						}
						if len(logReduceSettings) >= 3 {
							if threshold, err := strconv.Atoi(logReduceSettings[2]); err != nil || threshold < 0 {
								return errors.New("logReduce threshold " + logReduceSettings[2] + "' is invalid; must be a non-negative integer")
							}
						}
					}
				case k == "haProfiles":
					if !profileRegEx.MatchString(v) {
						return errors.New("haProfiles='" + v + "' is invalid; must be comma seperated profile names")
					}
				case k == "clockType":
					if !slices.Contains(clockTypes, v) {
						return errors.New("clockType='" + v + "' is invalid; must be one of ['" + strings.Join(clockTypes, "', '") + "']")
					}
				case k == "inSyncConditionTimes":
					// Validate inSyncConditionTimes is an unsigned integer
					if _, err := strconv.ParseUint(v, 10, 32); err != nil {
						return errors.New("inSyncConditionTimes='" + v + "' is invalid; must be an unsigned integer")
					}
				case k == "inSyncConditionThreshold":
					// Validate inSyncConditionThreshold is an unsigned integer
					if _, err := strconv.ParseUint(v, 10, 32); err != nil {
						return errors.New("inSyncConditionThreshold='" + v + "' is invalid; must be an unsigned integer")
					}

				case strings.Contains(k, "clockId"):
					// Allow explicit clockId
					if _, err := strconv.ParseUint(v, 10, 64); err != nil {
						if _, err := strconv.ParseUint(v, 16, 64); err != nil {
							return errors.New("clockId='" + v + "' is invalid; must be an unsigned integer")
						}
					}
				case k == "controllingProfile":
					// Allow controllingProfile setting - no specific validation required for string
				case k == "upstreamPort":
					// Temporary allow upstreamPort setting - no specific validation required for string
				case k == "leadingInterface":
					// Temporary allow leadingInterface setting - no specific validation required for string
				default:
					return errors.New("profile.PtpSettings '" + k + "' is not a configurable setting")
				}
			}
		}

		// validate secret-related settings for this profile
		saFilePath, err := getSaFileFromPtp4lConf(conf)
		if err != nil {
			return fmt.Errorf("failed to get sa file path from ptp4lConf: %w", err)
		}

		// Skip security validation if sa_file is not configured (auth disabled)
		if saFilePath == "" {
			continue
		}

		if err := validateSaFile(saFilePath); err != nil {
			return fmt.Errorf("failed to validate sa file: %w", err)
		}

		// Validate spp values per-interface (not global) exist in the secret
		// [global] has spp -1 which disables authentication globally in UDS sockets and other interfaces.
		secretName := GetSecretNameFromSaFilePath(saFilePath)
		secretKey := GetSecretKeyFromSaFilePath(saFilePath)
		if err := validateInterfaceSppInSecret(conf, secretName, secretKey); err != nil {
			return fmt.Errorf("failed to validate interface spp in secret: %w", err)
		}

	}
	return nil
}

// checking if the secret exists in the openshift-ptp namespace
func getSecret(secretName string) *corev1.Secret {
	if webhookClient == nil {
		ptpconfiglog.Info("webhook client not initialized, skipping secret existence validation")
		return nil
	}
	secret := &corev1.Secret{}
	err := webhookClient.Get(context.Background(), types.NamespacedName{
		Namespace: "openshift-ptp",
		Name:      secretName,
	}, secret)
	if err != nil {
		return nil
	}
	return secret
}

// GetSecretNameFromSaFilePath extracts the secret name from the sa_file path
func GetSecretNameFromSaFilePath(sa_file string) string {
	path := strings.TrimPrefix(sa_file, PTP_SEC_FOLDER)
	index := strings.Index(path, "/")
	return path[:index]
}

// GetSecretKeyFromSaFilePath extracts the secret key from the sa_file path
func GetSecretKeyFromSaFilePath(sa_file string) string {
	path := strings.TrimPrefix(sa_file, PTP_SEC_FOLDER)
	index := strings.Index(path, "/")
	return path[index+1:]
}

// validateSppPerSection validates that:
// - In [global] section: spp must be -1 (auth disabled by default for UDS sockets)
// - In interface sections: spp must NOT be -1 (auth should be enabled on network interfaces)
// This ensures phc2sys/pmc can communicate via UDS without authentication.
func validateSppPerSection(conf *Ptp4lConf) error {
	for sectionName, section := range conf.sections {
		sppValue, hasSpp := section.options["spp"]
		if !hasSpp {
			continue
		}

		if sectionName == "[global]" {
			// Global section: spp must be -1 to disable auth on UDS sockets
			if sppValue != "-1" {
				return fmt.Errorf("spp in [global] section must be -1 to disable authentication globally, Set spp -1 in [global] and configure spp per-interface")
			}
		} else {
			// Interface sections: spp must be between 0 and 255 (valid SPP range for network interfaces)
			if sppInt, err := strconv.Atoi(sppValue); err != nil || sppInt < 0 || sppInt > 255 {
				return fmt.Errorf("spp in interface section %s must be between 0 and 255, got %s", sectionName, sppValue)
			}
		}
	}
	return nil
}

func getSaFileFromPtp4lConf(conf *Ptp4lConf) (string, error) {
	globalSection, exists := conf.sections["[global]"]
	if !exists {
		return "", nil
	}
	saFileValue, exists := globalSection.options["sa_file"]
	if !exists {
		return "", nil
	}
	if saFileValue == "" {
		return "", errors.New("sa_file value is not set in the ptp4lConf")
	}
	return saFileValue, nil
}

// validateSaFile checks that the sa_file path is valid with prefix PTP_SEC_FOLDER
// next directory must be a valid secret name, e.g. PTP_SEC_FOLDER/secret_name/secret_key
// check that secret_key exists in the secret_name secret
func validateSaFile(saFilePath string) error {
	if !strings.HasPrefix(saFilePath, PTP_SEC_FOLDER) {
		return fmt.Errorf("sa_file path '%s' is invalid; must start with '%s'", saFilePath, PTP_SEC_FOLDER)
	}
	path := strings.TrimPrefix(saFilePath, PTP_SEC_FOLDER)
	index := strings.Index(path, "/")
	if index == -1 || index == len(path)-1 {
		return fmt.Errorf("sa_file path '%s' is incomplete; must contain a secret key", saFilePath)
	}
	secretName := path[:index]
	if secretName == "" {
		return fmt.Errorf("sa_file path '%s' is invalid; must contain a secret name", saFilePath)
	}
	secret := getSecret(secretName)
	if secret == nil {
		return fmt.Errorf("sa_file path '%s' has invalid secret name '%s'", saFilePath, secretName)
	}
	keyCandidate := path[index+1:]
	return validateKeyInSecret(secret, keyCandidate)
}

// this function will recieve a secret and a key candidate and check if the key is part of the secret
func validateKeyInSecret(secret *corev1.Secret, keyCandidate string) error {
	if _, exists := secret.Data[keyCandidate]; !exists {
		return fmt.Errorf("key '%s' is not part of the secret", keyCandidate)
	}
	return nil
}

// validateSppInSecret checks that the spp value exists in a specific key of the secret
func validateSppInSecretKey(sppValue string, secretName string, secretKey string) error {
	secret := getSecret(secretName)
	value, exists := secret.Data[secretKey]
	if !exists {
		return fmt.Errorf("key '%s' not found in secret '%s'", secretKey, secret.Name)
	}

	content := string(value)
	lines := strings.Split(content, "\n")

	// Look for lines starting with "spp <number>"
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if line starts with "spp "
		if strings.HasPrefix(strings.ToLower(line), "spp ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				secretSppValue := parts[1]

				// Check if this matches the PtpConfig's spp
				if secretSppValue == sppValue {
					ptpconfiglog.Info("validated spp match", "spp", sppValue, "secret", secret.Name, "key", secretKey)
					return nil // Validation passed ✅
				}
			}
		}
	}

	return fmt.Errorf("spp '%s' not found in key '%s' of secret '%s'", sppValue, secretKey, secret.Name)
}

// validateInterfaceSppInSecret validates that spp values in interface sections (not global) exist in the secret
// [global] section has spp -1 which doesn't need validation against the secret
func validateInterfaceSppInSecret(conf *Ptp4lConf, secretName string, secretKey string) error {
	for sectionName, section := range conf.sections {
		// Skip global section - it has spp -1 which is not in the secret
		if sectionName == "[global]" {
			continue
		}

		// Check if this interface section has spp
		sppValue, hasSpp := section.options["spp"]
		if !hasSpp || sppValue == "" {
			return fmt.Errorf("interface section %s must have spp configured when auth is enabled)", sectionName)
		}

		// Validate that this spp exists in the secret
		if err := validateSppInSecretKey(sppValue, secretName, secretKey); err != nil {
			return fmt.Errorf("interface %s: %w", sectionName, err)
		}
	}
	return nil
}

var _ webhook.Validator = &PtpConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateCreate() (admission.Warnings, error) {
	ptpconfiglog.Info("validate create", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}

	return admission.Warnings{}, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	ptpconfiglog.Info("validate update", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}

	return admission.Warnings{}, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *PtpConfig) ValidateDelete() (admission.Warnings, error) {
	ptpconfiglog.Info("validate delete", "name", r.Name)
	return admission.Warnings{}, nil
}

func getInterfaces(input *Ptp4lConf, mode PtpRole) (interfaces []string) {

	for index, section := range input.sections {
		sectionName := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(index, "[", ""), "]", ""))
		if strings.TrimSpace(section.options["masterOnly"]) == strconv.Itoa(int(mode)) {
			interfaces = append(interfaces, strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(sectionName, "[", ""), "]", "")))
		}
	}
	return interfaces
}

func GetInterfaces(config PtpConfig, mode PtpRole) (interfaces []string) {

	if len(config.Spec.Profile) > 1 {
		logrus.Warnf("More than one profile detected for ptpconfig %s", config.ObjectMeta.Name)
	}
	if len(config.Spec.Profile) == 0 {
		logrus.Warnf("No profile detected for ptpconfig %s", config.ObjectMeta.Name)
		return interfaces
	}
	conf := &Ptp4lConf{}
	var dummy *string
	err := conf.PopulatePtp4lConf(config.Spec.Profile[0].Ptp4lConf, dummy)
	if err != nil {
		logrus.Warnf("ptp4l conf parsing failed, err=%s", err)
	}

	interfaces = getInterfaces(conf, mode)
	var finalInterfaces []string
	for _, aIf := range interfaces {
		if aIf == "global" {
			if config.Spec.Profile[0].Interface != nil {
				finalInterfaces = append(finalInterfaces, *config.Spec.Profile[0].Interface)
			}
		} else {
			finalInterfaces = append(finalInterfaces, aIf)
		}
	}
	if len(interfaces) == 0 && mode == Slave && config.Spec.Profile[0].Interface != nil {
		finalInterfaces = append(finalInterfaces, *config.Spec.Profile[0].Interface)
	}
	return finalInterfaces
}
