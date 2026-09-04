package model

import (
	"time"

	"github.com/google/uuid"
)

type EventType string

const (
	EventMachineAdded    EventType = "MACHINE_ADDED"
	EventMachineRemoved  EventType = "MACHINE_REMOVED"
	EventStatusChanged   EventType = "STATUS_CHANGED"
	EventMetadataUpdated EventType = "METADATA_UPDATED"
)

type RVMEvent struct {
	EventID        uuid.UUID `json:"event_id"`
	RVMID          uint32    `json:"rvm_id"`
	SerialNumber   string    `json:"serial_number"`
	SupplierID     string    `json:"supplier_id"`
	EventType      EventType `json:"event_type"`
	PreviousStatus string    `json:"previous_status"`
	CurrentStatus  string    `json:"current_status"`
	PayloadDiff    string    `json:"payload_diff"`
	RecordedAt     time.Time `json:"recorded_at"`
}
