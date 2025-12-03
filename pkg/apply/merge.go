package apply

import (
	"context"
	"log"

	"github.com/pkg/errors"

	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const TLV_AUTH_SUFFIX = "-tlv-auth"

// MergeMetadataForUpdate merges the read-only fields of metadata.
// This is to be able to do a a meaningful comparison in apply,
// since objects created on runtime do not have these fields populated.
func MergeMetadataForUpdate(current, updated *uns.Unstructured) error {
	updated.SetCreationTimestamp(current.GetCreationTimestamp())
	updated.SetSelfLink(current.GetSelfLink())
	updated.SetGeneration(current.GetGeneration())
	updated.SetUID(current.GetUID())
	updated.SetResourceVersion(current.GetResourceVersion())

	mergeAnnotations(current, updated)
	mergeLabels(current, updated)

	return nil
}

// MergeObjectForUpdate prepares a "desired" object to be updated.
// Some objects, such as Deployments and Services require
// some semantic-aware updates
func MergeObjectForUpdate(ctx context.Context, current, updated *uns.Unstructured) error {
	if err := MergeDeploymentForUpdate(current, updated); err != nil {
		return err
	}

	if err := MergeDaemonSetForUpdate(ctx, current, updated); err != nil {
		return err
	}

	if err := MergeServiceForUpdate(current, updated); err != nil {
		return err
	}

	if err := MergeServiceAccountForUpdate(current, updated); err != nil {
		return err
	}

	// For all object types, merge metadata.
	// Run this last, in case any of the more specific merge logic has
	// changed "updated"
	MergeMetadataForUpdate(current, updated)

	return nil
}

const (
	deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
)

// MergeDeploymentForUpdate updates Deployment objects.
// We merge annotations, keeping ours except the Deployment Revision annotation.
func MergeDeploymentForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "apps" && gvk.Kind == "Deployment" {

		// Copy over the revision annotation from current up to updated
		// otherwise, updated would win, and this annotation is "special" and
		// needs to be preserved
		curAnnotations := current.GetAnnotations()
		updatedAnnotations := updated.GetAnnotations()
		if updatedAnnotations == nil {
			updatedAnnotations = map[string]string{}
		}

		anno, ok := curAnnotations[deploymentRevisionAnnotation]
		if ok {
			updatedAnnotations[deploymentRevisionAnnotation] = anno
		}

		updated.SetAnnotations(updatedAnnotations)
	}

	return nil
}

// MergeDaemonSetForUpdate merges DaemonSet templates using context to determine source:
// - PtpConfig controller: use all volumes/annotations/mounts from updated (even if empty)
// - PtpOperatorConfig controller: use base from updated + preserve security from current
// This supports adding/deleting sa_file or secrets while preserving non-security changes.
func MergeDaemonSetForUpdate(ctx context.Context, current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "apps" && gvk.Kind == "DaemonSet" {
		// Only apply to linuxptp-daemon DaemonSet
		if updated.GetName() != "linuxptp-daemon" {
			return nil
		}

		// Get controller source from context
		source, _ := ctx.Value(ControllerSourceKey).(string)
		log.Printf("MergeDaemonSet: Controller source from context: %s", source)

		// Determine if we should preserve security items from current
		preserveSecurityFromCurrent := (source == SourcePtpOperatorConfig)

		if err := mergeSecurityVolumes(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}

		if err := mergeSecurityAnnotations(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}

		if err := mergeSecurityVolumeMounts(current, updated, preserveSecurityFromCurrent); err != nil {
			return err
		}
	}

	return nil
}

// ============================================================================
// Helper Functions for Security Item Detection
// ============================================================================

// isSecurityItem checks if a volume/mount name is a security item
// Security items end with TLV_AUTH_SUFFIX suffix
func isSecurityItem(name string) bool {
	// Check for regular security volumes (ends with TLV_AUTH_SUFFIX)
	if len(name) >= 9 && name[len(name)-9:] == TLV_AUTH_SUFFIX {
		return true
	}
	return false
}

