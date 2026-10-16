import "golang.org/x/sync/errgroup"

var sbertrackIssues, jiraSigmaIssues, jiraDeltaIssues map[string]*entity.TrackerIssue

g, gctx := errgroup.WithContext(ctx)
g.Go(func() (err error) {
	sbertrackIssues, err = searchByKey(gctx, a.sbertrack, entity.TrackerSbertrack, issueKeys)
	return err
})
g.Go(func() (err error) {
	jiraSigmaIssues, err = searchByKey(gctx, a.jira, entity.TrackerJiraSigma, issueKeys)
	return err
})
g.Go(func() (err error) {
	jiraDeltaIssues, err = searchByKey(gctx, a.jira, entity.TrackerJiraDelta, issueKeys)
	return err
})
if err := g.Wait(); err != nil {
	return nil, err
}