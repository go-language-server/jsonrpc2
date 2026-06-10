// Copyright 2026 The Go Language Server Authors. All rights reserved.
// SPDX-License-Identifier: BSD-3-Clause

package jsonrpc2

import "testing"

func TestOutgoingCallSlotsTakeNumericID(t *testing.T) {
	t.Parallel()

	var slots outgoingCallSlots
	w := newSlotTestWaiter()
	slots.Add(NewNumberID(1), w)

	got, ok := slots.Take(NewNumberID(1))
	if !ok {
		t.Fatal("Take did not find registered numeric id")
	}
	if got != w {
		t.Fatalf("Take returned waiter %p, want %p", got, w)
	}
	if slots.Len() != 0 {
		t.Fatalf("Len after Take = %d, want 0", slots.Len())
	}
}

func TestOutgoingCallSlotsCollisionAndTombstone(t *testing.T) {
	t.Parallel()

	var slots outgoingCallSlots
	first := newSlotTestWaiter()
	colliding := newSlotTestWaiter()

	slots.Add(NewNumberID(1), first)
	// 17 collides with 1 in the initial 16-slot table and must remain reachable
	// after removing the earlier probe-chain entry.
	slots.Add(NewNumberID(17), colliding)

	if got, ok := slots.Take(NewNumberID(1)); !ok || got != first {
		t.Fatalf("Take(1) = (%p, %v), want (%p, true)", got, ok, first)
	}
	if got, ok := slots.Take(NewNumberID(17)); !ok || got != colliding {
		t.Fatalf("Take(17) = (%p, %v), want (%p, true)", got, ok, colliding)
	}
	if _, ok := slots.Take(NewNumberID(17)); ok {
		t.Fatal("Take found already removed id")
	}
}

func TestOutgoingCallSlotsGrowthPreservesWaiters(t *testing.T) {
	t.Parallel()

	var slots outgoingCallSlots
	waiters := make(map[int64]*waiter)
	for id := int64(1); id <= int64(initialOutgoingCallSlots); id++ {
		w := newSlotTestWaiter()
		waiters[id] = w
		slots.Add(NewNumberID(id), w)
	}
	if slots.Len() != len(waiters) {
		t.Fatalf("Len after Add = %d, want %d", slots.Len(), len(waiters))
	}

	for id, want := range waiters {
		got, ok := slots.Take(NewNumberID(id))
		if !ok || got != want {
			t.Fatalf("Take(%d) = (%p, %v), want (%p, true)", id, got, ok, want)
		}
	}
}

func TestOutgoingCallSlotsUnmatchedIDs(t *testing.T) {
	t.Parallel()

	var slots outgoingCallSlots
	slots.Add(NewNumberID(3), newSlotTestWaiter())

	if _, ok := slots.Take(NewStringID("3")); ok {
		t.Fatal("string id matched a generated numeric waiter")
	}
	if _, ok := slots.Take(ID{}); ok {
		t.Fatal("unset id matched a generated numeric waiter")
	}
	if slots.Len() != 1 {
		t.Fatalf("Len after unmatched IDs = %d, want 1", slots.Len())
	}
}

func TestOutgoingCallSlotsDrain(t *testing.T) {
	t.Parallel()

	var slots outgoingCallSlots
	want := map[int64]*waiter{
		1:  newSlotTestWaiter(),
		17: newSlotTestWaiter(),
		33: newSlotTestWaiter(),
	}
	for id, w := range want {
		slots.Add(NewNumberID(id), w)
	}

	got := make(map[int64]*waiter)
	slots.Drain(func(id ID, w *waiter) {
		n, ok := id.Number()
		if !ok {
			t.Fatalf("Drain id %v is not numeric", id)
		}
		got[n] = w
	})
	if slots.Len() != 0 {
		t.Fatalf("Len after Drain = %d, want 0", slots.Len())
	}
	for id, wantWaiter := range want {
		if got[id] != wantWaiter {
			t.Fatalf("Drain[%d] = %p, want %p", id, got[id], wantWaiter)
		}
		if _, ok := slots.Take(NewNumberID(id)); ok {
			t.Fatalf("Take(%d) found drained id", id)
		}
	}
}

func newSlotTestWaiter() *waiter { return &waiter{ready: make(chan struct{}, 1)} }
