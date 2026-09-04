package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/client"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
	"github.com/TheDJVG/BCRSTracking/internal/storage"
)

type binKey struct {
	rvmID           uint32
	compartmentType string
}

type Poller struct {
	cfg     *config.Config
	client  *client.Client
	storage *storage.Storage
	metrics *metrics.Metrics
	logger  *slog.Logger

	mu            sync.RWMutex
	activeRVMs    map[uint32]model.RVM
	lastBinCounts map[binKey]uint32
}

func New(cfg *config.Config, c *client.Client, s *storage.Storage, m *metrics.Metrics, l *slog.Logger) *Poller {
	if l == nil {
		l = slog.Default()
	}
	return &Poller{
		cfg:           cfg,
		client:        c,
		storage:       s,
		metrics:       m,
		logger:        l,
		activeRVMs:    make(map[uint32]model.RVM),
		lastBinCounts: make(map[binKey]uint32),
	}
}

func (p *Poller) HydrateFromStorage(ctx context.Context) error {
	if p.storage == nil {
		return nil
	}

	p.logger.Info("hydrating poller state from ClickHouse...")

	rvms, err := p.storage.LoadActiveRVMs(ctx)
	if err != nil {
		p.logger.Warn("failed to load active RVMs from ClickHouse (starting with empty state)", "error", err)
	} else if len(rvms) > 0 {
		p.mu.Lock()
		p.activeRVMs = rvms
		statusCounts := make(map[string]map[string]int)
		for _, loc := range rvms {
			if _, ok := statusCounts[loc.Status]; !ok {
				statusCounts[loc.Status] = make(map[string]int)
			}
			statusCounts[loc.Status][loc.SupplierID]++
		}
		p.mu.Unlock()

		// Pre-populate Prometheus RVM gauge metrics immediately on boot
		if p.metrics != nil {
			p.metrics.RVMTotal.Reset()
			for status, suppMap := range statusCounts {
				for supplier, count := range suppMap {
					p.metrics.SetRVMTotal(status, supplier, "Default", float64(count))
				}
			}
		}
		p.logger.Info("successfully hydrated active RVMs from ClickHouse", "count", len(rvms))
	}

	binList, err := p.storage.LoadLatestBinSnapshots(ctx)
	if err != nil {
		p.logger.Warn("failed to load latest bin snapshots from ClickHouse", "error", err)
	} else if len(binList) > 0 {
		p.applyHydratedBinSnapshots(binList)
		p.logger.Info("successfully hydrated latest bin counts and gauges from ClickHouse", "count", len(binList))
	}

	return nil
}

func (p *Poller) applyHydratedBinSnapshots(binList []storage.LatestBinInfo) {
	p.mu.Lock()
	binCounts := make(map[string]map[string]float64)
	for _, b := range binList {
		key := binKey{rvmID: b.RVMID, compartmentType: b.CompartmentType}
		p.lastBinCounts[key] = b.CurrentCount

		supplier := "UNKNOWN"
		if rvm, ok := p.activeRVMs[b.RVMID]; ok && rvm.SupplierID != "" {
			supplier = rvm.SupplierID
		}
		cType := b.CompartmentType
		if cType == "" {
			cType = "UNKNOWN"
		}

		var inBin float64
		if b.Capacity > 0 {
			inBin = float64(b.CurrentCount)
		} else {
			// Zero-capacity machines (TOMRA) report fill percentage in ThresholdLevel; estimate physical in-bin count (standard 1,000 capacity)
			inBin = float64(b.ThresholdLevel) * 10.0
		}

		if _, ok := binCounts[cType]; !ok {
			binCounts[cType] = make(map[string]float64)
		}
		binCounts[cType][supplier] += inBin
	}
	p.mu.Unlock()

	// Pre-populate Prometheus bin current count gauges immediately on boot
	if p.metrics != nil {
		p.metrics.ResetBinCurrentCounts()
		for cType, suppMap := range binCounts {
			for supplier, count := range suppMap {
				p.metrics.SetBinCurrentCount(cType, supplier, count)
			}
		}
	}
}

