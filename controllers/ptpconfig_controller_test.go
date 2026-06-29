package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
)

func TestNodeMatches_ByName(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	name := "worker-1"
	rules := []ptpv1.MatchRule{{NodeName: &name}}
	assert.True(t, nodeMatches(node, rules))
}

func TestNodeMatches_ByLabel(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "worker-2",
		Labels: map[string]string{"ptp/grandmaster": ""},
	}}
	label := "ptp/grandmaster"
	rules := []ptpv1.MatchRule{{NodeLabel: &label}}
	assert.True(t, nodeMatches(node, rules))
}

func TestNodeMatches_NoMatch(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "worker-3",
		Labels: map[string]string{"role": "compute"},
	}}
	name := "other-node"
	rules := []ptpv1.MatchRule{{NodeName: &name}}
	assert.False(t, nodeMatches(node, rules))
}

func TestNodeMatches_EmptyRules(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	assert.False(t, nodeMatches(node, nil))
	assert.False(t, nodeMatches(node, []ptpv1.MatchRule{}))
}

func TestNodeMatches_NilPointers(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "worker-1",
		Labels: map[string]string{"ptp/test": ""},
	}}
	rules := []ptpv1.MatchRule{{NodeName: nil, NodeLabel: nil}}
	assert.False(t, nodeMatches(node, rules))
}

func TestNodeMatches_NameTakesPriority(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "target-node",
		Labels: map[string]string{"other-label": ""},
	}}
	name := "target-node"
	label := "nonexistent-label"
	rules := []ptpv1.MatchRule{{NodeName: &name, NodeLabel: &label}}
	assert.True(t, nodeMatches(node, rules))
}

func TestReturnMapKeys(t *testing.T) {
	m := map[string]interface{}{
		"alpha": struct{}{},
		"beta":  struct{}{},
		"gamma": struct{}{},
	}
	keys := returnMapKeys(m)
	assert.Len(t, keys, 3)
	assert.ElementsMatch(t, []string{"alpha", "beta", "gamma"}, keys)
}

func TestReturnMapKeys_Empty(t *testing.T) {
	m := map[string]interface{}{}
	keys := returnMapKeys(m)
	assert.Empty(t, keys)
}

func makeDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "linuxptp-daemon", Namespace: "openshift-ptp"},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "linuxptp-daemon-container",
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config-volume", MountPath: "/etc/linuxptp"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config-volume", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "ptp-configmap"},
							},
						}},
					},
				},
			},
		},
	}
}

func TestInjectPtpSecurityVolume(t *testing.T) {
	ds := makeDaemonSet()
	injectPtpSecurityVolume(ds, "my-secret")

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 2)
	vol := ds.Spec.Template.Spec.Volumes[1]
	assert.Equal(t, "my-secret-tlv-auth", vol.Name)
	assert.Equal(t, "my-secret", vol.VolumeSource.Secret.SecretName)

	mounts := ds.Spec.Template.Spec.Containers[0].VolumeMounts
	assert.Len(t, mounts, 2)
	assert.Equal(t, "my-secret-tlv-auth", mounts[1].Name)
	assert.Equal(t, "/etc/ptp-secret-mount/my-secret", mounts[1].MountPath)
	assert.True(t, mounts[1].ReadOnly)
}

func TestInjectPtpSecurityVolume_NoMatchingContainer(t *testing.T) {
	ds := makeDaemonSet()
	ds.Spec.Template.Spec.Containers[0].Name = "other-container"
	injectPtpSecurityVolume(ds, "my-secret")

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 2, "volume should still be added")
	assert.Len(t, ds.Spec.Template.Spec.Containers[0].VolumeMounts, 1,
		"mount should not be added to non-matching container")
}

func TestRemoveSecurityVolumesFromDaemonSet(t *testing.T) {
	ds := makeDaemonSet()
	injectPtpSecurityVolume(ds, "secret-a")
	injectPtpSecurityVolume(ds, "secret-b")

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 3)
	assert.Len(t, ds.Spec.Template.Spec.Containers[0].VolumeMounts, 3)

	removeSecurityVolumesFromDaemonSet(ds)

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "config-volume", ds.Spec.Template.Spec.Volumes[0].Name)

	assert.Len(t, ds.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
	assert.Equal(t, "config-volume", ds.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name)
}

func TestRemoveSecurityVolumesFromDaemonSet_NonePresent(t *testing.T) {
	ds := makeDaemonSet()
	removeSecurityVolumesFromDaemonSet(ds)

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 1)
	assert.Len(t, ds.Spec.Template.Spec.Containers[0].VolumeMounts, 1)
}

func TestRemoveSecurityVolumesFromDaemonSet_NoMatchingContainer(t *testing.T) {
	ds := makeDaemonSet()
	ds.Spec.Template.Spec.Containers[0].Name = "other-container"
	injectPtpSecurityVolume(ds, "secret-a")

	removeSecurityVolumesFromDaemonSet(ds)

	assert.Len(t, ds.Spec.Template.Spec.Volumes, 1)
	assert.Len(t, ds.Spec.Template.Spec.Containers[0].VolumeMounts, 1,
		"non-matching container mounts should remain untouched")
}

