package controllers

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// updateEvent creates a minimal UpdateEvent for use in predicate tests.
func updateEvent(obj client.Object) event.UpdateEvent {
	return event.UpdateEvent{
		ObjectOld: obj,
		ObjectNew: obj,
	}
}
