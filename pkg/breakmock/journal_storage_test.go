// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	"slices"
	"testing"
)

func TestJournalWrapReconfigurationAndSnapshotIsolation(t *testing.T) {
	s := NewStore()
	s.ConfigureJournal(true, 3)
	add := func(n int) string { return s.RecordServed(Request{URL: fmt.Sprint(n)}, nil, 200, nil, nil) }
	check := func(want ...string) {
		t.Helper()
		entries := s.journalSnapshot()
		got := make([]string, len(entries))
		for i, e := range entries {
			got[i] = e.request.URL
		}
		if !slices.Equal(got, want) {
			t.Fatalf("journal URLs %v, want %v", got, want)
		}
	}
	for i := 0; i < 8; i++ {
		add(i)
	}
	check("7", "6", "5")
	old := s.journalSnapshot()
	id := old[1].event["id"].(string)
	s.AppendSubEvent(id, "ERROR", map[string]any{"message": "test"})
	if len(old[1].event["subEvents"].([]any)) != 0 {
		t.Fatal("subevent mutated prior snapshot")
	}
	if len(s.journalSnapshot()[1].event["subEvents"].([]any)) != 1 {
		t.Fatal("wrapped subevent missing")
	}
	s.removeEvents(map[string]bool{id: true})
	check("7", "5")
	add(8)
	add(9)
	check("9", "8", "7")
	s.ConfigureJournal(true, 5)
	add(10)
	add(11)
	check("11", "10", "9", "8", "7")
	s.ConfigureJournal(true, 2)
	// Reconfiguration historically applies the lower cap on the next insertion.
	check("11", "10", "9", "8", "7")
	add(12)
	add(13)
	check("13", "12")
	s.ConfigureJournal(true, 0)
	add(14)
	add(15)
	check("15", "14", "13", "12")
	s.ConfigureJournal(false, 3)
	if add(16) != "" {
		t.Fatal("disabled journal returned event ID")
	}
	check()
	s.ConfigureJournal(true, 3)
	add(17)
	add(18)
	add(19)
	add(20)
	s.ResetRequests()
	add(21)
	check("21")
	if old[0].request.URL != "7" || old[2].request.URL != "5" {
		t.Fatal("overwrite mutated prior snapshot")
	}
}
