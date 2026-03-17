package controllers

import "testing"

func TestBuildEventIDDeterministic(t *testing.T) {
	got := buildEventID("DeploymentUpdate", "Prod", "Web-App", "UID1", "generation=3")
	want := "deploymentupdate:prod:web-app:uid1:generation=3"
	if got != want {
		t.Fatalf("buildEventID() = %q, want %q", got, want)
	}
}

func TestRecordKeyForEventDeterministic(t *testing.T) {
	eventID := "deploymentupdate:prod:web:uid:generation=2"
	key1 := recordKeyForEvent(eventID)
	key2 := recordKeyForEvent(eventID)

	if key1 != key2 {
		t.Fatalf("recordKeyForEvent should be deterministic: %q vs %q", key1, key2)
	}
	if len(key1) == 0 || key1[:4] != "run." {
		t.Fatalf("record key format invalid: %q", key1)
	}
}
