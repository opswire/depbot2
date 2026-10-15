package action

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// GitIssue — задача, найденная в коммите (например, ключ из commit message).
type GitIssue struct {
	Key        string    // ключ задачи, например "PROJ-123"
	CommitHash string    // хеш коммита, в котором упомянута задача
	CommitDate time.Time // дата коммита
}

// SearchedIssue — задача, найденная в одном из трекеров.
type SearchedIssue struct {
	GitIssue  GitIssue  // исходная задача из коммита, по ней сопоставляем результаты
	Tracker   string    // в каком трекере нашли (значение IssueProvider.Type())
	CreatedAt time.Time // дата создания задачи в трекере
}

// IssueProvider — трекер задач, в котором можно искать задачи из коммитов.
// Возвращает только найденные задачи, ненайденные просто отсутствуют в ответе.
type IssueProvider interface {
	SearchIssue(ctx context.Context, issues []GitIssue) ([]SearchedIssue, error)
	Type() string
}

// RelevantIssueSearchInput — входные данные для поиска.
type RelevantIssueSearchInput struct {
	GitIssues []GitIssue
}

// RelevantIssueSearchAction определяет, к какому трекеру относится каждая задача:
// sbertrack, jira sigma или jira delta.
type RelevantIssueSearchAction struct {
	sbertrack IssueProvider
	jiraSigma IssueProvider
	jiraDelta IssueProvider
	logger    *slog.Logger
}

func NewRelevantIssueSearchAction(
	sbertrack IssueProvider,
	jiraSigma IssueProvider,
	jiraDelta IssueProvider,
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
//   - sigma + delta (без sbertrack)   -> выбираем ту, что ближе к дате коммита
//   - нигде не нашлась                -> логируем как потеряшку
//
// Порядок результата совпадает с порядком input.GitIssues (без потеряшек).
func (a *RelevantIssueSearchAction) Execute(ctx context.Context, input RelevantIssueSearchInput) ([]SearchedIssue, error) {
	sbertrackIssues, err := searchByKey(ctx, a.sbertrack, input.GitIssues)
	if err != nil {
		return nil, err
	}

	jiraSigmaIssues, err := searchByKey(ctx, a.jiraSigma, input.GitIssues)
	if err != nil {
		return nil, err
	}

	jiraDeltaIssues, err := searchByKey(ctx, a.jiraDelta, input.GitIssues)
	if err != nil {
		return nil, err
	}

	relevantIssues := make([]SearchedIssue, 0, len(input.GitIssues))
	for _, gitIssue := range input.GitIssues {
		relevantIssue := chooseRelevantIssue(
			gitIssue.CommitDate,
			sbertrackIssues[gitIssue.Key],
			jiraSigmaIssues[gitIssue.Key],
			jiraDeltaIssues[gitIssue.Key],
		)

		if relevantIssue == nil {
			a.logger.WarnContext(ctx, "issue not found in any tracker",
				"issue_key", gitIssue.Key,
				"commit_hash", gitIssue.CommitHash,
			)
			continue
		}

		relevantIssues = append(relevantIssues, *relevantIssue)
	}

	return relevantIssues, nil
}

// chooseRelevantIssue выбирает задачу по правилам из Execute.
// nil на входе — задача в этом трекере не найдена; nil на выходе — не найдена нигде.
func chooseRelevantIssue(commitDate time.Time, sbertrackIssue, jiraSigmaIssue, jiraDeltaIssue *SearchedIssue) *SearchedIssue {
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
func closerToCommit(commitDate time.Time, first, second *SearchedIssue) *SearchedIssue {
	firstDistance := first.CreatedAt.Sub(commitDate).Abs()
	secondDistance := second.CreatedAt.Sub(commitDate).Abs()

	if secondDistance < firstDistance {
		return second
	}
	return first
}

// searchByKey ищет задачи в трекере и раскладывает их по ключу задачи.
// Если трекер вернул несколько результатов на один ключ, берём первый.
func searchByKey(ctx context.Context, provider IssueProvider, gitIssues []GitIssue) (map[string]*SearchedIssue, error) {
	foundIssues, err := provider.SearchIssue(ctx, gitIssues)
	if err != nil {
		return nil, fmt.Errorf("search issues in %s: %w", provider.Type(), err)
	}

	issuesByKey := make(map[string]*SearchedIssue, len(foundIssues))
	for i := range foundIssues {
		key := foundIssues[i].GitIssue.Key
		if _, exists := issuesByKey[key]; !exists {
			issuesByKey[key] = &foundIssues[i]
		}
	}

	return issuesByKey, nil
}
