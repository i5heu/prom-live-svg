package app

import (
	"testing"
	"time"
)

func TestRequestStatStorePersistsAcrossRestartAndCounterReset(t *testing.T) { // A
	t.Parallel()

	dir := t.TempDir()
	store, err := newRequestStatStore(dir)
	if err != nil {
		t.Fatalf("newRequestStatStore returned error: %v", err)
	}

	seededAt := time.Unix(1779896355, 0).UTC()
	if got, want := initializeRequestStatEntry(t, store, 140000, 145678, seededAt).Total, 140000.0; got != want {
		t.Fatalf("unexpected seeded initial total: got %v want %v", got, want)
	}
	if got, want := addRequestStatDelta(t, store, 8, 145686, seededAt.Add(15*time.Second)).Total, 140008.0; got != want {
		t.Fatalf("unexpected accumulated total before restart: got %v want %v", got, want)
	}

	reloadedStore, err := newRequestStatStore(dir)
	if err != nil {
		t.Fatalf("reload request stat store: %v", err)
	}

	if got, want := addRequestStatDelta(t, reloadedStore, 5, 5, seededAt.Add(30*time.Second)).Total, 140013.0; got != want {
		t.Fatalf("unexpected accumulated total after reset: got %v want %v", got, want)
	}
	if got, want := addRequestStatDelta(t, reloadedStore, 4, 9, seededAt.Add(45*time.Second)).Total, 140017.0; got != want {
		t.Fatalf("unexpected accumulated total after resumed growth: got %v want %v", got, want)
	}

	entry, ok := reloadedStore.GetEntry("chrony_requests_summary", "all_time_requests")
	if !ok {
		t.Fatal("expected persisted request stat entry to exist after reload")
	}
	if got, want := entry.LastObservedAt, seededAt.Add(45*time.Second).Unix(); got != want {
		t.Fatalf("unexpected last observed timestamp: got %d want %d", got, want)
	}
}

func TestRequestStatStoreKeepsStatsIsolated(t *testing.T) { // A
	t.Parallel()

	store, err := newRequestStatStore(t.TempDir())
	if err != nil {
		t.Fatalf("newRequestStatStore returned error: %v", err)
	}

	at := time.Unix(1779896355, 0).UTC()
	if got, want := initializeNamedRequestStatEntry(t, store, "chrony_requests_summary", "all_time_requests", 50, 50, at).Total, 50.0; got != want {
		t.Fatalf("unexpected total for first stat: got %v want %v", got, want)
	}
	if got, want := initializeNamedRequestStatEntry(t, store, "chrony_requests_summary", "other_total", 10, 10, at).Total, 10.0; got != want {
		t.Fatalf("unexpected total for second stat: got %v want %v", got, want)
	}
	if got, want := initializeNamedRequestStatEntry(t, store, "other_chart", "all_time_requests", 7, 7, at).Total, 7.0; got != want {
		t.Fatalf("unexpected total for third stat: got %v want %v", got, want)
	}
}

func initializeRequestStatEntry(t *testing.T, store *requestStatStore, total float64, lastObserved float64, observedAt time.Time) requestStatEntry { // A
	t.Helper()
	return initializeNamedRequestStatEntry(t, store, "chrony_requests_summary", "all_time_requests", total, lastObserved, observedAt)
}

func initializeNamedRequestStatEntry(t *testing.T, store *requestStatStore, chartName string, statName string, total float64, lastObserved float64, observedAt time.Time) requestStatEntry { // A
	t.Helper()

	entry, err := store.InitializeEntry(chartName, statName, total, lastObserved, observedAt)
	if err != nil {
		t.Fatalf("InitializeEntry returned error: %v", err)
	}
	return entry
}

func addRequestStatDelta(t *testing.T, store *requestStatStore, delta float64, lastObserved float64, observedAt time.Time) requestStatEntry { // A
	t.Helper()

	entry, err := store.AddDelta("chrony_requests_summary", "all_time_requests", delta, lastObserved, observedAt)
	if err != nil {
		t.Fatalf("AddDelta returned error: %v", err)
	}
	return entry
}