// filterNonSecurityVolumes extracts non-security volumes from a volume list
func filterNonSecurityVolumes(volumes []interface{}) []interface{} {
	var nonSecurityVolumes []interface{}
	for _, vol := range volumes {
		volMap, ok := vol.(map[string]interface{})
		if !ok {
			nonSecurityVolumes = append(nonSecurityVolumes, vol)
			continue
		}
		name, ok := volMap["name"].(string)
		if !ok {
			nonSecurityVolumes = append(nonSecurityVolumes, vol)
			continue
		}
		if !isSecurityItem(name) {
			nonSecurityVolumes = append(nonSecurityVolumes, vol)
		}
	}
	return nonSecurityVolumes
}

// extractSecurityVolumes extracts security volumes from a volume list
func extractSecurityVolumes(volumes []interface{}, logSource string) []interface{} {
	var securityVolumes []interface{}
	for _, vol := range volumes {
		volMap, ok := vol.(map[string]interface{})
		if !ok {
			continue
		}
		name, ok := volMap["name"].(string)
		if !ok {
			continue
		}
		if isSecurityItem(name) {
			log.Printf("MergeDaemonSet: %s security volume: %s", logSource, name)
			securityVolumes = append(securityVolumes, vol)
		}
	}
	return securityVolumes
}

// ============================================================================
// Logical Block Functions for mergeSecurityVolumes
// ============================================================================

// Logic 1: Get volumes from current DaemonSet
func getVolumesFromCurrent(current *uns.Unstructured) []interface{} {
	volumes, found, err := uns.NestedSlice(current.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		return []interface{}{}
	}
	return volumes
}

// Logic 2: Get volumes from updated DaemonSet
func getVolumesFromUpdated(updated *uns.Unstructured) ([]interface{}, error) {
	volumes, found, err := uns.NestedSlice(updated.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return nil, err
	}
	if !found {
		return []interface{}{}, nil
	}
	return volumes, nil
}

// Logic 3: If PtpOperatorConfig -> preserve security volumes from current
func preserveSecurityFromCurrentVolumes(currentVolumes []interface{}) []interface{} {
	return extractSecurityVolumes(currentVolumes, "Preserving from current")
}

// Logic 4: If PtpConfig -> use security volumes from updated
func useSecurityFromUpdatedVolumes(updatedVolumes []interface{}) []interface{} {
	return extractSecurityVolumes(updatedVolumes, "Using from updated")
}

// ============================================================================
// Helper Functions for Annotation Operations
// ============================================================================

// isSecurityAnnotation checks if an annotation key is a security hash annotation
func isSecurityAnnotation(key string) bool {
	securityPrefix := "ptp.openshift.io/secret-hash-"
	return len(key) > len(securityPrefix) && key[:len(securityPrefix)] == securityPrefix
}

// filterNonSecurityAnnotations extracts non-security annotations from annotation map
func filterNonSecurityAnnotations(annotations map[string]string) map[string]string {
	nonSecurityAnnotations := make(map[string]string)
	for k, v := range annotations {
		if !isSecurityAnnotation(k) {
			nonSecurityAnnotations[k] = v
		}
	}
	return nonSecurityAnnotations
}

// extractSecurityAnnotations extracts security annotations from annotation map
func extractSecurityAnnotations(annotations map[string]string, logSource string) map[string]string {
	securityAnnotations := make(map[string]string)
	for k, v := range annotations {
		if isSecurityAnnotation(k) {
			log.Printf("MergeDaemonSet: %s security annotation: %s", logSource, k)
			securityAnnotations[k] = v
		}
	}
	return securityAnnotations
}

// ============================================================================
// Logical Block Functions for mergeSecurityAnnotations
// ============================================================================

// Logic 1: Get annotations from current DaemonSet
func getAnnotationsFromCurrent(current *uns.Unstructured) map[string]string {
	annotations, found, err := uns.NestedStringMap(current.Object, "spec", "template", "metadata", "annotations")
	if err != nil || !found {
		return make(map[string]string)
	}
	return annotations
}

