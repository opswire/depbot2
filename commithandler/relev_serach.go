package action

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"example.com/ris/entity"
)

// TrackerProvider — клиент трекера задач.
// Получает ключи задач и возвращает только те, которые в трекере реально есть.
// Разбивку ключей на пачки (лимиты JQL/TQL) делает сам клиент.
type TrackerProvider interface {
	SearchIssues(ctx context.Context, issueKeys []string) ([]entity.TrackerIssue, error)
}

// RelevantIssueSearchInput — входные данные для поиска.
type RelevantIssueSearchInput struct {
	GitIssues []entity.GitIssue
}

// RelevantIssueSearchAction определяет, к какому трекеру относится каждая задача из коммитов:
// sbertrack, jira sigma или jira delta.
type RelevantIssueSearchAction struct {
	sbertrack TrackerProvider
	jiraSigma TrackerProvider
	jiraDelta TrackerProvider
	logger    *slog.Logger
}

func NewRelevantIssueSearchAction(
	sbertrack TrackerProvider,
	jiraSigma TrackerProvider,
	jiraDelta TrackerProvider,
	logger *slog.Logger,
) *RelevantIssueSearchAction {
	return &RelevantIssueSearchAction{
		sbertrack: sbertrack,
		jiraSigma: jiraSigma,
		jiraDelta: jiraDelta,
		logger:    logger,
	}
}

// Execute ищет задачи во всех трекерах и для каждой выбирает один релевантный.
//
// Правила выбора:
//   - нашлась только в одном трекере -> выбираем его
//   - sbertrack + любая jira          -> выбираем sbertrack
//   - sigma + delta (без sbertrack)   -> выбираем ту, что создана ближе к дате коммита
//   - нигде не нашлась                -> логируем как потеряшку
//
// Порядок результата совпадает с порядком input.GitIssues (без потеряшек).
func (a *RelevantIssueSearchAction) Execute(ctx context.Context, input RelevantIssueSearchInput) ([]entity.SearchedIssue, error) {
	if len(input.GitIssues) == 0 {
		return nil, nil
	}

	issueKeys := uniqueIssueKeys(input.GitIssues)

	sbertrackIssues, err := searchByKey(ctx, a.sbertrack, entity.TrackerSbertrack, issueKeys)
	if err != nil {
		return nil, err
	}

	jiraSigmaIssues, err := searchByKey(ctx, a.jiraSigma, entity.TrackerJiraSigma, issueKeys)
	if err != nil {
		return nil, err
	}

	jiraDeltaIssues, err := searchByKey(ctx, a.jiraDelta, entity.TrackerJiraDelta, issueKeys)
	if err != nil {
		return nil, err
	}

	searchedIssues := make([]entity.SearchedIssue, 0, len(input.GitIssues))
	for _, gitIssue := range input.GitIssues {
		issueKey := normalizeIssueKey(gitIssue.Key)

		relevantIssue := chooseRelevantIssue(
			gitIssue.CommitDate,
			sbertrackIssues[issueKey],
			jiraSigmaIssues[issueKey],
			jiraDeltaIssues[issueKey],
		)

		if relevantIssue == nil {
			a.logger.WarnContext(ctx, "issue not found in any tracker",
				"issue_key", gitIssue.Key,
				"commit_hash", gitIssue.CommitHash,
			)
			continue
		}

		searchedIssues = append(searchedIssues, entity.SearchedIssue{
			GitIssue:     gitIssue,
			TrackerIssue: *relevantIssue,
		})
	}

	return searchedIssues, nil
}

// chooseRelevantIssue выбирает задачу по правилам из Execute.
// nil на входе — в этом трекере задача не найдена; nil на выходе — не найдена нигде.
func chooseRelevantIssue(commitDate time.Time, sbertrackIssue, jiraSigmaIssue, jiraDeltaIssue *entity.TrackerIssue) *entity.TrackerIssue {
	switch {
	case sbertrackIssue != nil:
		return sbertrackIssue
	case jiraSigmaIssue != nil && jiraDeltaIssue != nil:
		return closerToCommit(commitDate, jiraSigmaIssue, jiraDeltaIssue)
	case jiraSigmaIssue != nil:
		return jiraSigmaIssue
	default:
		return jiraDeltaIssue // nil, если не нашлась и здесь
	}
}

// closerToCommit возвращает задачу, созданную ближе к дате коммита.
// При равенстве выбирается first.
func closerToCommit(commitDate time.Time, first, second *entity.TrackerIssue) *entity.TrackerIssue {
	firstDistance := first.CreatedAt.Sub(commitDate).Abs()
	secondDistance := second.CreatedAt.Sub(commitDate).Abs()

	if secondDistance < firstDistance {
		return second
	}
	return first
}

// searchByKey ищет задачи в трекере, проставляет им трекер и раскладывает по ключу.
func searchByKey(ctx context.Context, provider TrackerProvider, tracker entity.Tracker, issueKeys []string) (map[string]*entity.TrackerIssue, error) {
	foundIssues, err := provider.SearchIssues(ctx, issueKeys)
	if err != nil {
		return nil, fmt.Errorf("search issues in %s: %w", tracker, err)
	}

	issuesByKey := make(map[string]*entity.TrackerIssue, len(foundIssues))
	for i := range foundIssues {
		foundIssues[i].Tracker = tracker
		issuesByKey[normalizeIssueKey(foundIssues[i].Key)] = &foundIssues[i]
	}

	return issuesByKey, nil
}

// uniqueIssueKeys собирает ключи без дублей: одна задача может быть в нескольких коммитах.
func uniqueIssueKeys(gitIssues []entity.GitIssue) []string {
	seenKeys := make(map[string]struct{}, len(gitIssues))
	issueKeys := make([]string, 0, len(gitIssues))

	for _, gitIssue := range gitIssues {
		issueKey := normalizeIssueKey(gitIssue.Key)
		if _, seen := seenKeys[issueKey]; seen {
			continue
		}
		seenKeys[issueKey] = struct{}{}
		issueKeys = append(issueKeys, issueKey)
	}

	return issueKeys
}

// normalizeIssueKey приводит ключ к виду, в котором его возвращают трекеры ("proj-1 " -> "PROJ-1").
func normalizeIssueKey(issueKey string) string {
	return strings.ToUpper(strings.TrimSpace(issueKey))
}
