package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func buildEventID(triggerType, namespace, name, uid, qualifier string) string {
	return strings.ToLower(fmt.Sprintf("%s:%s:%s:%s:%s", triggerType, namespace, name, uid, qualifier))
}

func recordKeyForEvent(eventID string) string {
	hash := sha256.Sum256([]byte(eventID))
	return "run." + hex.EncodeToString(hash[:16])
}
