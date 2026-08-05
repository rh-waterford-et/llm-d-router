/*
Copyright 2025 The llm-d Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cucumber/godog"
)

// saturationTracker is a minimal in-process model of the concurrency saturation concept
// used by the flow control subsystem. It tracks in-flight requests and computes saturation
// as inflight/maxConcurrency, matching the behavioral contract of the production detector.
type saturationTracker struct {
	maxConcurrency int64
	inflight       atomic.Int64
}

func newSaturationTracker(maxConcurrency int) *saturationTracker {
	return &saturationTracker{maxConcurrency: int64(maxConcurrency)}
}

func (s *saturationTracker) track() {
	s.inflight.Add(1)
}

func (s *saturationTracker) release() {
	s.inflight.Add(-1)
}

func (s *saturationTracker) saturation() float64 {
	current := s.inflight.Load()
	if s.maxConcurrency == 0 {
		return 1.0
	}
	sat := float64(current) / float64(s.maxConcurrency)
	if sat > 1.0 {
		return 1.0
	}
	if sat < 0.0 {
		return 0.0
	}
	return sat
}

func initFlowControlSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a saturation detector with max concurrency (\d+)$`, aSaturationDetector)
	ctx.Step(`^(\d+) requests are currently in flight$`, requestsInFlight)
	ctx.Step(`^a new request arrives$`, aNewRequestArrives)
	ctx.Step(`^the saturation level is below 1\.0$`, saturationBelowOne)
	ctx.Step(`^the saturation level is checked$`, saturationIsChecked)
	ctx.Step(`^the saturation level is 1\.0$`, saturationIsOne)
	ctx.Step(`^the saturation level is 0\.0$`, saturationIsZero)
	ctx.Step(`^(\d+) requests are tracked and then released$`, requestsTrackedAndReleased)
	ctx.Step(`^(\d+) requests are tracked and released concurrently$`, requestsTrackedAndReleasedConcurrently)
	ctx.Step(`^all saturation readings are in the range 0\.0 to 1\.0$`, allSaturationReadingsInRange)
}

func aSaturationDetector(ctx context.Context, maxConcurrency int) (context.Context, error) {
	tracker := newSaturationTracker(maxConcurrency)
	return withValue(ctx, keyDetector, tracker), nil
}

func requestsInFlight(ctx context.Context, count int) (context.Context, error) {
	tracker := getValue[*saturationTracker](ctx, keyDetector)
	for range count {
		tracker.track()
	}
	return ctx, nil
}

func aNewRequestArrives(ctx context.Context) (context.Context, error) {
	tracker := getValue[*saturationTracker](ctx, keyDetector)
	tracker.track()
	sat := tracker.saturation()
	return withValue(ctx, keySaturation, sat), nil
}

func saturationBelowOne(ctx context.Context) error {
	sat := getValue[float64](ctx, keySaturation)
	if sat >= 1.0 {
		return fmt.Errorf("expected saturation below 1.0, got %f", sat)
	}
	return nil
}

func saturationIsChecked(ctx context.Context) (context.Context, error) {
	tracker := getValue[*saturationTracker](ctx, keyDetector)
	sat := tracker.saturation()
	return withValue(ctx, keySaturation, sat), nil
}

func saturationIsOne(ctx context.Context) error {
	sat := getValue[float64](ctx, keySaturation)
	if sat != 1.0 {
		return fmt.Errorf("expected saturation 1.0, got %f", sat)
	}
	return nil
}

func saturationIsZero(ctx context.Context) error {
	sat := getValue[float64](ctx, keySaturation)
	if sat != 0.0 {
		return fmt.Errorf("expected saturation 0.0, got %f", sat)
	}
	return nil
}

func requestsTrackedAndReleased(ctx context.Context, count int) (context.Context, error) {
	tracker := getValue[*saturationTracker](ctx, keyDetector)
	for range count {
		tracker.track()
	}
	for range count {
		tracker.release()
	}
	return ctx, nil
}

func requestsTrackedAndReleasedConcurrently(ctx context.Context, count int) (context.Context, error) {
	tracker := getValue[*saturationTracker](ctx, keyDetector)
	readings := make([]float64, 0, count*10)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.track()
			sat := tracker.saturation()
			mu.Lock()
			readings = append(readings, sat)
			mu.Unlock()
			tracker.release()
		}()
	}
	wg.Wait()

	ctx = withValue(ctx, keySaturationAll, readings)
	return ctx, nil
}

func allSaturationReadingsInRange(ctx context.Context) error {
	readings := getValue[[]float64](ctx, keySaturationAll)
	if len(readings) == 0 {
		return errors.New("no saturation readings recorded")
	}
	for i, sat := range readings {
		if sat < 0.0 || sat > 1.0 {
			return fmt.Errorf("reading %d out of range: %f", i, sat)
		}
	}
	return nil
}