// Logic 2: Get annotations from updated DaemonSet
func getAnnotationsFromUpdated(updated *uns.Unstructured) (map[string]string, error) {
	annotations, found, err := uns.NestedStringMap(updated.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return nil, err
	}
	if !found {
		return make(map[string]string), nil
	}
	return annotations, nil
}

// Logic 3: If PtpOperatorConfig -> preserve security annotations from current
func preserveSecurityFromCurrentAnnotations(currentAnnotations map[string]string) map[string]string {
	return extractSecurityAnnotations(currentAnnotations, "Preserving from current")
}

// Logic 4: If PtpConfig -> use security annotations from updated
func useSecurityFromUpdatedAnnotations(updatedAnnotations map[string]string) map[string]string {
	return extractSecurityAnnotations(updatedAnnotations, "Using from updated")
}

// ============================================================================
// Helper Functions for Volume Mount Operations
// ============================================================================

// filterNonSecurityMounts extracts non-security mounts from mount list
func filterNonSecurityMounts(mounts []interface{}) []interface{} {
	var nonSecurityMounts []interface{}
	for _, mount := range mounts {
		mountMap, ok := mount.(map[string]interface{})
		if !ok {
			nonSecurityMounts = append(nonSecurityMounts, mount)
			continue
		}
		mountName, ok := mountMap["name"].(string)
		if !ok {
			nonSecurityMounts = append(nonSecurityMounts, mount)
			continue
		}
		if !isSecurityItem(mountName) {
			nonSecurityMounts = append(nonSecurityMounts, mount)
		}
	}
	return nonSecurityMounts
}

// extractSecurityMounts extracts security mounts from mount list
func extractSecurityMounts(mounts []interface{}, logSource string) []interface{} {
	var securityMounts []interface{}
	for _, mount := range mounts {
		mountMap, ok := mount.(map[string]interface{})
		if !ok {
			continue
		}
		mountName, ok := mountMap["name"].(string)
		if !ok {
			continue
		}
		if isSecurityItem(mountName) {
			log.Printf("MergeDaemonSet: %s security mount: %s", logSource, mountName)
			securityMounts = append(securityMounts, mount)
		}
	}
	return securityMounts
}

// getMountsFromContainer retrieves volumeMounts from a specific container
func getMountsFromContainer(containers []interface{}, containerName string) []interface{} {
	for _, cont := range containers {
		contMap, ok := cont.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := contMap["name"].(string); ok && name == containerName {
			mounts, found, err := uns.NestedSlice(contMap, "volumeMounts")
			if err != nil || !found {
				return []interface{}{}
			}
			return mounts
		}
	}
	return []interface{}{}
}

// ============================================================================
// Logical Block Functions for mergeSecurityVolumeMounts
// ============================================================================

// Logic 1: Get mounts from current DaemonSet containers
func getMountsFromCurrent(current *uns.Unstructured) []interface{} {
	currentContainers, found, err := uns.NestedSlice(current.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return []interface{}{}
	}
	return getMountsFromContainer(currentContainers, "linuxptp-daemon-container")
}

// Logic 2: Get mounts from updated DaemonSet containers
func getMountsFromUpdated(updated *uns.Unstructured) ([]interface{}, error) {
	updatedContainers, found, err := uns.NestedSlice(updated.Object, "spec", "template", "spec", "containers")
	if err != nil || !found {
		return nil, err
	}
	return getMountsFromContainer(updatedContainers, "linuxptp-daemon-container"), nil
}

// Logic 3: If PtpOperatorConfig -> preserve security mounts from current
func preserveSecurityFromCurrentMounts(currentMounts []interface{}) []interface{} {
	return extractSecurityMounts(currentMounts, "Preserving from current")
}

// Logic 4: If PtpConfig -> use security mounts from updated
func useSecurityFromUpdatedMounts(updatedMounts []interface{}) []interface{} {
	return extractSecurityMounts(updatedMounts, "Using from updated")
}

