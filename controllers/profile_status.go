package controllers

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptpv2alpha1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v2alpha1"
)

func localProfileName(ref, crName string) string {
	if crName == "" || ref == "" {
		return ref
	}
	prefix := crName + ProfileNameSeperator
	if strings.HasPrefix(ref, prefix) {
		return strings.TrimPrefix(ref, prefix)
	}
	return ref
}

// relatedProfileMatches reports whether a listed profile name refers to profileName
// in crName. listed and related may be bare ("tbc-tr") or qualified ("tbc-config_tbc-tr").
func relatedProfileMatches(listed, related, crName string) bool {
	if listed == "" || related == "" {
		return false
	}
	if listed == related {
		return true
	}
	if crName == "" {
		return false
	}
	return listed == qualifyProfileName(crName, related) || related == qualifyProfileName(crName, listed)
}

func configsOrSelf(ptpConfig *ptpv1.PtpConfig, all *ptpv1.PtpConfigList) *ptpv1.PtpConfigList {
	if all != nil {
		return all
	}
	return &ptpv1.PtpConfigList{Items: []ptpv1.PtpConfig{*ptpConfig}}
}

// qualifiedProfileRef returns <ptpconfigName>_<profileName> for a spec reference,
// resolving cross-CR names the same way recommend.go does.
func qualifiedProfileRef(ref string, cfg *ptpv1.PtpConfig, all *ptpv1.PtpConfigList) string {
	if ref == "" {
		return ref
	}
	all = configsOrSelf(cfg, all)
	if parts := strings.SplitN(ref, "_", 2); len(parts) == 2 {
		if profileExistsInCR(parts[0], parts[1], all) {
			return ref
		}
	}
	if owner := owningConfigName(ref, all); owner != "" {
		return qualifyProfileName(owner, ref)
	}
	return qualifyProfileName(cfg.Name, ref)
}

func profileHasPhc2sys(profile ptpv1.PtpProfile) bool {
	return profile.Phc2sysOpts != nil && *profile.Phc2sysOpts != ""
}

type appliedProfileInfo struct {
	nodes     []string
	clockType string
}

func appliedProfilesFromDevices(devices *ptpv1.NodePtpDeviceList) map[string]appliedProfileInfo {
	applied := make(map[string]appliedProfileInfo)
	if devices == nil {
		return applied
	}
	for _, dev := range devices.Items {
		if dev.Status.Sync == nil {
			continue
		}
		for _, profile := range dev.Status.Sync.Profiles {
			if profile.Name == "" {
				continue
			}
			info := applied[profile.Name]
			info.nodes = append(info.nodes, dev.Name)
			if info.clockType == "" {
				info.clockType = profile.ClockType
			}
			applied[profile.Name] = info
		}
	}
	return applied
}

func appliedForProfile(name, crName string, applied map[string]appliedProfileInfo) (nodes []string, clockType string) {
	for listed, info := range applied {
		if !relatedProfileMatches(listed, name, crName) {
			continue
		}
		nodes = append(nodes, info.nodes...)
		if clockType == "" {
			clockType = info.clockType
		}
	}
	sort.Strings(nodes)
	return slices.Compact(nodes), clockType
}

func hardwareConfigsForProfile(name, crName string, hwList *ptpv2alpha1.HardwareConfigList) []string {
	if hwList == nil {
		return nil
	}
	var names []string
	for i := range hwList.Items {
		hw := &hwList.Items[i]
		if hw.Spec.RelatedPtpProfileName == "" {
			continue
		}
		if relatedProfileMatches(hw.Spec.RelatedPtpProfileName, name, crName) {
			names = append(names, hw.Name)
		}
	}
	sort.Strings(names)
	return names
}

func matchListFor(cfg *ptpv1.PtpConfig, self *ptpv1.PtpConfig, selfMatch []ptpv1.NodeMatchList) []ptpv1.NodeMatchList {
	if self != nil && cfg.Name == self.Name && cfg.Namespace == self.Namespace {
		return selfMatch
	}
	return cfg.Status.MatchList
}

