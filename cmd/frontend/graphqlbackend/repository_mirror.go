package graphqlbackend

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/graph-gophers/graphql-go"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/backend"
	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/gitserver/protocol"
	"github.com/sourcegraph/sourcegraph/internal/lazyregexp"
	"github.com/sourcegraph/sourcegraph/internal/repoupdater"
	repoupdaterprotocol "github.com/sourcegraph/sourcegraph/internal/repoupdater/protocol"
	"github.com/sourcegraph/sourcegraph/internal/types"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

func (r *RepositoryResolver) MirrorInfo() *repositoryMirrorInfoResolver {
	return &repositoryMirrorInfoResolver{repository: r, db: r.db}
}

type repositoryMirrorInfoResolver struct {
	repository *RepositoryResolver
	db         database.DB

	// memoize the repo-updater RepoUpdateSchedulerInfo call
	repoUpdateSchedulerInfoOnce   sync.Once
	repoUpdateSchedulerInfoResult *repoupdaterprotocol.RepoUpdateSchedulerInfoResult
	repoUpdateSchedulerInfoErr    error

	// memoize the gitserver RepoInfo call
	repoInfoOnce     sync.Once
	repoInfoResponse *protocol.RepoInfo
	repoInfoErr      error
}

func (r *repositoryMirrorInfoResolver) gitserverRepoInfo(ctx context.Context) (*protocol.RepoInfo, error) {
	r.repoInfoOnce.Do(func() {
		resp, err := gitserver.DefaultClient.RepoInfo(ctx, r.repository.RepoName())
		r.repoInfoResponse, r.repoInfoErr = resp.Results[r.repository.RepoName()], err
	})
	return r.repoInfoResponse, r.repoInfoErr
}

func (r *repositoryMirrorInfoResolver) repoUpdateSchedulerInfo(ctx context.Context) (*repoupdaterprotocol.RepoUpdateSchedulerInfoResult, error) {
	r.repoUpdateSchedulerInfoOnce.Do(func() {
		args := repoupdaterprotocol.RepoUpdateSchedulerInfoArgs{
			RepoName: r.repository.RepoName(),
			ID:       r.repository.IDInt32(),
		}
		r.repoUpdateSchedulerInfoResult, r.repoUpdateSchedulerInfoErr = repoupdater.DefaultClient.RepoUpdateSchedulerInfo(ctx, args)
	})
	return r.repoUpdateSchedulerInfoResult, r.repoUpdateSchedulerInfoErr
}

// TODO(flying-robot): this regex and the majority of the removeUserInfo function can
// be extracted to a common location in a subsequent change.
var nonSCPURLRegex = lazyregexp.New(`^(git\+)?(https?|ssh|rsync|file|git|perforce)://`)

func (r *repositoryMirrorInfoResolver) RemoteURL(ctx context.Context) (string, error) {
	// 🚨 SECURITY: The remote URL might contain secret credentials in the URL userinfo, so
	// only allow site admins to see it.
	if err := backend.CheckCurrentUserIsSiteAdmin(ctx, r.db); err != nil {
		return "", err
	}

	// removeUserinfo strips the userinfo component of a remote URL. The provided string s
	// will be returned if it cannot be parsed as a URL.
	removeUserinfo := func(s string) string {
		// Support common syntax (HTTPS, SSH, etc.)
		if nonSCPURLRegex.MatchString(s) {
			u, err := url.Parse(s)
			if err != nil {
				return s
			}
			u.User = nil
			return u.String()
		}

		// Support SCP-style syntax.
		u, err := url.Parse("fake://" + strings.Replace(s, ":", "/", 1))
		if err != nil {
			return s
		}
		u.User = nil
		return strings.Replace(strings.Replace(u.String(), "fake://", "", 1), "/", ":", 1)
	}

	{
		repo, err := r.repository.repo(ctx)
		if err != nil {
			return "", err
		}

		if cloneURLs := repo.CloneURLs(); len(cloneURLs) > 0 {
			return removeUserinfo(cloneURLs[0]), nil
		}
	}

	// Fall back to the gitserver repo info for repos on hosts that are not yet fully supported by repo-updater.
	info, err := r.gitserverRepoInfo(ctx)
	if err != nil {
		return "", err
	}
	return removeUserinfo(info.URL), nil
}

func (r *repositoryMirrorInfoResolver) Cloned(ctx context.Context) (bool, error) {
	info, err := r.gitserverRepoInfo(ctx)
	if err != nil {
		return false, err
	}
	return info.Cloned, nil
}

