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

// fakeTracker возвращает задачи из found, если их ключ был в запросе.
type fakeTracker struct {
	found         map[string]time.Time // ключ -> дата создания
	err           error
	requestedKeys []string
}

func (f *fakeTracker) SearchIssues(_ context.Context, issueKeys []string) ([]entity.TrackerIssue, error) {
	f.requestedKeys = issueKeys
	if f.err != nil {
		return nil, f.err
	}

	var foundIssues []entity.TrackerIssue
	for _, issueKey := range issueKeys {
		if createdAt, ok := f.found[issueKey]; ok {
			foundIssues = append(foundIssues, entity.TrackerIssue{Key: issueKey, CreatedAt: createdAt})
		}
	}
	return foundIssues, nil
}

func TestExecute(t *testing.T) {
	commitDate := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	sbertrack := &fakeTracker{found: map[string]time.Time{"ONLY-ST": commitDate, "ST-SIGMA": commitDate, "ALL": commitDate}}
	jiraSigma := &fakeTracker{found: map[string]time.Time{"ST-SIGMA": commitDate, "BOTH-JIRA": commitDate.Add(-5 * day), "ONLY-SIGMA": commitDate, "ALL": commitDate}}
	jiraDelta := &fakeTracker{found: map[string]time.Time{"BOTH-JIRA": commitDate.Add(-1 * day), "ONLY-DELTA": commitDate, "ALL": commitDate}}

	action := NewRelevantIssueSearchAction(sbertrack, jiraSigma, jiraDelta, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
		entity.TrackerSbertrack, // ST-SIGMA
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

	if len(sbertrack.requestedKeys) != 7 {
		t.Fatalf("expected 7 unique keys in request, got %v", sbertrack.requestedKeys)
	}

	jiraDelta.err = errors.New("timeout")
	if _, err := action.Execute(context.Background(), input); err == nil {
		t.Fatal("expected error from tracker")
	}
}