func buildProfileStatuses(ptpConfig *ptpv1.PtpConfig, devices *ptpv1.NodePtpDeviceList, all *ptpv1.PtpConfigList, hwList *ptpv2alpha1.HardwareConfigList) []ptpv1.ProfileStatus {
	var statuses []ptpv1.ProfileStatus
	all = configsOrSelf(ptpConfig, all)
	applied := appliedProfilesFromDevices(devices)

	controlsMap := make(map[string][]string)
	haCoordinator := make(map[string]string)

	for _, profile := range ptpConfig.Spec.Profile {
		if profile.Name == nil {
			continue
		}
		qname := qualifyProfileName(ptpConfig.Name, *profile.Name)
		settings := profile.PtpSettings
		if settings == nil {
			continue
		}
		if cp, ok := settings["controllingProfile"]; ok && cp != "" {
			controller := localProfileName(cp, ptpConfig.Name)
			controlsMap[controller] = append(controlsMap[controller], qname)
		}
		if ha, ok := settings["haProfiles"]; ok && ha != "" {
			for _, member := range strings.Split(ha, ",") {
				member = strings.TrimSpace(member)
				if member == "" {
					continue
				}
				haCoordinator[localProfileName(member, ptpConfig.Name)] = qname
			}
		}
	}

	for _, profile := range ptpConfig.Spec.Profile {
		if profile.Name == nil {
			continue
		}
		name := *profile.Name
		ps := ptpv1.ProfileStatus{
			Name:       qualifyProfileName(ptpConfig.Name, name),
			HasPhc2sys: profileHasPhc2sys(profile),
		}

		if profile.PtpSettings != nil {
			if cp, ok := profile.PtpSettings["controllingProfile"]; ok && cp != "" {
				ps.ControlledBy = qualifiedProfileRef(cp, ptpConfig, all)
			}
			if ha, ok := profile.PtpSettings["haProfiles"]; ok && ha != "" {
				for _, member := range strings.Split(ha, ",") {
					member = strings.TrimSpace(member)
					if member == "" {
						continue
					}
					ps.HaMembers = append(ps.HaMembers, qualifiedProfileRef(member, ptpConfig, all))
				}
			}
		}

		if controlled, ok := controlsMap[name]; ok {
			ps.Controls = controlled
		}

		if coordinator, ok := haCoordinator[name]; ok {
			ps.PartOfHa = coordinator
		}

		nodes, clockType := appliedForProfile(name, ptpConfig.Name, applied)
		ps.AppliedOnNodes = nodes
		ps.ClockType = clockType
		ps.HardwareConfigs = hardwareConfigsForProfile(name, ptpConfig.Name, hwList)

		statuses = append(statuses, ps)
	}

	return statuses
}

func detectWarnings(ptpConfig *ptpv1.PtpConfig, matchList []ptpv1.NodeMatchList, all *ptpv1.PtpConfigList) []string {
	var warnings []string
	all = configsOrSelf(ptpConfig, all)

	nodePhc2sys := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for i := range all.Items {
		cfg := &all.Items[i]
		matches := matchListFor(cfg, ptpConfig, matchList)
		for _, profile := range cfg.Spec.Profile {
			if profile.Name == nil || !profileHasPhc2sys(profile) {
				continue
			}
			qname := qualifyProfileName(cfg.Name, *profile.Name)
			for _, match := range matches {
				if match.NodeName == nil || match.Profile == nil {
					continue
				}
				if !relatedProfileMatches(*match.Profile, *profile.Name, cfg.Name) {
					continue
				}
				node := *match.NodeName
				if seen[node] == nil {
					seen[node] = make(map[string]bool)
				}
				if seen[node][qname] {
					continue
				}
				seen[node][qname] = true
				nodePhc2sys[node] = append(nodePhc2sys[node], qname)
			}
		}
	}
	nodes := make([]string, 0, len(nodePhc2sys))
	for node := range nodePhc2sys {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		profiles := nodePhc2sys[node]
		if len(profiles) < 2 {
			continue
		}
		sort.Strings(profiles)
		involved := false
		prefix := ptpConfig.Name + ProfileNameSeperator
		for _, p := range profiles {
			if strings.HasPrefix(p, prefix) {
				involved = true
				break
			}
		}
		if involved {
			warnings = append(warnings, "Multiple profiles have phc2sys enabled on "+node+": "+strings.Join(profiles, ", "))
		}
	}

	for _, profile := range ptpConfig.Spec.Profile {
		if profile.Name == nil || profile.PtpSettings == nil {
			continue
		}
		cp, ok := profile.PtpSettings["controllingProfile"]
		if !ok || cp == "" {
			continue
		}
		controllerApplied := false
		profileApplied := false
		for _, match := range matchList {
			if match.Profile == nil {
				continue
			}
			if relatedProfileMatches(*match.Profile, localProfileName(cp, ptpConfig.Name), ptpConfig.Name) ||
				relatedProfileMatches(*match.Profile, cp, ptpConfig.Name) {
				controllerApplied = true
			}
			if relatedProfileMatches(*match.Profile, *profile.Name, ptpConfig.Name) {
				profileApplied = true
			}
		}
		if controllerApplied && !profileApplied {
			qname := qualifyProfileName(ptpConfig.Name, *profile.Name)
			qcontroller := qualifiedProfileRef(cp, ptpConfig, all)
			warnings = append(warnings, "Profile "+qname+" is controlled by "+qcontroller+" but not applied on any node")
		}
	}

	sort.Strings(warnings)
	return warnings
}

func profileReferencesValid(ptpConfig *ptpv1.PtpConfig, all *ptpv1.PtpConfigList) (bool, string) {
	all = configsOrSelf(ptpConfig, all)
	for _, profile := range ptpConfig.Spec.Profile {
		if profile.PtpSettings == nil {
			continue
		}
		if cp := profile.PtpSettings["controllingProfile"]; cp != "" {
			if !profileReferenceExists(cp, all) {
				return false, fmt.Sprintf("profile '%s' referenced in controllingProfile not found in any PtpConfig CR", qualifiedProfileRef(cp, ptpConfig, all))
			}
		}
		if ha := profile.PtpSettings["haProfiles"]; ha != "" {
			for _, member := range strings.Split(ha, ",") {
				member = strings.TrimSpace(member)
				if member == "" {
					continue
				}
				if !profileReferenceExists(member, all) {
					return false, fmt.Sprintf("profile '%s' referenced in haProfiles not found in any PtpConfig CR", qualifiedProfileRef(member, ptpConfig, all))
				}
			}
		}
	}
	return true, "All profile references are valid"
}