func (r *repositoryMirrorInfoResolver) CloneInProgress(ctx context.Context) (bool, error) {
	info, err := r.gitserverRepoInfo(ctx)
	if err != nil {
		return false, err
	}
	return info.CloneInProgress, nil
}

func (r *repositoryMirrorInfoResolver) CloneProgress(ctx context.Context) (*string, error) {
	info, err := r.gitserverRepoInfo(ctx)
	if err != nil {
		return nil, err
	}
	return strptr(info.CloneProgress), nil
}

func (r *repositoryMirrorInfoResolver) UpdatedAt(ctx context.Context) (*DateTime, error) {
	info, err := r.gitserverRepoInfo(ctx)
	if err != nil {
		return nil, err
	}
	return DateTimeOrNil(info.LastFetched), nil
}

func (r *repositoryMirrorInfoResolver) UpdateSchedule(ctx context.Context) (*updateScheduleResolver, error) {
	info, err := r.repoUpdateSchedulerInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info.Schedule == nil {
		return nil, nil
	}
	return &updateScheduleResolver{schedule: info.Schedule}, nil
}

type updateScheduleResolver struct {
	schedule *repoupdaterprotocol.RepoScheduleState
}

func (r *updateScheduleResolver) IntervalSeconds() int32 {
	return int32(r.schedule.IntervalSeconds)
}

func (r *updateScheduleResolver) Due() DateTime {
	return DateTime{Time: r.schedule.Due}
}

func (r *updateScheduleResolver) Index() int32 {
	return int32(r.schedule.Index)
}

func (r *updateScheduleResolver) Total() int32 {
	return int32(r.schedule.Total)
}

func (r *repositoryMirrorInfoResolver) UpdateQueue(ctx context.Context) (*updateQueueResolver, error) {
	info, err := r.repoUpdateSchedulerInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info.Queue == nil {
		return nil, nil
	}
	return &updateQueueResolver{queue: info.Queue}, nil
}

type updateQueueResolver struct {
	queue *repoupdaterprotocol.RepoQueueState
}

func (r *updateQueueResolver) Updating() bool {
	return r.queue.Updating
}

func (r *updateQueueResolver) Index() int32 {
	return int32(r.queue.Index)
}

func (r *updateQueueResolver) Total() int32 {
	return int32(r.queue.Total)
}

func (r *schemaResolver) CheckMirrorRepositoryConnection(ctx context.Context, args *struct {
	Repository *graphql.ID
	Name       *string
}) (*checkMirrorRepositoryConnectionResult, error) {
	// 🚨 SECURITY: This is an expensive operation and the errors may contain secrets,
	// so only site admins may run it.
	if err := backend.CheckCurrentUserIsSiteAdmin(ctx, r.db); err != nil {
		return nil, err
	}

	if (args.Repository != nil && args.Name != nil) || (args.Repository == nil && args.Name == nil) {
		return nil, errors.New("exactly one of the repository and name arguments must be set")
	}

	var repo *types.Repo
	switch {
	case args.Repository != nil:
		repoID, err := UnmarshalRepositoryID(*args.Repository)
		if err != nil {
			return nil, err
		}
		repo, err = backend.NewRepos(r.db).Get(ctx, repoID)
		if err != nil {
			return nil, err
		}
	case args.Name != nil:
		// Use just the name to look up the repository from gitserver.
		repo = &types.Repo{Name: api.RepoName(*args.Name)}
	}

	var result checkMirrorRepositoryConnectionResult
	if err := gitserver.DefaultClient.IsRepoCloneable(ctx, repo.Name); err != nil {
		result.errorMessage = err.Error()
	}
	return &result, nil
}

type checkMirrorRepositoryConnectionResult struct {
	errorMessage string
}

func (r *checkMirrorRepositoryConnectionResult) Error() *string {
	if r.errorMessage == "" {
		return nil
	}
	return &r.errorMessage
}

func (r *schemaResolver) UpdateMirrorRepository(ctx context.Context, args *struct {
	Repository graphql.ID
}) (*EmptyResponse, error) {
	// 🚨 SECURITY: There is no reason why non-site-admins would need to run this operation.
	if err := backend.CheckCurrentUserIsSiteAdmin(ctx, r.db); err != nil {
		return nil, err
	}

	repo, err := r.repositoryByID(ctx, args.Repository)
	if err != nil {
		return nil, err
	}

	if _, err := repoupdater.DefaultClient.EnqueueRepoUpdate(ctx, repo.RepoName()); err != nil {
		return nil, err
	}
	return &EmptyResponse{}, nil
}
