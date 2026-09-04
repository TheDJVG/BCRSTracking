package poller

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/model"
	"github.com/google/uuid"
)

type DiffResult struct {
	Added   []model.RVM
	Updated []model.RVM
	Removed []model.RVM
	Events  []model.RVMEvent
}

func DiffState(previous map[uint32]model.RVM, current []model.RVM, now time.Time) DiffResult {
	var result DiffResult
	currentMap := make(map[uint32]model.RVM, len(current))

	for _, rvm := range current {
		currentMap[rvm.ID] = rvm
		prev, exists := previous[rvm.ID]

		if !exists || prev.IsDeleted == 1 {
			rvm.FirstSeenAt = now
			rvm.UpdatedAt = now
			rvm.IsDeleted = 0
			result.Added = append(result.Added, rvm)
			result.Events = append(result.Events, model.RVMEvent{
				EventID:       uuid.New(),
				RVMID:         rvm.ID,
				SerialNumber:  rvm.SerialNumber,
				SupplierID:    rvm.SupplierID,
				EventType:     model.EventMachineAdded,
				CurrentStatus: rvm.Status,
				PayloadDiff:   rvm.RawJSON,
				RecordedAt:    now,
			})
		} else {
			rvm.FirstSeenAt = prev.FirstSeenAt
			rvm.UpdatedAt = now
			rvm.IsDeleted = 0
			if prev.Status != rvm.Status {
				diffBytes, _ := json.Marshal(map[string]string{
					"from": prev.Status,
					"to":   rvm.Status,
				})
				result.Events = append(result.Events, model.RVMEvent{
					EventID:        uuid.New(),
					RVMID:          rvm.ID,
					SerialNumber:   rvm.SerialNumber,
					SupplierID:     rvm.SupplierID,
					EventType:      model.EventStatusChanged,
					PreviousStatus: prev.Status,
					CurrentStatus:  rvm.Status,
					PayloadDiff:    string(diffBytes),
					RecordedAt:     now,
				})
				result.Updated = append(result.Updated, rvm)
			} else if prev.LocationName != rvm.LocationName || prev.Address != rvm.Address {
				result.Events = append(result.Events, model.RVMEvent{
					EventID:        uuid.New(),
					RVMID:          rvm.ID,
					SerialNumber:   rvm.SerialNumber,
					SupplierID:     rvm.SupplierID,
					EventType:      model.EventMetadataUpdated,
					PreviousStatus: prev.Status,
					CurrentStatus:  rvm.Status,
					PayloadDiff:    rvm.RawJSON,
					RecordedAt:     now,
				})
				result.Updated = append(result.Updated, rvm)
			}
		}
	}

	for prevID, prev := range previous {
		if prev.IsDeleted == 1 {
			continue
		}
		if _, exists := currentMap[prevID]; !exists {
			prev.IsDeleted = 1
			prev.UpdatedAt = now
			result.Removed = append(result.Removed, prev)
			result.Events = append(result.Events, model.RVMEvent{
				EventID:        uuid.New(),
				RVMID:          prev.ID,
				SerialNumber:   prev.SerialNumber,
				SupplierID:     prev.SupplierID,
				EventType:      model.EventMachineRemoved,
				PreviousStatus: prev.Status,
				CurrentStatus:  "DELETED",
				PayloadDiff:    fmt.Sprintf(`{"deleted_id": %d}`, prev.ID),
				RecordedAt:     now,
			})
		}
	}

	return result
}
