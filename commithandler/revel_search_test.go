package action

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"example.com/ris/entity"
)

// fakeTracker возвращает задачи из found[tracker], если их ключ был в запросе.
// Один fakeTracker может изображать несколько трекеров, как реальный jira-клиент.
type fakeTracker struct {
	found         map[entity.Tracker]map[string]time.Time // трекер -> ключ -> дата создания
	err           error
	requestedKeys map[entity.Tracker][]string // что у клиента запросили, по трекерам
}

func (f *fakeTracker) SearchIssues(_ context.Context, tracker entity.Tracker, issueKeys []string) ([]entity.TrackerIssue, error) {
	if f.requestedKeys == nil {
		f.requestedKeys = make(map[entity.Tracker][]string)
	}
	f.requestedKeys[tracker] = issueKeys

	if f.err != nil {
		return nil, f.err
	}

	var foundIssues []entity.TrackerIssue
	for _, issueKey := range issueKeys {
		if createdAt, ok := f.found[tracker][issueKey]; ok {
			foundIssues = append(foundIssues, entity.TrackerIssue{Key: issueKey, CreatedAt: createdAt})
		}
	}
	return foundIssues, nil
}

func newTestAction(sbertrack, jira TrackerProvider) *RelevantIssueSearchAction {
	return NewRelevantIssueSearchAction(sbertrack, jira, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestExecute(t *testing.T) {
	commitDate := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	sbertrack := &fakeTracker{found: map[entity.Tracker]map[string]time.Time{
		entity.TrackerSbertrack: {"ONLY-ST": commitDate, "ST-SIGMA": commitDate, "ALL": commitDate},
	}}
	jira := &fakeTracker{found: map[entity.Tracker]map[string]time.Time{
		entity.TrackerJiraSigma: {"ST-SIGMA": commitDate, "BOTH-JIRA": commitDate.Add(-5 * day), "ONLY-SIGMA": commitDate, "ALL": commitDate},
		entity.TrackerJiraDelta: {"BOTH-JIRA": commitDate.Add(-1 * day), "ONLY-DELTA": commitDate, "ALL": commitDate},
	}}

	action := newTestAction(sbertrack, jira)

	var input RelevantIssueSearchInput
	// "only-st" в нижнем регистре и дубль "ALL" проверяют нормализацию и дедупликацию ключей
	for _, issueKey := range []string{"only-st", "ST-SIGMA", "BOTH-JIRA", "ONLY-SIGMA", "ONLY-DELTA", "ALL", "ALL", "LOST"} {
		input.GitIssues = append(input.GitIssues, entity.GitIssue{Key: issueKey, CommitDate: commitDate})
	}

	searchedIssues, err := action.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	wantTrackers := []entity.Tracker{
		entity.TrackerSbertrack, // only-st
		entity.TrackerSbertrack, // ST-SIGMA: sbertrack важнее jira
		entity.TrackerJiraDelta, // BOTH-JIRA: delta ближе к коммиту
		entity.TrackerJiraSigma, // ONLY-SIGMA
		entity.TrackerJiraDelta, // ONLY-DELTA
		entity.TrackerSbertrack, // ALL
		entity.TrackerSbertrack, // ALL (второй коммит)
		// LOST — потеряшка, в результат не попадает
	}

	var gotTrackers []entity.Tracker
	for _, searchedIssue := range searchedIssues {
		gotTrackers = append(gotTrackers, searchedIssue.TrackerIssue.Tracker)
	}
	if !slices.Equal(gotTrackers, wantTrackers) {
		t.Fatalf("got %v, want %v", gotTrackers, wantTrackers)
	}

	// sbertrack-клиент вызван только для sbertrack, jira-клиент — для sigma и delta
	if len(sbertrack.requestedKeys) != 1 || sbertrack.requestedKeys[entity.TrackerSbertrack] == nil {
		t.Fatalf("sbertrack client called with unexpected trackers: %v", sbertrack.requestedKeys)
	}
	for _, tracker := range []entity.Tracker{entity.TrackerJiraSigma, entity.TrackerJiraDelta} {
		if len(jira.requestedKeys[tracker]) != 7 {
			t.Fatalf("expected 7 unique keys for %s, got %v", tracker, jira.requestedKeys[tracker])
		}
	}
}

func TestExecuteTrackerError(t *testing.T) {
	jira := &fakeTracker{err: errors.New("timeout")}
	action := newTestAction(&fakeTracker{}, jira)

	input := RelevantIssueSearchInput{GitIssues: []entity.GitIssue{{Key: "PROJ-1"}}}
	if _, err := action.Execute(context.Background(), input); err == nil {
		t.Fatal("expected error from tracker")
	}
}

func TestExecuteEmptyInput(t *testing.T) {
	sbertrack := &fakeTracker{}
	jira := &fakeTracker{}
	action := newTestAction(sbertrack, jira)

	searchedIssues, err := action.Execute(context.Background(), RelevantIssueSearchInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(searchedIssues) != 0 {
		t.Fatalf("expected empty result, got %v", searchedIssues)
	}
	if sbertrack.requestedKeys != nil || jira.requestedKeys != nil {
		t.Fatal("trackers must not be called on empty input")
	}
}
