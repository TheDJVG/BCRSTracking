package storage

import (
	"context"
	"sync"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/model"
)

type BatchSink interface {
	InsertRVMs(ctx context.Context, rvms []model.RVM) error
	InsertEvents(ctx context.Context, events []model.RVMEvent) error
	InsertBins(ctx context.Context, bins []model.BinStatus) error
}

type BatchBuffer struct {
	sink          BatchSink
	metrics       *metrics.Metrics
	batchSize     int
	flushInterval time.Duration

	rvmChan      chan model.RVM
	eventChan    chan model.RVMEvent
	binChan      chan model.BinStatus
	flushReqChan chan chan struct{}

	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewBatchBuffer(cfg *config.Config, m *metrics.Metrics, sink BatchSink) *BatchBuffer {
	batchSize := 500
	flushInterval := 5 * time.Second

	if cfg != nil {
		if cfg.ClickHouseBatchSize > 0 {
			batchSize = cfg.ClickHouseBatchSize
		}
		if cfg.ClickHouseFlushInterval > 0 {
			flushInterval = cfg.ClickHouseFlushInterval
		}
	}

	bb := &BatchBuffer{
		sink:          sink,
		metrics:       m,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		rvmChan:       make(chan model.RVM, 5000),
		eventChan:     make(chan model.RVMEvent, 5000),
		binChan:       make(chan model.BinStatus, 10000),
		flushReqChan:  make(chan chan struct{}),
		stopChan:      make(chan struct{}),
	}

	bb.wg.Add(1)
	go bb.worker()
	return bb
}

func (bb *BatchBuffer) EnqueueRVM(r model.RVM) {
	select {
	case bb.rvmChan <- r:
		if bb.metrics != nil {
			bb.metrics.SetClickHouseBufferDepth("rvm_current", float64(len(bb.rvmChan)))
		}
	default:
	}
}

func (bb *BatchBuffer) EnqueueEvent(e model.RVMEvent) {
	select {
	case bb.eventChan <- e:
		if bb.metrics != nil {
			bb.metrics.SetClickHouseBufferDepth("rvm_events", float64(len(bb.eventChan)))
		}
	default:
	}
}

func (bb *BatchBuffer) EnqueueBin(b model.BinStatus) {
	select {
	case bb.binChan <- b:
		if bb.metrics != nil {
			bb.metrics.SetClickHouseBufferDepth("rvm_bin_snapshots", float64(len(bb.binChan)))
		}
	default:
	}
}

func (bb *BatchBuffer) worker() {
	defer bb.wg.Done()
	ticker := time.NewTicker(bb.flushInterval)
	defer ticker.Stop()

	var rvms []model.RVM
	var events []model.RVMEvent
	var bins []model.BinStatus

	drainChannels := func() {
		for {
			select {
			case r := <-bb.rvmChan:
				rvms = append(rvms, r)
			default:
				goto drainEvents
			}
		}
	drainEvents:
		for {
			select {
			case e := <-bb.eventChan:
				events = append(events, e)
			default:
				goto drainBins
			}
		}
	drainBins:
		for {
			select {
			case b := <-bb.binChan:
				bins = append(bins, b)
			default:
				return
			}
		}
	}

	flush := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if len(rvms) > 0 && bb.sink != nil {
			start := time.Now()
			err := bb.sink.InsertRVMs(ctx, rvms)
			if bb.metrics != nil {
				bb.metrics.RecordClickHouseInsert("rvm_current", len(rvms), time.Since(start), err)
			}
			if err == nil {
				rvms = rvms[:0]
			}
		} else if len(rvms) > 0 && bb.sink == nil {
			rvms = rvms[:0]
		}

		if len(events) > 0 && bb.sink != nil {
			start := time.Now()
			err := bb.sink.InsertEvents(ctx, events)
			if bb.metrics != nil {
				bb.metrics.RecordClickHouseInsert("rvm_events", len(events), time.Since(start), err)
			}
			if err == nil {
				events = events[:0]
			}
		} else if len(events) > 0 && bb.sink == nil {
			events = events[:0]
		}

		if len(bins) > 0 && bb.sink != nil {
			start := time.Now()
			err := bb.sink.InsertBins(ctx, bins)
			if bb.metrics != nil {
				bb.metrics.RecordClickHouseInsert("rvm_bin_snapshots", len(bins), time.Since(start), err)
			}
			if err == nil {
				bins = bins[:0]
			}
		} else if len(bins) > 0 && bb.sink == nil {
			bins = bins[:0]
		}

		if bb.metrics != nil {
			bb.metrics.SetClickHouseBufferDepth("rvm_current", float64(len(bb.rvmChan)+len(rvms)))
			bb.metrics.SetClickHouseBufferDepth("rvm_events", float64(len(bb.eventChan)+len(events)))
			bb.metrics.SetClickHouseBufferDepth("rvm_bin_snapshots", float64(len(bb.binChan)+len(bins)))
		}
	}

	for {
		select {
		case r := <-bb.rvmChan:
			rvms = append(rvms, r)
			if len(rvms) >= bb.batchSize {
				flush()
			}
		case e := <-bb.eventChan:
			events = append(events, e)
			if len(events) >= bb.batchSize {
				flush()
			}
		case b := <-bb.binChan:
			bins = append(bins, b)
			if len(bins) >= bb.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case ack := <-bb.flushReqChan:
			drainChannels()
			flush()
			close(ack)
		case <-bb.stopChan:
			drainChannels()
			flush()
			return
		}
	}
}

func (bb *BatchBuffer) Flush(ctx context.Context) {
	ack := make(chan struct{})
	select {
	case bb.flushReqChan <- ack:
		select {
		case <-ack:
		case <-ctx.Done():
		case <-bb.stopChan:
		}
	case <-bb.stopChan:
	case <-ctx.Done():
	}
}

func (bb *BatchBuffer) Stop() {
	bb.stopOnce.Do(func() {
		close(bb.stopChan)
		bb.wg.Wait()
	})
}

func (bb *BatchBuffer) Close() {
	bb.Stop()
}
