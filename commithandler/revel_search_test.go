package action

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"example.com/ris/entity"
)

// fakeTracker — фейковый клиент трекера.
// Один экземпляр может изображать несколько трекеров, как реальный jira-клиент (sigma и delta).
type fakeTracker struct {
	foundIssues   map[entity.Tracker]map[string]time.Time // трекер -> ключ -> дата создания
	errors        map[entity.Tracker]error                // трекер -> ошибка, которую вернуть
	requestedKeys map[entity.Tracker][]string             // трекер -> ключи, с которыми вызвали
}

func newFakeTracker() *fakeTracker {
	return &fakeTracker{
		foundIssues:   make(map[entity.Tracker]map[string]time.Time),
		errors:        make(map[entity.Tracker]error),
		requestedKeys: make(map[entity.Tracker][]string),
	}
}

func (f *fakeTracker) addIssue(tracker entity.Tracker, issueKey string, createdAt time.Time) {
	if f.foundIssues[tracker] == nil {
		f.foundIssues[tracker] = make(map[string]time.Time)
	}
	f.foundIssues[tracker][issueKey] = createdAt
}

func (f *fakeTracker) SearchIssues(_ context.Context, tracker entity.Tracker, issueKeys []string) ([]entity.TrackerIssue, error) {
	f.requestedKeys[tracker] = issueKeys

	if err := f.errors[tracker]; err != nil {
		return nil, err
	}

	// Как и настоящий трекер, ищем без учёта регистра и возвращаем ключ в том виде, в каком он хранится.
	var foundIssues []entity.TrackerIssue
	for _, requestedKey := range issueKeys {
		for storedKey, createdAt := range f.foundIssues[tracker] {
			if strings.EqualFold(storedKey, requestedKey) {
				foundIssues = append(foundIssues, entity.TrackerIssue{Key: storedKey, CreatedAt: createdAt})
			}
		}
	}
	return foundIssues, nil
}

type RelevantIssueSearchSuite struct {
	suite.Suite

	ctx        context.Context
	commitDate time.Time

	sbertrack *fakeTracker
	jira      *fakeTracker
	action    *RelevantIssueSearchAction
}

func TestRelevantIssueSearchSuite(t *testing.T) {
	suite.Run(t, new(RelevantIssueSearchSuite))
}

