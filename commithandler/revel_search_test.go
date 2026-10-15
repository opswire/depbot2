package action

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeProvider struct {
	name  string
	found map[string]time.Time // ключ -> CreatedAt
	err   error
}

func (f fakeProvider) Type() string { return f.name }

func (f fakeProvider) SearchIssue(_ context.Context, issues []GitIssue) ([]SearchedIssue, error) {
	if f.err != nil {
		return nil, f.err
	}
	var result []SearchedIssue
	for _, issue := range issues {
		if createdAt, ok := f.found[issue.Key]; ok {
			result = append(result, SearchedIssue{GitIssue: issue, Tracker: f.name, CreatedAt: createdAt})
		}
	}
	return result, nil
}

func TestExecute(t *testing.T) {
	commit := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	action := NewRelevantIssueSearchAction(
		fakeProvider{name: "sbertrack", found: map[string]time.Time{"A": commit, "B": commit, "F": commit}},
		fakeProvider{name: "sigma", found: map[string]time.Time{"B": commit, "C": commit.Add(-5 * day), "D": commit, "F": commit}},
		fakeProvider{name: "delta", found: map[string]time.Time{"C": commit.Add(-1 * day), "E": commit, "F": commit}},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	input := RelevantIssueSearchInput{}
	for _, key := range []string{"A", "B", "C", "D", "E", "F", "LOST"} {
		input.GitIssues = append(input.GitIssues, GitIssue{Key: key, CommitDate: commit})
	}

	got, err := action.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"A": "sbertrack", "B": "sbertrack", "C": "delta", "D": "sigma", "E": "delta", "F": "sbertrack"}
	if len(got) != len(want) {
		t.Fatalf("got %d issues, want %d", len(got), len(want))
	}
	for _, issue := range got {
		if want[issue.GitIssue.Key] != issue.Tracker {
			t.Errorf("%s: got %s, want %s", issue.GitIssue.Key, issue.Tracker, want[issue.GitIssue.Key])
		}
	}

	action.jiraDelta = fakeProvider{name: "delta", err: errors.New("boom")}
	if _, err := action.Execute(context.Background(), input); err == nil {
		t.Fatal("expected error")
	}
}
