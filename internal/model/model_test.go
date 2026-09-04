package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheDJVG/BCRSTracking/internal/model"
)

func TestUnmarshalLocationsResponse(t *testing.T) {
	rawPayload := []byte(`{
		"status": "ok",
		"data": [
			{
				"id": 213,
				"serialNumber": "SGRVM0275",
				"supplierId": "SGRECYCLE001",
				"locationName": "2 Marsiling Dr",
				"latitude": "1.43992600",
				"longitude": "103.77612100",
				"coords_color": "Red",
				"address": "2 Marsiling Dr, 730002",
				"postalCode": "730002",
				"status": "RUNNING",
				"rvm_remarks": "Test Remarks",
				"model": "Model-A",
				"rvm_type": "Type-1",
				"groupId": [39, 42],
				"rvmOpeningHours": "Mon - Sun: 24 Hrs",
				"new_future_field": "some_future_value"
			}
		]
	}`)

	var resp model.LocationsResponse
	if err := json.Unmarshal(rawPayload, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if resp.Status != "ok" || len(resp.Data) != 1 {
		t.Fatalf("expected 1 item with status ok, got %v", resp)
	}

	rvm := resp.Data[0]
	if rvm.ID != 213 || rvm.SerialNumber != "SGRVM0275" {
		t.Errorf("unexpected ID/serial: %d, %s", rvm.ID, rvm.SerialNumber)
	}
	if rvm.SupplierID != "SGRECYCLE001" {
		t.Errorf("unexpected supplierId: %s", rvm.SupplierID)
	}
	if rvm.LocationName != "2 Marsiling Dr" {
		t.Errorf("unexpected locationName: %s", rvm.LocationName)
	}
	if rvm.Latitude != 1.439926 || rvm.Longitude != 103.776121 {
		t.Errorf("lat/lng conversion error: %f, %f", rvm.Latitude, rvm.Longitude)
	}
	if rvm.CoordsColor != "Red" || rvm.Address != "2 Marsiling Dr, 730002" || rvm.PostalCode != "730002" {
		t.Errorf("unexpected metadata: %+v", rvm)
	}
	if rvm.Status != "RUNNING" || rvm.RVMRemarks != "Test Remarks" || rvm.Model != "Model-A" || rvm.RVMType != "Type-1" {
		t.Errorf("unexpected status/model/type: %+v", rvm)
	}
	if len(rvm.GroupIDs) != 2 || rvm.GroupIDs[0] != 39 || rvm.GroupIDs[1] != 42 {
		t.Errorf("groupIDs mismatch: %v", rvm.GroupIDs)
	}
	if rvm.OpeningHours != "Mon - Sun: 24 Hrs" {
		t.Errorf("unexpected opening hours: %s", rvm.OpeningHours)
	}
	if len(rvm.RawJSON) == 0 {
		t.Errorf("expected RawJSON to be preserved")
	}
	// Verify raw JSON retains unknown future fields
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rvm.RawJSON), &rawMap); err != nil {
		t.Fatalf("failed to unmarshal RawJSON: %v", err)
	}
	if rawMap["new_future_field"] != "some_future_value" {
		t.Errorf("expected raw JSON to preserve new_future_field")
	}
}

func TestUnmarshalLocationsResponse_InvalidCoordinates(t *testing.T) {
	rawPayload := []byte(`{
		"status": "ok",
		"data": [
			{
				"id": 214,
				"serialNumber": "SGRVM0276",
				"latitude": "invalid-lat",
				"longitude": ""
			}
		]
	}`)

	var resp model.LocationsResponse
	if err := json.Unmarshal(rawPayload, &resp); err != nil {
		t.Fatalf("unmarshal should not fail on invalid coordinates: %v", err)
	}

	rvm := resp.Data[0]
	if rvm.Latitude != 0 || rvm.Longitude != 0 {
		t.Errorf("expected 0 for invalid/empty coordinates, got lat=%f, lng=%f", rvm.Latitude, rvm.Longitude)
	}
	if len(rvm.RawJSON) == 0 {
		t.Errorf("expected RawJSON to be preserved")
	}
}

