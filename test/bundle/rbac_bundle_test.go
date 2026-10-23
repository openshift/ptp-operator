package bundle

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

type rule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

func TestCurrentBundleOmitsNetworkPolicies(t *testing.T) {
	for _, dir := range []string{"bundle/manifests", "manifests/stable"} {
		t.Run(dir, func(t *testing.T) {
			paths, err := filepath.Glob(filepath.Join("..", "..", dir, "*.yaml"))
			if err != nil || len(paths) == 0 {
				t.Fatalf("read bundle manifests: %v", err)
			}
			for _, path := range paths {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				decoder := yaml.NewDecoder(f)
				for {
					var obj struct {
						Kind string `yaml:"kind"`
					}
					if err := decoder.Decode(&obj); err != nil {
						if err == io.EOF {
							break
						}
						_ = f.Close()
						t.Fatalf("decode %s: %v", path, err)
					}
					if obj.Kind == "NetworkPolicy" {
						_ = f.Close()
						t.Fatalf("unsupported NetworkPolicy bundle object in %s", path)
					}
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type csv struct {
	Spec struct {
		Install struct {
			Spec struct {
				ClusterPermissions []struct {
					ServiceAccountName string `yaml:"serviceAccountName"`
					Rules              []rule `yaml:"rules"`
				} `yaml:"clusterPermissions"`
				Deployments []struct {
					Spec struct {
						Template struct {
							Spec struct {
								Containers []struct {
									Ports []struct {
										ContainerPort int `yaml:"containerPort"`
									} `yaml:"ports"`
								} `yaml:"containers"`
							} `yaml:"spec"`
						} `yaml:"template"`
					} `yaml:"spec"`
				} `yaml:"deployments"`
			} `yaml:"spec"`
		} `yaml:"install"`
		WebhookDefinitions []struct {
			TargetPort int `yaml:"targetPort"`
		} `yaml:"webhookdefinitions"`
	} `yaml:"spec"`
}

func TestOperatorBundlePermissionsAndWebhook(t *testing.T) {
	var previous []rule
	for _, dir := range []string{"bundle/manifests", "manifests/stable"} {
		t.Run(dir, func(t *testing.T) {
			path := filepath.Join("../..", dir)
			contents, err := os.ReadFile(filepath.Join(path, "ptp-operator.clusterserviceversion.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest csv
			if err := yaml.Unmarshal(contents, &manifest); err != nil {
				t.Fatal(err)
			}
			var operatorRules []rule
			for _, permission := range manifest.Spec.Install.Spec.ClusterPermissions {
				if permission.ServiceAccountName == "ptp-operator" {
					operatorRules = permission.Rules
				}
			}
			if len(operatorRules) == 0 {
				t.Fatal("missing ptp-operator clusterPermissions")
			}
			if previous != nil && !reflect.DeepEqual(previous, operatorRules) {
				t.Fatal("bundle and stable operator RBAC differ")
			}
			previous = operatorRules
			required := map[string]bool{"nodes": false, "daemonsets": false, "networkpolicies": false, "ptpoperatorconfigs": false, "nodeptpdevices": false}
			for _, r := range operatorRules {
				values := append([]string{}, r.APIGroups...)
				values = append(values, r.Resources...)
				values = append(values, r.Verbs...)
				for _, value := range values {
					if value == "*" {
						t.Fatalf("wildcard operator grant: %+v", r)
					}
				}
				for _, resource := range r.Resources {
					if _, ok := required[resource]; ok {
						required[resource] = true
					}
					if resource == "namespaces" || resource == "deployments" || resource == "serviceaccounts" {
						t.Fatalf("unexpected operator resource: %s", resource)
					}
					if resource == "pods" || resource == "endpoints" {
						if !reflect.DeepEqual(r.Verbs, []string{"get", "list", "watch"}) {
							t.Fatalf("%s should only be readable: %+v", resource, r)
						}
					}
					if resource == "networkpolicies" {
						verbs := append([]string(nil), r.Verbs...)
						sort.Strings(verbs)
						if !reflect.DeepEqual(verbs, []string{"create", "delete", "get", "list", "update", "watch"}) {
							t.Fatalf("network policy reconciliation grants should be get/list/watch/create/update/delete: %+v", r)
						}
					}
				}
			}
			for resource, found := range required {
				if !found {
					t.Errorf("missing operator resource %s", resource)
				}
			}
			if len(manifest.Spec.Install.Spec.Deployments) != 1 || len(manifest.Spec.Install.Spec.Deployments[0].Spec.Template.Spec.Containers) != 1 ||
				len(manifest.Spec.Install.Spec.Deployments[0].Spec.Template.Spec.Containers[0].Ports) != 1 ||
				manifest.Spec.Install.Spec.Deployments[0].Spec.Template.Spec.Containers[0].Ports[0].ContainerPort != 9443 {
				t.Fatal("operator webhook container must listen on 9443")
			}
			if len(manifest.Spec.WebhookDefinitions) == 0 {
				t.Fatal("missing webhook definitions")
			}
			for _, webhook := range manifest.Spec.WebhookDefinitions {
				if webhook.TargetPort != 9443 {
					t.Errorf("webhook target port = %d, want 9443", webhook.TargetPort)
				}
			}
			service, err := os.ReadFile(filepath.Join(path, "webhook-service_v1_service.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var svc struct {
				Spec struct {
					Ports []struct {
						Port       int `yaml:"port"`
						TargetPort int `yaml:"targetPort"`
					} `yaml:"ports"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal(service, &svc); err != nil {
				t.Fatal(err)
			}
			if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 443 || svc.Spec.Ports[0].TargetPort != 9443 {
				t.Fatalf("webhook service port mismatch: %+v", svc.Spec.Ports)
			}
		})
	}
}
