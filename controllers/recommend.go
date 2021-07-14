package controllers

import (
	"fmt"
	"sort"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
)

func printWhenNotNil(p *string, description string) {
	if p != nil {
		glog.Info(description, ": ", *p)
	}
}

// getRecommendNodePtpProfiles return recommended node ptp profile
func getRecommendNodePtpProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("in getRecommendNodePtpProfiles")

	profiles, err := getRecommendProfiles(ptpConfigList, node)
	if err != nil {
		return nil, fmt.Errorf("get recommended ptp profiles failed: %v", err)
	}

	glog.Infof("ptp profiles to be updated for node: %s", node.Name)
	for _, profile := range profiles {
		glog.Infof("------------------------------------")
		printWhenNotNil(profile.Name, "Profile Name")
		printWhenNotNil(profile.Interface, "Interface")
		printWhenNotNil(profile.Ptp4lOpts, "Ptp4lOpts")
		printWhenNotNil(profile.Phc2sysOpts, "Phc2sysOpts")
		printWhenNotNil(profile.Ptp4lConf, "Ptp4lConf")
		glog.Infof("------------------------------------")
	}

	return profiles, nil
}

func getRecommendProfiles(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	glog.V(2).Infof("In getRecommendProfiles")

	profilesNames := getRecommendProfilesNames(ptpConfigList, node)
	glog.V(2).Infof("recommended ptp profiles names are %v for node: %s", returnMapKeys(profilesNames), node.Name)

	profiles := []ptpv1.PtpProfile{}
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Profile != nil {
			for _, profile := range cfg.Spec.Profile {
				if _, exist := profilesNames[*profile.Name]; exist {
					profiles = append(profiles, profile)
				}
			}
		}
	}

	if len(profiles) != len(profilesNames) {
		return nil, fmt.Errorf("failed to find all the profiles")
	}
	return profiles, nil
}

func getRecommendProfilesNames(ptpConfigList *ptpv1.PtpConfigList, node corev1.Node) map[string]interface{} {
	glog.V(2).Infof("In getRecommendProfilesNames")

	var (
		allRecommend []ptpv1.PtpRecommend
	)

	// append recommend section from each custom resource into one list
	for _, cfg := range ptpConfigList.Items {
		if cfg.Spec.Recommend != nil {
			allRecommend = append(allRecommend, cfg.Spec.Recommend...)
		}
	}

	// allRecommend sorted by priority
	// priority 0 will become the first item in allRecommend
	sort.Slice(allRecommend, func(i, j int) bool {
		if allRecommend[i].Priority != nil && allRecommend[j].Priority != nil {
			return *allRecommend[i].Priority < *allRecommend[j].Priority
		}
		return allRecommend[i].Priority != nil
	})

	// Add all the profiles with the same priority
	profilesNames := make(map[string]interface{})
	foundPolicy := false
	priority := int64(-1)

	// loop allRecommend from high priority(0) to low(*)
	for _, r := range allRecommend {

		// ignore if profile not define in recommend
		if r.Profile == nil {
			continue
		}

		// ignore if match section is empty
		if len(r.Match) == 0 {
			continue
		}

		// check if the policy match the node
		switch {
		case !nodeMatches(&node, r.Match):
			continue
		case !foundPolicy:
			profilesNames[*r.Profile] = struct{}{}
			priority = *r.Priority
			foundPolicy = true
		case *r.Priority == priority:
			profilesNames[*r.Profile] = struct{}{}
		default:
			break
		}
	}

	return profilesNames
}

func nodeMatches(node *corev1.Node, matchRuleList []ptpv1.MatchRule) bool {
	// loop over Match list
	for _, m := range matchRuleList {

		// nodeName has higher priority than nodeLabel
		// return immediately if nodeName matches
		// make sure m.NodeName pointer is not nil before
		// comparing values
		if m.NodeName != nil && *m.NodeName == node.Name {
			return true
		}

		// return immediately when label matches
		// this makes sure priority field is respected
		for k, _ := range node.Labels {
			// make sure m.NodeLabel pointer is not nil before
			// comparing values
			if m.NodeLabel != nil && *m.NodeLabel == k {
				return true
			}
		}
	}

	return false
}

func returnMapKeys(profiles map[string]interface{}) []string {
	keys := make([]string, len(profiles))

	i := 0
	for k := range profiles {
		keys[i] = k
		i++
	}

	return keys
}
