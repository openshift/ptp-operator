package controllers

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetCondition(t *testing.T) {
	var conditions []metav1.Condition

	setCondition(&conditions, "ProfileReferenceValid", true, "AllReferencesResolved", "All profile references are valid")
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if conditions[0].Type != "ProfileReferenceValid" {
		t.Errorf("expected type ProfileReferenceValid, got %s", conditions[0].Type)
	}
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", conditions[0].Status)
	}
	if conditions[0].Reason != "AllReferencesResolved" {
		t.Errorf("expected reason AllReferencesResolved, got %s", conditions[0].Reason)
	}

	setCondition(&conditions, "ProfileReferenceValid", false, "UnresolvedProfileReference", "profile missing")
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition after update, got %d", len(conditions))
	}
	if conditions[0].Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %s", conditions[0].Status)
	}
}

func TestConditionReason(t *testing.T) {
	if r := conditionReason(true, "AllReferencesResolved", "UnresolvedProfileReference"); r != "AllReferencesResolved" {
		t.Errorf("expected AllReferencesResolved, got %s", r)
	}
	if r := conditionReason(false, "AllReferencesResolved", "UnresolvedProfileReference"); r != "UnresolvedProfileReference" {
		t.Errorf("expected UnresolvedProfileReference, got %s", r)
	}
}

func TestHasCondition(t *testing.T) {
	conditions := []metav1.Condition{
		{Type: "ProfileReferenceValid", Status: metav1.ConditionTrue},
	}

	if !hasCondition(conditions, "ProfileReferenceValid") {
		t.Error("expected to find ProfileReferenceValid condition")
	}
	if hasCondition(conditions, "Ready") {
		t.Error("did not expect to find Ready condition")
	}
	if hasCondition(nil, "ProfileReferenceValid") {
		t.Error("did not expect to find condition in nil slice")
	}
}

func TestSetConditionTransitions(t *testing.T) {
	var conditions []metav1.Condition

	setCondition(&conditions, "ProfileReferenceValid", false, "UnresolvedProfileReference", "missing")
	if conditions[0].Status != metav1.ConditionFalse {
		t.Error("expected False initially")
	}
	firstTransition := conditions[0].LastTransitionTime

	setCondition(&conditions, "ProfileReferenceValid", true, "AllReferencesResolved", "All profile references are valid")
	if conditions[0].Status != metav1.ConditionTrue {
		t.Error("expected True after transition")
	}
	if conditions[0].LastTransitionTime.Equal(&firstTransition) {
		t.Error("expected transition time to change on status change")
	}
}
