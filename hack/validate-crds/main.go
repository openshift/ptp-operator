// Command validate-crds runs the Kubernetes API server's static CRD validation
// locally, including CEL rule parsing and estimated-cost checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-crds <CRD directory>")
		os.Exit(2)
	}
	if err := validateDirectory(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validateDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read CRD directory %q: %w", directory, err)
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "_.yaml" {
			// controller-gen leaves this empty placeholder in bases; it is not
			// included in the CRD kustomization or installed on clusters.
			continue
		}
		extension := filepath.Ext(entry.Name())
		if extension == ".yaml" || extension == ".yml" {
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("no CRD YAML files found in %q", directory)
	}

	var validationErrors []string
	for _, path := range paths {
		if err := validateFile(path); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}
	if len(validationErrors) > 0 {
		return errors.New(strings.Join(validationErrors, "\n"))
	}
	fmt.Printf("Validated %d CRD(s) with Kubernetes CRD/CEL validation\n", len(paths))
	return nil
}

func validateFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	jsonData, err := utilyaml.ToJSON(data)
	if err != nil {
		return fmt.Errorf("parse %s as YAML: %w", path, err)
	}

	var versioned apiextensionsv1.CustomResourceDefinition
	if err := json.Unmarshal(jsonData, &versioned); err != nil {
		return fmt.Errorf("decode %s as a CRD: %w", path, err)
	}
	var internal apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &internal, nil); err != nil {
		return fmt.Errorf("convert %s to the internal CRD type: %w", path, err)
	}

	// The API server initializes these status values as part of CRD creation;
	// populate them so the public static validator sees a valid create object.
	internal.Status.AcceptedNames = internal.Spec.Names
	for _, version := range internal.Spec.Versions {
		if version.Storage {
			internal.Status.StoredVersions = append(internal.Status.StoredVersions, version.Name)
		}
	}

	if errs := crdvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) > 0 {
		messages := make([]string, 0, len(errs))
		for _, validationErr := range errs {
			messages = append(messages, validationErr.Error())
		}
		return fmt.Errorf("%s:\n  %s", path, strings.Join(messages, "\n  "))
	}
	fmt.Printf("Validated %s\n", internal.Name)
	return nil
}
