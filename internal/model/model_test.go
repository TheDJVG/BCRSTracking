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

func TestUnmarshalAccessTokenResponse(t *testing.T) {
	// Top-level RFC3339
	raw1 := []byte(`{"token":"tok-abc-123","expiresAt":"2026-09-06T15:04:05Z"}`)
	var resp1 model.AccessTokenResponse
	if err := json.Unmarshal(raw1, &resp1); err != nil {
		t.Fatalf("failed to unmarshal top-level token: %v", err)
	}
	if resp1.Token != "tok-abc-123" {
		t.Errorf("expected tok-abc-123, got %s", resp1.Token)
	}
	expectedTime := time.Date(2026, 9, 6, 15, 4, 5, 0, time.UTC)
	if !resp1.ExpiresAt.Equal(expectedTime) {
		t.Errorf("expected %v, got %v", expectedTime, resp1.ExpiresAt)
	}

	// Nested data with Unix timestamp (seconds)
	raw2 := []byte(`{"status":"success","data":{"token":"tok-xyz-456","expiresAt":1788703200}}`)
	var resp2 model.AccessTokenResponse
	if err := json.Unmarshal(raw2, &resp2); err != nil {
		t.Fatalf("failed to unmarshal nested data: %v", err)
	}
	if resp2.Token != "tok-xyz-456" {
		t.Errorf("expected tok-xyz-456, got %s", resp2.Token)
	}
	if resp2.ExpiresAt.Unix() != 1788703200 {
		t.Errorf("expected unix 1788703200, got %d", resp2.ExpiresAt.Unix())
	}

	// Nested data with Unix milliseconds and snake_case
	raw3 := []byte(`{"data":{"access_token":"tok-milli","expires_at":1788703200000}}`)
	var resp3 model.AccessTokenResponse
	if err := json.Unmarshal(raw3, &resp3); err != nil {
		t.Fatalf("failed to unmarshal snake_case millis: %v", err)
	}
	if resp3.Token != "tok-milli" {
		t.Errorf("expected tok-milli, got %s", resp3.Token)
	}
	if resp3.ExpiresAt.Unix() != 1788703200 {
		t.Errorf("expected unix 1788703200, got %d", resp3.ExpiresAt.Unix())
	}

	// Missing token returns error
	raw4 := []byte(`{"status":"error","message":"no token"}`)
	var resp4 model.AccessTokenResponse
	if err := json.Unmarshal(raw4, &resp4); err == nil {
		t.Error("expected error for missing token, got nil")
	}

	// Direct string in "data" field
	raw5 := []byte(`{"status":"success","data":"tok-direct-string"}`)
	var resp5 model.AccessTokenResponse
	if err := json.Unmarshal(raw5, &resp5); err != nil {
		t.Fatalf("failed to unmarshal direct string token: %v", err)
	}
	if resp5.Token != "tok-direct-string" {
		t.Errorf("expected tok-direct-string, got %s", resp5.Token)
	}

	// Relative expires_in (seconds)
	before6 := time.Now()
	raw6 := []byte(`{"data":{"token":"tok-exp-in","expires_in":3600}}`)
	var resp6 model.AccessTokenResponse
	if err := json.Unmarshal(raw6, &resp6); err != nil {
		t.Fatalf("failed to unmarshal relative expires_in: %v", err)
	}
	if resp6.Token != "tok-exp-in" {
		t.Errorf("expected tok-exp-in, got %s", resp6.Token)
	}
	if resp6.ExpiresAt.Before(before6.Add(3590*time.Second)) || resp6.ExpiresAt.After(time.Now().Add(3610*time.Second)) {
		t.Errorf("unexpected relative expires_in duration: %v", resp6.ExpiresAt)
	}

	// Relative expiresIn string
	raw7 := []byte(`{"data":{"token":"tok-exp-in-camel","expiresIn":"1800"}}`)
	var resp7 model.AccessTokenResponse
	if err := json.Unmarshal(raw7, &resp7); err != nil {
		t.Fatalf("failed to unmarshal relative expiresIn string: %v", err)
	}
	if resp7.Token != "tok-exp-in-camel" {
		t.Errorf("expected tok-exp-in-camel, got %s", resp7.Token)
	}
	if resp7.ExpiresAt.Before(before6.Add(1790*time.Second)) || resp7.ExpiresAt.After(time.Now().Add(1810*time.Second)) {
		t.Errorf("unexpected relative expiresIn duration: %v", resp7.ExpiresAt)
	}

	// Whitespace-only token returns error
	raw8 := []byte(`{"token":"   "}`)
	var resp8 model.AccessTokenResponse
	if err := json.Unmarshal(raw8, &resp8); err == nil {
		t.Error("expected error for whitespace-only token, got nil")
	}

	// ISO8601 without offset (e.g. 2026-09-06T15:04:05)
	raw9 := []byte(`{"token":"tok-iso-local","expiresAt":"2026-09-06T15:04:05"}`)
	var resp9 model.AccessTokenResponse
	if err := json.Unmarshal(raw9, &resp9); err != nil {
		t.Fatalf("failed to unmarshal ISO8601 without offset: %v", err)
	}
	if resp9.Token != "tok-iso-local" {
		t.Errorf("expected tok-iso-local, got %s", resp9.Token)
	}
	expected9 := time.Date(2026, 9, 6, 15, 4, 5, 0, time.UTC)
	if !resp9.ExpiresAt.Equal(expected9) {
		t.Errorf("expected %v, got %v", expected9, resp9.ExpiresAt)
	}

	// Standard JWT exp claim
	raw10 := []byte(`{"token":"tok-jwt-exp","exp":1788703200}`)
	var resp10 model.AccessTokenResponse
	if err := json.Unmarshal(raw10, &resp10); err != nil {
		t.Fatalf("failed to unmarshal JWT exp claim: %v", err)
	}
	if resp10.Token != "tok-jwt-exp" {
		t.Errorf("expected tok-jwt-exp, got %s", resp10.Token)
	}
	if resp10.ExpiresAt.Unix() != 1788703200 {
		t.Errorf("expected unix 1788703200, got %d", resp10.ExpiresAt.Unix())
	}

	// Standard OAuth expiry claim
	raw11 := []byte(`{"token":"tok-oauth-exp","expiry":1788703200}`)
	var resp11 model.AccessTokenResponse
	if err := json.Unmarshal(raw11, &resp11); err != nil {
		t.Fatalf("failed to unmarshal OAuth expiry claim: %v", err)
	}
	if resp11.ExpiresAt.Unix() != 1788703200 {
		t.Errorf("expected unix 1788703200, got %d", resp11.ExpiresAt.Unix())
	}

	// Result wrapper object
	raw12 := []byte(`{"status":"success","result":{"token":"tok-result-obj","expiresAt":"2026-09-06T15:04:05Z"}}`)
	var resp12 model.AccessTokenResponse
	if err := json.Unmarshal(raw12, &resp12); err != nil {
		t.Fatalf("failed to unmarshal result wrapper: %v", err)
	}
	if resp12.Token != "tok-result-obj" {
		t.Errorf("expected tok-result-obj, got %s", resp12.Token)
	}

	// Direct string in result field
	raw13 := []byte(`{"status":"success","result":"tok-result-str"}`)
	var resp13 model.AccessTokenResponse
	if err := json.Unmarshal(raw13, &resp13); err != nil {
		t.Fatalf("failed to unmarshal direct string result: %v", err)
	}
	if resp13.Token != "tok-result-str" {
		t.Errorf("expected tok-result-str, got %s", resp13.Token)
	}

	// Fractional relative expires_in (seconds with subseconds)
	raw14 := []byte(`{"data":{"token":"tok-frac","expires_in":3600.5}}`)
	var resp14 model.AccessTokenResponse
	if err := json.Unmarshal(raw14, &resp14); err != nil {
		t.Fatalf("failed to unmarshal fractional expires_in: %v", err)
	}
	if resp14.Token != "tok-frac" {
		t.Errorf("expected tok-frac, got %s", resp14.Token)
	}
	if resp14.ExpiresAt.Before(before6.Add(3595*time.Second)) || resp14.ExpiresAt.After(time.Now().Add(3605*time.Second)) {
		t.Errorf("unexpected fractional expires_in: %v", resp14.ExpiresAt)
	}

	// mapToken field and response wrapper
	raw15 := []byte(`{"response":{"mapToken":"tok-map-tok","ttl":1800}}`)
	var resp15 model.AccessTokenResponse
	if err := json.Unmarshal(raw15, &resp15); err != nil {
		t.Fatalf("failed to unmarshal mapToken with response wrapper: %v", err)
	}
	if resp15.Token != "tok-map-tok" {
		t.Errorf("expected tok-map-tok, got %s", resp15.Token)
	}

	// payload wrapper and ttl with duration unit string
	raw16 := []byte(`{"payload":{"token":"tok-duration-str","ttl":"1h"}}`)
	var resp16 model.AccessTokenResponse
	if err := json.Unmarshal(raw16, &resp16); err != nil {
		t.Fatalf("failed to unmarshal ttl duration string: %v", err)
	}
	if resp16.Token != "tok-duration-str" {
		t.Errorf("expected tok-duration-str, got %s", resp16.Token)
	}
	if resp16.ExpiresAt.Before(time.Now().Add(59*time.Minute)) || resp16.ExpiresAt.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("unexpected duration string expiry: %v", resp16.ExpiresAt)
	}

	// Subsecond float Unix timestamp
	raw17 := []byte(`{"token":"tok-subsec-unix","exp":1788703200.75}`)
	var resp17 model.AccessTokenResponse
	if err := json.Unmarshal(raw17, &resp17); err != nil {
		t.Fatalf("failed to unmarshal subsecond float unix: %v", err)
	}
	if resp17.ExpiresAt.Unix() != 1788703200 || resp17.ExpiresAt.Nanosecond() != 750000000 {
		t.Errorf("expected unix 1788703200 and 750000000 ns, got %d and %d", resp17.ExpiresAt.Unix(), resp17.ExpiresAt.Nanosecond())
	}

	// Large number in expires_in safely treated as Unix timestamp without integer overflow
	raw18 := []byte(`{"token":"tok-large-exp-in","expires_in":1788703200}`)
	var resp18 model.AccessTokenResponse
	if err := json.Unmarshal(raw18, &resp18); err != nil {
		t.Fatalf("failed to unmarshal large expires_in: %v", err)
	}
	if resp18.ExpiresAt.Unix() != 1788703200 {
		t.Errorf("expected large expires_in to be treated as unix timestamp 1788703200, got %d", resp18.ExpiresAt.Unix())
	}
}

