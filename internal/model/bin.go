package model

import (
	"encoding/json"
	"time"
)

type BinStatus struct {
	ID              uint64     `json:"id"`
	RVMID           uint32     `json:"rvm_id"`
	SerialNumber    string     `json:"-"`
	CompartmentType string     `json:"compartment_type"`
	Capacity        uint32     `json:"capacity"`
	CurrentCount    uint32     `json:"current_count"`
	ThresholdLevel  uint8      `json:"threshold_level"`
	LastUpdated     *time.Time `json:"last_updated"`
	PolledAt        time.Time  `json:"-"`
	RawJSON         string     `json:"-"`
}

func (b *BinStatus) UnmarshalJSON(data []byte) error {
	type Alias BinStatus
	aux := struct {
		LastUpdatedStr *string `json:"last_updated"`
		*Alias
	}{
		Alias: (*Alias)(b),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.LastUpdatedStr != nil && *aux.LastUpdatedStr != "" {
		if t, err := time.Parse(time.RFC3339, *aux.LastUpdatedStr); err == nil {
			b.LastUpdated = &t
		} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", *aux.LastUpdatedStr); err == nil {
			b.LastUpdated = &t
		}
	}

	b.RawJSON = string(data)
	return nil
}

type BinStatusResponse struct {
	Status string      `json:"status"`
	Data   []BinStatus `json:"data"`
}
