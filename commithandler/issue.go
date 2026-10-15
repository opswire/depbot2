package entity

import "time"

// Tracker — трекер задач, к которому может относиться задача из коммита.
type Tracker string

const (
	TrackerSbertrack Tracker = "sbertrack"
	TrackerJiraSigma Tracker = "jira_sigma"
	TrackerJiraDelta Tracker = "jira_delta"
)

// GitIssue — задача, упомянутая в коммите.
type GitIssue struct {
	Key        string    // ключ задачи, например "PROJ-123"
	CommitHash string    // хеш коммита, в котором упомянута задача
	CommitDate time.Time // дата коммита
}

// TrackerIssue — задача, которая реально существует в трекере.
type TrackerIssue struct {
	Key       string
	Tracker   Tracker
	CreatedAt time.Time // дата создания задачи в трекере
}

// SearchedIssue — итог поиска: задача из коммита и задача в трекере, к которому она относится.
type SearchedIssue struct {
	GitIssue     GitIssue
	TrackerIssue TrackerIssue
}
