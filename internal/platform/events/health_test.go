// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/events"
)

// --- Bus.ReportHealth ---

func TestBusReportHealthDefaultsToHealthyBeforeAnyPublish(t *testing.T) {
	bus := events.NewBus(events.WithBusComponent("event-bus"))
	health := bus.ReportHealth(context.Background())
	if health.Component != "event-bus" {
		t.Fatalf("Component = %q, want %q", health.Component, "event-bus")
	}
	if health.Health != domain.HealthHealthy {
		t.Fatalf("Health = %q, want %q (no evidence of failure yet)", health.Health, domain.HealthHealthy)
	}
	if health.Lifecycle != domain.LifecycleRunning {
		t.Fatalf("Lifecycle = %q, want %q", health.Lifecycle, domain.LifecycleRunning)
	}
}

func TestBusReportHealthAfterSuccessfulPublish(t *testing.T) {
	clock := newTestClock(testNow)
	bus := events.NewBus(events.WithBusClock(clock))
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := bus.Publish(context.Background(), testEvent("e-1", "t")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	health := bus.ReportHealth(context.Background())
	if health.Health != domain.HealthHealthy {
		t.Fatalf("Health = %q, want %q", health.Health, domain.HealthHealthy)
	}
	if !health.LastSuccessfulCheck.Equal(testNow) {
		t.Fatalf("LastSuccessfulCheck = %v, want %v", health.LastSuccessfulCheck, testNow)
	}
}

func TestBusReportHealthDegradedAfterSubscriberFailure(t *testing.T) {
	bus := events.NewBus()
	failure := contracts.NewError(contracts.Internal, "handler exploded")
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error { return failure }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := bus.Publish(context.Background(), testEvent("e-1", "t")); err == nil {
		t.Fatal("expected Publish to return an error")
	}

	health := bus.ReportHealth(context.Background())
	if health.Health != domain.HealthDegraded {
		t.Fatalf("Health = %q, want %q", health.Health, domain.HealthDegraded)
	}
	if health.DegradedReason == "" {
		t.Fatal("expected a non-empty DegradedReason")
	}
	if health.LastFailureCategory != string(contracts.Internal) {
		t.Fatalf("LastFailureCategory = %q, want %q", health.LastFailureCategory, contracts.Internal)
	}
	if health.RedactionClass == domain.RedactionNone {
		t.Fatal("expected DegradedReason to default to redacted (not RedactionNone) in a support bundle")
	}
}

func TestBusReportHealthRecoversAfterSuccessfulPublishFollowingFailure(t *testing.T) {
	bus := events.NewBus()
	fail := true
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		if fail {
			return errors.New("boom")
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), testEvent("e-1", "t")); err == nil {
		t.Fatal("expected first Publish to fail")
	}
	if health := bus.ReportHealth(context.Background()); health.Health != domain.HealthDegraded {
		t.Fatalf("Health after failure = %q, want %q", health.Health, domain.HealthDegraded)
	}

	fail = false
	if err := bus.Publish(context.Background(), testEvent("e-2", "t")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if health := bus.ReportHealth(context.Background()); health.Health != domain.HealthHealthy {
		t.Fatalf("Health after recovery = %q, want %q", health.Health, domain.HealthHealthy)
	}
}

// --- Worker.ReportHealth ---

func TestWorkerReportHealthUnavailableBeforeFirstRun(t *testing.T) {
	outbox := newFakeOutbox(newTestClock(testNow))
	worker := events.NewWorker(outbox, events.NewBus(), "outbox-worker")

	health := worker.ReportHealth(context.Background())
	if health.Component != "outbox-worker" {
		t.Fatalf("Component = %q, want %q", health.Component, "outbox-worker")
	}
	if health.Health != domain.HealthUnavailable {
		t.Fatalf("Health = %q, want %q (never run yet)", health.Health, domain.HealthUnavailable)
	}
}

func TestWorkerReportHealthHealthyAfterSuccessfulDrain(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	bus := events.NewBus()
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker", events.WithClock(clock))
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	health := worker.ReportHealth(context.Background())
	if health.Health != domain.HealthHealthy {
		t.Fatalf("Health = %q, want %q", health.Health, domain.HealthHealthy)
	}
	if !health.LastSuccessfulCheck.Equal(testNow) {
		t.Fatalf("LastSuccessfulCheck = %v, want %v", health.LastSuccessfulCheck, testNow)
	}
}

func TestWorkerReportHealthDegradedWhenDeliveryFails(t *testing.T) {
	clock := newTestClock(testNow)
	outbox := newFakeOutbox(clock)
	if err := outbox.Append(context.Background(), domain.OutboxEvent{Event: testEvent("e-1", "t")}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	bus := events.NewBus()
	if _, err := bus.Subscribe("t", func(context.Context, domain.Event) error {
		return contracts.NewError(contracts.Unavailable, "downstream exploded")
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	worker := events.NewWorker(outbox, bus, "outbox-worker", events.WithClock(clock))
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	health := worker.ReportHealth(context.Background())
	if health.Health != domain.HealthDegraded {
		t.Fatalf("Health = %q, want %q", health.Health, domain.HealthDegraded)
	}
	if health.LastFailureCategory != string(contracts.Unavailable) {
		t.Fatalf("LastFailureCategory = %q, want %q", health.LastFailureCategory, contracts.Unavailable)
	}
	if !health.LastSuccessfulCheck.IsZero() {
		t.Fatalf("LastSuccessfulCheck = %v, want zero (this run was not healthy)", health.LastSuccessfulCheck)
	}
}

func TestWorkerReportHealthLifecycleTracksStartStop(t *testing.T) {
	outbox := newFakeOutbox(newTestClock(testNow))
	worker := events.NewWorker(outbox, events.NewBus(), "outbox-worker")

	if lifecycle := worker.ReportHealth(context.Background()).Lifecycle; lifecycle != domain.LifecycleStopped {
		t.Fatalf("Lifecycle before Start = %q, want %q", lifecycle, domain.LifecycleStopped)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	if lifecycle := worker.ReportHealth(context.Background()).Lifecycle; lifecycle != domain.LifecycleRunning {
		t.Fatalf("Lifecycle after Start = %q, want %q", lifecycle, domain.LifecycleRunning)
	}

	worker.Stop()
	if lifecycle := worker.ReportHealth(context.Background()).Lifecycle; lifecycle != domain.LifecycleStopped {
		t.Fatalf("Lifecycle after Stop = %q, want %q", lifecycle, domain.LifecycleStopped)
	}
}