func TestUnmarshalRVM_InvalidJSON(t *testing.T) {
	var rvm model.RVM
	if err := json.Unmarshal([]byte(`{invalid`), &rvm); err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestUnmarshalBinStatusResponse(t *testing.T) {
	rawPayload := []byte(`{
		"status": "ok",
		"data": [
			{
				"id": 72737099,
				"rvm_id": 213,
				"compartment_type": "Bottle Can",
				"capacity": 800,
				"current_count": 265,
				"threshold_level": 33,
				"last_updated": "2026-08-23T05:10:11.000Z",
				"extra_metric": 42
			}
		]
	}`)

	var resp model.BinStatusResponse
	if err := json.Unmarshal(rawPayload, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 bin status item")
	}

	bin := resp.Data[0]
	if bin.ID != 72737099 || bin.RVMID != 213 || bin.CurrentCount != 265 {
		t.Errorf("unexpected bin values: %+v", bin)
	}
	if bin.CompartmentType != "Bottle Can" || bin.Capacity != 800 || bin.ThresholdLevel != 33 {
		t.Errorf("unexpected bin metadata: %+v", bin)
	}
	if bin.LastUpdated == nil || bin.LastUpdated.Year() != 2026 || bin.LastUpdated.Month() != time.August || bin.LastUpdated.Day() != 23 {
		t.Errorf("unexpected timestamp: %v", bin.LastUpdated)
	}
	if len(bin.RawJSON) == 0 {
		t.Errorf("expected RawJSON to be preserved")
	}
	// Verify raw JSON retains unknown extra metrics
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(bin.RawJSON), &rawMap); err != nil {
		t.Fatalf("failed to unmarshal RawJSON: %v", err)
	}
	if rawMap["extra_metric"] != float64(42) {
		t.Errorf("expected raw JSON to preserve extra_metric")
	}
}

func TestUnmarshalBinStatusResponse_RFC3339Timestamp(t *testing.T) {
	rawPayload := []byte(`{
		"status": "ok",
		"data": [
			{
				"id": 100,
				"rvm_id": 200,
				"compartment_type": "Plastic",
				"last_updated": "2026-08-23T05:10:11+08:00"
			}
		]
	}`)

	var resp model.BinStatusResponse
	if err := json.Unmarshal(rawPayload, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	bin := resp.Data[0]
	if bin.LastUpdated == nil {
		t.Fatal("expected non-nil LastUpdated")
	}
	expectedUTC := time.Date(2026, 8, 22, 21, 10, 11, 0, time.UTC)
	if !bin.LastUpdated.Equal(expectedUTC) {
		t.Errorf("expected %v, got %v", expectedUTC, bin.LastUpdated.UTC())
	}
}

func TestUnmarshalBinStatusResponse_NullOrEmptyTimestamp(t *testing.T) {
	rawPayload := []byte(`{
		"status": "ok",
		"data": [
			{
				"id": 101,
				"rvm_id": 201,
				"last_updated": null
			},
			{
				"id": 102,
				"rvm_id": 202,
				"last_updated": ""
			}
		]
	}`)

	var resp model.BinStatusResponse
	if err := json.Unmarshal(rawPayload, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 bin status items")
	}
	if resp.Data[0].LastUpdated != nil {
		t.Errorf("expected nil LastUpdated for null, got %v", resp.Data[0].LastUpdated)
	}
	if resp.Data[1].LastUpdated != nil {
		t.Errorf("expected nil LastUpdated for empty string, got %v", resp.Data[1].LastUpdated)
	}
}

func TestUnmarshalBinStatus_InvalidJSON(t *testing.T) {
	var bin model.BinStatus
	if err := json.Unmarshal([]byte(`{invalid`), &bin); err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestRVMEventTypesAndStructure(t *testing.T) {
	eventID := uuid.New()
	now := time.Now().UTC()

	event := model.RVMEvent{
		EventID:        eventID,
		RVMID:          213,
		SerialNumber:   "SGRVM0275",
		SupplierID:     "SGRECYCLE001",
		EventType:      model.EventStatusChanged,
		PreviousStatus: "RUNNING",
		CurrentStatus:  "OFFLINE",
		PayloadDiff:    `{"status":{"old":"RUNNING","new":"OFFLINE"}}`,
		RecordedAt:     now,
	}

	if event.EventType != model.EventType("STATUS_CHANGED") {
		t.Errorf("unexpected event type: %s", event.EventType)
	}
	if model.EventMachineAdded != "MACHINE_ADDED" {
		t.Errorf("unexpected EventMachineAdded constant: %s", model.EventMachineAdded)
	}
	if model.EventMachineRemoved != "MACHINE_REMOVED" {
		t.Errorf("unexpected EventMachineRemoved constant: %s", model.EventMachineRemoved)
	}
	if model.EventMetadataUpdated != "METADATA_UPDATED" {
		t.Errorf("unexpected EventMetadataUpdated constant: %s", model.EventMetadataUpdated)
	}
}
