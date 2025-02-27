package lockfiles

import (
	"context"
	"io"

	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/authz"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/vcs/git"
)

type GitService interface {
	LsFiles(ctx context.Context, repo api.RepoName, commits api.CommitID, paths ...string) ([]string, error)
	Archive(ctx context.Context, repo api.RepoName, opts gitserver.ArchiveOptions) (io.ReadCloser, error)
}

type gitService struct {
	db      database.DB
	checker authz.SubRepoPermissionChecker
}

func NewDefaultGitService(checker authz.SubRepoPermissionChecker, db database.DB) GitService {
	if checker == nil {
		checker = authz.DefaultSubRepoPermsChecker
	}

	return &gitService{
		db:      db,
		checker: checker,
	}
}

func (s *gitService) LsFiles(ctx context.Context, repo api.RepoName, commits api.CommitID, paths ...string) ([]string, error) {
	return git.LsFiles(ctx, s.db, s.checker, repo, commits, paths...)
}

func (s *gitService) Archive(ctx context.Context, repo api.RepoName, opts gitserver.ArchiveOptions) (io.ReadCloser, error) {
	return git.ArchiveReader(ctx, s.db, repo, opts)
}