// mergeSecurityVolumes implements simplified merge logic for volumes
// Divided into 4 logical blocks for clarity
func mergeSecurityVolumes(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Logic 1: Get volumes from current
	currentVolumes := getVolumesFromCurrent(current)

	// Logic 2: Get volumes from updated
	updatedVolumes, err := getVolumesFromUpdated(updated)
	if err != nil {
		return err
	}

	// Filter non-security volumes from updated (base template)
	updatedNonSecurityVolumes := filterNonSecurityVolumes(updatedVolumes)

	// Select which security volumes to use based on controller source
	var securityVolumesToUse []interface{}
	if preserveSecurityFromCurrent {
		// Logic 3: PtpOperatorConfig -> preserve security from current
		securityVolumesToUse = preserveSecurityFromCurrentVolumes(currentVolumes)
	} else {
		// Logic 4: PtpConfig -> use security from updated
		securityVolumesToUse = useSecurityFromUpdatedVolumes(updatedVolumes)
	}

	// Merge = non-security from updated + security (from current or updated)
	mergedVolumes := append(updatedNonSecurityVolumes, securityVolumesToUse...)
	log.Printf("MergeDaemonSet: Merged volumes: %d non-security + %d security = %d total",
		len(updatedNonSecurityVolumes), len(securityVolumesToUse), len(mergedVolumes))

	return uns.SetNestedSlice(updated.Object, mergedVolumes, "spec", "template", "spec", "volumes")
}

// mergeSecurityAnnotations implements simplified merge logic for annotations
// Divided into 4 logical blocks for clarity
// Security annotations contain secret hashes - changes trigger pod restarts
func mergeSecurityAnnotations(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Logic 1: Get annotations from current
	currentAnnotations := getAnnotationsFromCurrent(current)

	// Logic 2: Get annotations from updated
	updatedAnnotations, err := getAnnotationsFromUpdated(updated)
	if err != nil {
		return err
	}

	// Filter non-security annotations from updated
	updatedNonSecurityAnnotations := filterNonSecurityAnnotations(updatedAnnotations)

	// Select which security annotations to use based on controller source
	var securityAnnotationsToUse map[string]string
	if preserveSecurityFromCurrent {
		// Logic 3: PtpOperatorConfig -> preserve security from current
		securityAnnotationsToUse = preserveSecurityFromCurrentAnnotations(currentAnnotations)
	} else {
		// Logic 4: PtpConfig -> use security from updated
		securityAnnotationsToUse = useSecurityFromUpdatedAnnotations(updatedAnnotations)
	}

	// Merge annotations - combine non-security with selected security hashes
	mergedAnnotations := make(map[string]string)
	for k, v := range updatedNonSecurityAnnotations {
		mergedAnnotations[k] = v
	}
	for k, v := range securityAnnotationsToUse {
		mergedAnnotations[k] = v
	}

	log.Printf("MergeDaemonSet: Merged annotations: %d non-security + %d security = %d total",
		len(updatedNonSecurityAnnotations), len(securityAnnotationsToUse), len(mergedAnnotations))

	// Apply merged annotations (annotation changes trigger pod restarts)
	return uns.SetNestedStringMap(updated.Object, mergedAnnotations, "spec", "template", "metadata", "annotations")
}