// SetupTest и SetupSubTest дают каждому тесту и каждому кейсу матрицы чистые фейки.
func (s *RelevantIssueSearchSuite) SetupTest() {
	s.ctx = context.Background()
	s.commitDate = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	s.sbertrack = newFakeTracker()
	s.jira = newFakeTracker()
	s.action = NewRelevantIssueSearchAction(s.sbertrack, s.jira, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (s *RelevantIssueSearchSuite) SetupSubTest() {
	s.SetupTest()
}

// addIssue кладёт задачу в нужный фейк: sbertrack — в свой клиент, sigma и delta — в jira.
func (s *RelevantIssueSearchSuite) addIssue(tracker entity.Tracker, issueKey string, createdAt time.Time) {
	s.trackerClient(tracker).addIssue(tracker, issueKey, createdAt)
}

func (s *RelevantIssueSearchSuite) trackerClient(tracker entity.Tracker) *fakeTracker {
	if tracker == entity.TrackerSbertrack {
		return s.sbertrack
	}
	return s.jira
}

func (s *RelevantIssueSearchSuite) gitIssues(issueKeys ...string) RelevantIssueSearchInput {
	var input RelevantIssueSearchInput
	for _, issueKey := range issueKeys {
		input.GitIssues = append(input.GitIssues, entity.GitIssue{
			Key:        issueKey,
			CommitHash: "hash-" + issueKey,
			CommitDate: s.commitDate,
		})
	}
	return input
}

// TestSelectsRelevantTracker — матрица правил выбора трекера для одной задачи.
func (s *RelevantIssueSearchSuite) TestSelectsRelevantTracker() {
	const issueKey = "PROJ-1"
	day := 24 * time.Hour

	// foundIn: трекер -> смещение даты создания задачи относительно даты коммита
	testCases := []struct {
		name        string
		foundIn     map[entity.Tracker]time.Duration
		wantTracker entity.Tracker // пусто — задача не найдена нигде
	}{
		{
			name:    "не найдена нигде",
			foundIn: nil,
		},
		{
			name:        "только sbertrack",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerSbertrack: 0},
			wantTracker: entity.TrackerSbertrack,
		},
		{
			name:        "только jira sigma",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraSigma: 0},
			wantTracker: entity.TrackerJiraSigma,
		},
		{
			name:        "только jira delta",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraDelta: 0},
			wantTracker: entity.TrackerJiraDelta,
		},
		{
			name:        "sbertrack и sigma — выбираем sbertrack",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerSbertrack: -10 * day, entity.TrackerJiraSigma: 0},
			wantTracker: entity.TrackerSbertrack,
		},
		{
			name:        "sbertrack и delta — выбираем sbertrack",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerSbertrack: -10 * day, entity.TrackerJiraDelta: 0},
			wantTracker: entity.TrackerSbertrack,
		},
		{
			name:        "все три — выбираем sbertrack",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerSbertrack: -10 * day, entity.TrackerJiraSigma: 0, entity.TrackerJiraDelta: 0},
			wantTracker: entity.TrackerSbertrack,
		},
		{
			name:        "sigma и delta — sigma ближе к коммиту",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraSigma: -1 * day, entity.TrackerJiraDelta: -5 * day},
			wantTracker: entity.TrackerJiraSigma,
		},
		{
			name:        "sigma и delta — delta ближе к коммиту",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraSigma: -5 * day, entity.TrackerJiraDelta: -1 * day},
			wantTracker: entity.TrackerJiraDelta,
		},
		{
			name:        "sigma и delta — задача создана после коммита, но ближе",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraSigma: -5 * day, entity.TrackerJiraDelta: 2 * day},
			wantTracker: entity.TrackerJiraDelta,
		},
		{
			name:        "sigma и delta — одинаково близко, выбираем sigma",
			foundIn:     map[entity.Tracker]time.Duration{entity.TrackerJiraSigma: -3 * day, entity.TrackerJiraDelta: 3 * day},
			wantTracker: entity.TrackerJiraSigma,
		},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			for tracker, createdOffset := range testCase.foundIn {
				s.addIssue(tracker, issueKey, s.commitDate.Add(createdOffset))
			}

			searchedIssues, err := s.action.Execute(s.ctx, s.gitIssues(issueKey))
			s.Require().NoError(err)

			if testCase.wantTracker == "" {
				s.Empty(searchedIssues)
				return
			}

			s.Require().Len(searchedIssues, 1)
			s.Equal(testCase.wantTracker, searchedIssues[0].TrackerIssue.Tracker)
			s.Equal(issueKey, searchedIssues[0].TrackerIssue.Key)
			s.Equal(issueKey, searchedIssues[0].GitIssue.Key)
		})
	}
}

// TestKeepsOrderAndSkipsLostIssues — порядок результата как во входе, потеряшки выпадают,
// задача из нескольких коммитов попадает в результат для каждого коммита.
func (s *RelevantIssueSearchSuite) TestKeepsOrderAndSkipsLostIssues() {
	s.addIssue(entity.TrackerJiraDelta, "C-1", s.commitDate)
	s.addIssue(entity.TrackerSbertrack, "A-1", s.commitDate)
	s.addIssue(entity.TrackerJiraSigma, "B-1", s.commitDate)

	searchedIssues, err := s.action.Execute(s.ctx, s.gitIssues("A-1", "LOST-1", "B-1", "C-1", "A-1"))
	s.Require().NoError(err)

	var gotKeys []string
	for _, searchedIssue := range searchedIssues {
		gotKeys = append(gotKeys, searchedIssue.GitIssue.Key)
	}
	s.Equal([]string{"A-1", "B-1", "C-1", "A-1"}, gotKeys)
}