func (p *Poller) Start(ctx context.Context) {
	p.logger.Info("starting poller service...")

	// Hydrate previous state from ClickHouse if available
	_ = p.HydrateFromStorage(ctx)

	locInterval := 15 * time.Minute
	binInterval := 5 * time.Minute
	if p.cfg != nil {
		if p.cfg.LocationPollInterval > 0 {
			locInterval = p.cfg.LocationPollInterval
		}
		if p.cfg.BinPollInterval > 0 {
			binInterval = p.cfg.BinPollInterval
		}
	}

	var wg sync.WaitGroup

	// Locations Polling Loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.PollLocations(ctx)

		locTicker := time.NewTicker(locInterval)
		defer locTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-locTicker.C:
				p.PollLocations(ctx)
			}
		}
	}()

	// Bin Status Polling Loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.PollBins(ctx)

		binTicker := time.NewTicker(binInterval)
		defer binTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-binTicker.C:
				p.PollBins(ctx)
			}
		}
	}()

	wg.Wait()
	p.logger.Info("poller service stopped")
}

func (p *Poller) PollLocations(ctx context.Context) {
	start := time.Now()
	p.logger.Info("polling location directory...")

	if p.client == nil {
		p.logger.Warn("client is nil, skipping location poll")
		return
	}

	locations, err := p.client.GetLocations(ctx)
	if p.metrics != nil {
		p.metrics.RecordPollCycle("locations", time.Since(start), err)
	}
	if err != nil {
		p.logger.Error("failed to poll locations", "error", err)
		return
	}

	p.mu.Lock()
	diff := DiffState(p.activeRVMs, locations, time.Now())
	newMap := make(map[uint32]model.RVM, len(locations))
	statusCounts := make(map[string]map[string]int)

	for _, loc := range locations {
		newMap[loc.ID] = loc
		if _, ok := statusCounts[loc.Status]; !ok {
			statusCounts[loc.Status] = make(map[string]int)
		}
		statusCounts[loc.Status][loc.SupplierID]++
	}
	p.activeRVMs = newMap
	p.mu.Unlock()

	// Update gauge metrics
	if p.metrics != nil {
		if p.metrics.RVMTotal != nil {
			p.metrics.RVMTotal.Reset()
		}
		for status, suppliers := range statusCounts {
			for supplier, count := range suppliers {
				p.metrics.SetRVMTotal(status, supplier, "", float64(count))
			}
		}
	}

	// Buffer events and updates
	if p.storage != nil {
		buf := p.storage.Buffer()
		if buf != nil {
			for _, rvm := range diff.Added {
				buf.EnqueueRVM(rvm)
			}
			for _, rvm := range diff.Updated {
				buf.EnqueueRVM(rvm)
			}
			for _, rvm := range diff.Removed {
				buf.EnqueueRVM(rvm)
			}
			for _, evt := range diff.Events {
				buf.EnqueueEvent(evt)
			}
		}
	}

	p.logger.Info("locations poll completed",
		"total", len(locations),
		"added", len(diff.Added),
		"updated", len(diff.Updated),
		"removed", len(diff.Removed),
		"duration", time.Since(start),
	)
}

func (p *Poller) PollLocationsOnce(ctx context.Context) {
	p.PollLocations(ctx)
}