// mergeSecurityVolumeMounts implements simplified merge logic for volume mounts
// Divided into 4 logical blocks for clarity
func mergeSecurityVolumeMounts(current, updated *uns.Unstructured, preserveSecurityFromCurrent bool) error {
	// Logic 1: Get mounts from current
	currentMounts := getMountsFromCurrent(current)

	// Logic 2: Get mounts from updated
	updatedMounts, err := getMountsFromUpdated(updated)
	if err != nil {
		return err
	}

	// Filter non-security mounts from updated
	updatedNonSecurityMounts := filterNonSecurityMounts(updatedMounts)

	// Select which security mounts to use based on controller source
	var securityMountsToUse []interface{}
	if preserveSecurityFromCurrent {
		// Logic 3: PtpOperatorConfig -> preserve security from current
		securityMountsToUse = preserveSecurityFromCurrentMounts(currentMounts)
	} else {
		// Logic 4: PtpConfig -> use security from updated
		securityMountsToUse = useSecurityFromUpdatedMounts(updatedMounts)
	}

	// Merge = non-security from updated + security (from current or updated)
	mergedMounts := append(updatedNonSecurityMounts, securityMountsToUse...)
	log.Printf("MergeDaemonSet: Merged mounts: %d non-security + %d security = %d total",
		len(updatedNonSecurityMounts), len(securityMountsToUse), len(mergedMounts))

	// Update the linuxptp-daemon-container with merged mounts
	updatedContainers, _, _ := uns.NestedSlice(updated.Object, "spec", "template", "spec", "containers")
	for i, cont := range updatedContainers {
		contMap, ok := cont.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := contMap["name"].(string); ok && name == "linuxptp-daemon-container" {
			contMap["volumeMounts"] = mergedMounts
			updatedContainers[i] = contMap
			break
		}
	}

	return uns.SetNestedSlice(updated.Object, updatedContainers, "spec", "template", "spec", "containers")
}

// MergeServiceForUpdate ensures the clusterip is never written to
func MergeServiceForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "Service" {
		clusterIP, found, err := uns.NestedString(current.Object, "spec", "clusterIP")
		if err != nil {
			return err
		}

		if found {
			return uns.SetNestedField(updated.Object, clusterIP, "spec", "clusterIP")
		}
	}

	return nil
}

// MergeServiceAccountForUpdate copies secrets from current to updated.
// This is intended to preserve the auto-generated token.
// Right now, we just copy current to updated and don't support supplying
// any secrets ourselves.
func MergeServiceAccountForUpdate(current, updated *uns.Unstructured) error {
	gvk := updated.GroupVersionKind()
	if gvk.Group == "" && gvk.Kind == "ServiceAccount" {
		curSecrets, ok, err := uns.NestedSlice(current.Object, "secrets")
		if err != nil {
			return err
		}

		if ok {
			uns.SetNestedField(updated.Object, curSecrets, "secrets")
		}

		curImagePullSecrets, ok, err := uns.NestedSlice(current.Object, "imagePullSecrets")
		if err != nil {
			return err
		}
		if ok {
			uns.SetNestedField(updated.Object, curImagePullSecrets, "imagePullSecrets")
		}
	}
	return nil
}

// mergeAnnotations copies over any annotations from current to updated,
// with updated winning if there's a conflict
func mergeAnnotations(current, updated *uns.Unstructured) {
	updatedAnnotations := updated.GetAnnotations()
	curAnnotations := current.GetAnnotations()

	if curAnnotations == nil {
		curAnnotations = map[string]string{}
	}

	for k, v := range updatedAnnotations {
		curAnnotations[k] = v
	}

	updated.SetAnnotations(curAnnotations)
}

// mergeLabels copies over any labels from current to updated,
// with updated winning if there's a conflict
func mergeLabels(current, updated *uns.Unstructured) {
	updatedLabels := updated.GetLabels()
	curLabels := current.GetLabels()

	if curLabels == nil {
		curLabels = map[string]string{}
	}

	for k, v := range updatedLabels {
		curLabels[k] = v
	}

	updated.SetLabels(curLabels)
}

// IsObjectSupported rejects objects with configurations we don't support.
// This catches ServiceAccounts with secrets, which is valid but we don't
// support reconciling them.
func IsObjectSupported(obj *uns.Unstructured) error {
	gvk := obj.GroupVersionKind()

	// We cannot create ServiceAccounts with secrets because there's currently
	// no need and the merging logic is complex.
	// If you need this, please file an issue.
	if gvk.Group == "" && gvk.Kind == "ServiceAccount" {
		secrets, ok, err := uns.NestedSlice(obj.Object, "secrets")
		if err != nil {
			return err
		}

		if ok && len(secrets) > 0 {
			return errors.Errorf("cannot create ServiceAccount with secrets")
		}
	}

	return nil
}