func TestEventTransportHostAvailabilityCheck(t *testing.T) {
	r := &PtpOperatorConfigReconciler{}

	host, err := r.EventTransportHostAvailabilityCheck("")
	assert.NoError(t, err)
	assert.Equal(t, DefaultTransportHost(), host, "empty host should return default")

	host, err = r.EventTransportHostAvailabilityCheck("http://my-host:9043")
	assert.NoError(t, err)
	assert.Equal(t, "http://my-host:9043", host, "valid host should be returned as-is")

	host, err = r.EventTransportHostAvailabilityCheck(DefaultTransportHost())
	assert.NoError(t, err)
	assert.Equal(t, DefaultTransportHost(), host)
}

func TestDefaultTransportHostUsesNamespace(t *testing.T) {
	original := names.Namespace
	defer func() { names.Namespace = original }()

	names.Namespace = "openshift-ptp"
	host := DefaultTransportHost()
	assert.Contains(t, host, ".openshift-ptp.svc.cluster.local",
		"default namespace should appear in transport host URL")

	names.Namespace = "custom-ns"
	host = DefaultTransportHost()
	assert.Contains(t, host, ".custom-ns.svc.cluster.local",
		"custom namespace should appear in transport host URL")
	assert.NotContains(t, host, "openshift-ptp",
		"old namespace should not appear when overridden")
}

func TestDefaultTransportHostFormat(t *testing.T) {
	original := names.Namespace
	defer func() { names.Namespace = original }()

	names.Namespace = "test-ns"
	host := DefaultTransportHost()
	assert.Equal(t,
		"http://ptp-event-publisher-service-NODE_NAME.test-ns.svc.cluster.local:9043",
		host)
}

func TestGetRecommendProfilesNamesForConfig_NilRecommend(t *testing.T) {
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{Recommend: nil},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Empty(t, result)
}

func TestGetRecommendProfilesNamesForConfig_SkipsNilProfile(t *testing.T) {
	label := "ptp/test"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: nil, Priority: int64Ptr(5), Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Empty(t, result)
}

func TestGetRecommendProfilesNamesForConfig_SkipsEmptyMatch(t *testing.T) {
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("my-profile"), Priority: int64Ptr(5), Match: []ptpv1.MatchRule{}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Empty(t, result)
}

func TestGetRecommendProfilesNamesForConfig_MatchesByLabel(t *testing.T) {
	label := "ptp/grandmaster"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("gm-profile"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/grandmaster": ""},
	}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Contains(t, result, "gm-profile")
}

func TestGetRecommendProfilesNamesForConfig_SamePriority(t *testing.T) {
	label := "ptp/ha"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("profile-a"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
				{Profile: strPtr("profile-b"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/ha": ""},
	}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "profile-a")
	assert.Contains(t, result, "profile-b")
}

func TestGetRecommendProfilesNamesForConfig_NilPriority(t *testing.T) {
	label := "ptp/test"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("nil-pri"), Priority: nil, Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}

	assert.NotPanics(t, func() {
		result := getRecommendProfilesNamesForConfig(&cfg, node)
		assert.Len(t, result, 1)
		assert.Contains(t, result, "nil-pri")
	})
}

func TestGetRecommendProfilesNamesForConfig_DifferentPriority(t *testing.T) {
	label := "ptp/test"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("high-pri"), Priority: int64Ptr(1),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
				{Profile: strPtr("low-pri"), Priority: int64Ptr(10),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}
	result := getRecommendProfilesNamesForConfig(&cfg, node)
	assert.Len(t, result, 1)
	assert.Contains(t, result, "high-pri")
}

func TestGetRecommendNodePtpProfilesForConfig_ReturnsProfiles(t *testing.T) {
	label := "ptp/test"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Profile: []ptpv1.PtpProfile{
				{Name: strPtr("my-profile")},
				{Name: strPtr("other-profile")},
			},
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("my-profile"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}

	profiles, err := getRecommendNodePtpProfilesForConfig(&cfg, node)
	assert.NoError(t, err)
	assert.Len(t, profiles, 1)
	assert.Equal(t, "my-profile", *profiles[0].Name)
}

func TestGetRecommendNodePtpProfilesForConfig_NoMatch(t *testing.T) {
	label := "ptp/other"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Profile: []ptpv1.PtpProfile{{Name: strPtr("my-profile")}},
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("my-profile"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}

	profiles, err := getRecommendNodePtpProfilesForConfig(&cfg, node)
	assert.NoError(t, err)
	assert.Empty(t, profiles)
}

func TestGetRecommendNodePtpProfilesForConfig_NilProfileSpec(t *testing.T) {
	label := "ptp/test"
	cfg := ptpv1.PtpConfig{
		Spec: ptpv1.PtpConfigSpec{
			Profile: nil,
			Recommend: []ptpv1.PtpRecommend{
				{Profile: strPtr("my-profile"), Priority: int64Ptr(5),
					Match: []ptpv1.MatchRule{{NodeLabel: &label}}},
			},
		},
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker-1", Labels: map[string]string{"ptp/test": ""},
	}}

	profiles, err := getRecommendNodePtpProfilesForConfig(&cfg, node)
	assert.NoError(t, err)
	assert.Empty(t, profiles)
}