func (p *Poller) PollBins(ctx context.Context) {
	start := time.Now()
	p.mu.RLock()
	rvmList := make([]model.RVM, 0, len(p.activeRVMs))
	for _, r := range p.activeRVMs {
		if r.IsDeleted == 0 {
			rvmList = append(rvmList, r)
		}
	}
	p.mu.RUnlock()

	if len(rvmList) == 0 {
		p.logger.Warn("no active RVMs found to poll bins for")
		return
	}

	p.logger.Info("starting bin status poll cycle", "rvm_count", len(rvmList))

	jobs := make(chan model.RVM, len(rvmList))
	for _, r := range rvmList {
		jobs <- r
	}
	close(jobs)

	type scrapedBin struct {
		bin      model.BinStatus
		supplier string
	}

	scrapedCh := make(chan scrapedBin, 1000)
	var collectorWg sync.WaitGroup
	var allBins []scrapedBin

	collectorWg.Add(1)
	go func() {
		defer collectorWg.Done()
		for item := range scrapedCh {
			allBins = append(allBins, item)
		}
	}()

	var wg sync.WaitGroup
	workers := 8
	if p.cfg != nil && p.cfg.BinWorkerConcurrency > 0 {
		workers = p.cfg.BinWorkerConcurrency
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rvm := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if p.client == nil {
					continue
				}

				bins, err := p.client.GetBinStatus(ctx, rvm.ID)
				if err != nil {
					p.logger.Debug("failed to get bin status for RVM", "rvm_id", rvm.ID, "error", err)
					continue
				}

				now := time.Now()
				for _, b := range bins {
					b.RVMID = rvm.ID
					b.SerialNumber = rvm.SerialNumber
					b.PolledAt = now
					if p.storage != nil && p.storage.Buffer() != nil {
						p.storage.Buffer().EnqueueBin(b)
					}
					scrapedCh <- scrapedBin{bin: b, supplier: rvm.SupplierID}
				}
			}
		}()
	}

	wg.Wait()
	close(scrapedCh)
	collectorWg.Wait()

	// Track container ingestion deltas and update current bin capacities
	p.mu.Lock()
	currentCounts := make(map[string]map[string]float64)
	for _, item := range allBins {
		b := item.bin
		supplier := item.supplier
		cType := b.CompartmentType
		if cType == "" {
			cType = "UNKNOWN"
		}
		if supplier == "" {
			supplier = "UNKNOWN"
		}

		if _, ok := currentCounts[cType]; !ok {
			currentCounts[cType] = make(map[string]float64)
		}
		var inBin float64
		if b.Capacity > 0 {
			inBin = float64(b.CurrentCount)
		} else {
			// Zero-capacity machines (TOMRA) report fill percentage in ThresholdLevel; estimate physical in-bin count (standard 1,000 capacity)
			inBin = float64(b.ThresholdLevel) * 10.0
		}
		currentCounts[cType][supplier] += inBin

		key := binKey{rvmID: b.RVMID, compartmentType: b.CompartmentType}
		prevCount, exists := p.lastBinCounts[key]
		p.lastBinCounts[key] = b.CurrentCount

		var delta uint32
		if exists {
			if b.Capacity == 0 {
				// TOMRA: Monotonic lifetime odometer counter
				if prevCount > 0 && b.CurrentCount >= prevCount {
					diff := b.CurrentCount - prevCount
					// Sanity cap: single machine cannot physically accept >200 containers in 5 minutes
					if diff <= 200 {
						delta = diff
					}
				}
			} else {
				// RVMS & SGRECYCLE: Physical in-bin container count
				if b.CurrentCount > prevCount {
					diff := b.CurrentCount - prevCount
					if diff <= 200 {
						delta = diff
					}
				} else if b.CurrentCount < prevCount {
					// Actual bin emptying: previous count was significant and current dropped substantially
					if prevCount >= 50 && b.CurrentCount <= prevCount/2 {
						if b.CurrentCount <= 200 {
							delta = b.CurrentCount
						}
					}
					// Otherwise, minor sensor fluctuations (e.g., 450 -> 448) are ignored
				}
			}
		}
		if p.metrics != nil {
			p.metrics.RecordContainersIngested(cType, supplier, delta)
		}
	}
	p.mu.Unlock()

	if p.metrics != nil {
		p.metrics.ResetBinCurrentCounts()
		for cType, suppMap := range currentCounts {
			for supplier, count := range suppMap {
				p.metrics.SetBinCurrentCount(cType, supplier, count)
			}
		}
		p.metrics.RecordPollCycle("bins", time.Since(start), ctx.Err())
	}
	p.logger.Info("bin status poll cycle completed", "duration", time.Since(start))
}

func (p *Poller) PollBinsOnce(ctx context.Context) {
	p.PollBins(ctx)
}

func (p *Poller) ActiveRVMs() map[uint32]model.RVM {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copied := make(map[uint32]model.RVM, len(p.activeRVMs))
	for k, v := range p.activeRVMs {
		copied[k] = v
	}
	return copied
}

// SetActiveRVMsForTest sets active RVMs for unit tests.
func (p *Poller) SetActiveRVMsForTest(rvms []model.RVM) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeRVMs = make(map[uint32]model.RVM, len(rvms))
	for _, r := range rvms {
		p.activeRVMs[r.ID] = r
	}
}

// ApplyHydratedBinSnapshotsForTest exposes applyHydratedBinSnapshots for unit tests.
func (p *Poller) ApplyHydratedBinSnapshotsForTest(binList []storage.LatestBinInfo) {
	p.applyHydratedBinSnapshots(binList)
}
