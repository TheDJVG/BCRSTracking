package model

import (
	"encoding/json"
	"strconv"
	"time"
)

type RVM struct {
	ID           uint32    `json:"id"`
	SerialNumber string    `json:"serialNumber"`
	SupplierID   string    `json:"supplierId"`
	LocationName string    `json:"locationName"`
	Latitude     float64   `json:"-"`
	Longitude    float64   `json:"-"`
	CoordsColor  string    `json:"coords_color"`
	Address      string    `json:"address"`
	PostalCode   string    `json:"postalCode"`
	Status       string    `json:"status"`
	RVMRemarks   string    `json:"rvm_remarks"`
	Model        string    `json:"model"`
	RVMType      string    `json:"rvm_type"`
	GroupIDs     []uint32  `json:"groupId"`
	OpeningHours string    `json:"rvmOpeningHours"`
	RawJSON      string    `json:"-"`
	FirstSeenAt  time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
	IsDeleted    uint8     `json:"-"`
}

func (r *RVM) UnmarshalJSON(data []byte) error {
	type Alias RVM
	aux := struct {
		Lat string `json:"latitude"`
		Lng string `json:"longitude"`
		*Alias
	}{
		Alias: (*Alias)(r),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Lat != "" {
		if lat, err := strconv.ParseFloat(aux.Lat, 64); err == nil {
			r.Latitude = lat
		}
	}
	if aux.Lng != "" {
		if lng, err := strconv.ParseFloat(aux.Lng, 64); err == nil {
			r.Longitude = lng
		}
	}

	r.RawJSON = string(data)
	return nil
}

type LocationsResponse struct {
	Status string `json:"status"`
	Data   []RVM  `json:"data"`
}