// TestNormalizesIssueKeys — ключ из коммита в любом регистре и с пробелами
// уходит в трекер нормализованным и сопоставляется с ответом трекера.
func (s *RelevantIssueSearchSuite) TestNormalizesIssueKeys() {
	testCases := []struct {
		name        string
		gitIssueKey string // как ключ записан в коммите
		trackerKey  string // как ключ вернул трекер
	}{
		{name: "нижний регистр в коммите", gitIssueKey: "proj-1", trackerKey: "PROJ-1"},
		{name: "пробелы по краям", gitIssueKey: "  PROJ-1 ", trackerKey: "PROJ-1"},
		{name: "трекер вернул в нижнем регистре", gitIssueKey: "PROJ-1", trackerKey: "proj-1"},
	}

	for _, testCase := range testCases {
		s.Run(testCase.name, func() {
			s.addIssue(entity.TrackerSbertrack, testCase.trackerKey, s.commitDate)

			searchedIssues, err := s.action.Execute(s.ctx, s.gitIssues(testCase.gitIssueKey))
			s.Require().NoError(err)

			s.Equal([]string{"PROJ-1"}, s.sbertrack.requestedKeys[entity.TrackerSbertrack])
			s.Require().Len(searchedIssues, 1)
			s.Equal(entity.TrackerSbertrack, searchedIssues[0].TrackerIssue.Tracker)
			s.Equal(testCase.gitIssueKey, searchedIssues[0].GitIssue.Key, "исходный ключ из коммита не меняется")
		})
	}
}

// TestDeduplicatesIssueKeys — одна задача в нескольких коммитах уходит в трекер одним ключом.
func (s *RelevantIssueSearchSuite) TestDeduplicatesIssueKeys() {
	_, err := s.action.Execute(s.ctx, s.gitIssues("PROJ-1", "proj-1", "PROJ-2", "PROJ-1"))
	s.Require().NoError(err)

	wantKeys := []string{"PROJ-1", "PROJ-2"}
	s.Equal(wantKeys, s.sbertrack.requestedKeys[entity.TrackerSbertrack])
	s.Equal(wantKeys, s.jira.requestedKeys[entity.TrackerJiraSigma])
	s.Equal(wantKeys, s.jira.requestedKeys[entity.TrackerJiraDelta])
}

// TestRoutesTrackersToClients — sbertrack-клиент спрашивают только про sbertrack,
// jira-клиент — про sigma и delta.
func (s *RelevantIssueSearchSuite) TestRoutesTrackersToClients() {
	_, err := s.action.Execute(s.ctx, s.gitIssues("PROJ-1"))
	s.Require().NoError(err)

	s.Len(s.sbertrack.requestedKeys, 1)
	s.Contains(s.sbertrack.requestedKeys, entity.TrackerSbertrack)

	s.Len(s.jira.requestedKeys, 2)
	s.Contains(s.jira.requestedKeys, entity.TrackerJiraSigma)
	s.Contains(s.jira.requestedKeys, entity.TrackerJiraDelta)
}

// TestReturnsTrackerError — ошибка любого трекера прерывает поиск и пробрасывается наверх.
func (s *RelevantIssueSearchSuite) TestReturnsTrackerError() {
	for _, failingTracker := range []entity.Tracker{entity.TrackerSbertrack, entity.TrackerJiraSigma, entity.TrackerJiraDelta} {
		s.Run(string(failingTracker), func() {
			trackerErr := errors.New("timeout")
			s.trackerClient(failingTracker).errors[failingTracker] = trackerErr

			searchedIssues, err := s.action.Execute(s.ctx, s.gitIssues("PROJ-1"))

			s.Require().ErrorIs(err, trackerErr)
			s.ErrorContains(err, string(failingTracker))
			s.Nil(searchedIssues)
		})
	}
}

// TestEmptyInput — на пустом входе трекеры не вызываются.
func (s *RelevantIssueSearchSuite) TestEmptyInput() {
	searchedIssues, err := s.action.Execute(s.ctx, RelevantIssueSearchInput{})

	s.Require().NoError(err)
	s.Empty(searchedIssues)
	s.Empty(s.sbertrack.requestedKeys)
	s.Empty(s.jira.requestedKeys)
}
