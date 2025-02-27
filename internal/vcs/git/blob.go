package git

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/authz"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/trace/ot"
	"github.com/sourcegraph/sourcegraph/internal/vcs/util"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

// ReadFile returns the first maxBytes of the named file at commit. If maxBytes <= 0, the entire
// file is read. (If you just need to check a file's existence, use Stat, not ReadFile.)
func ReadFile(ctx context.Context, db database.DB, repo api.RepoName, commit api.CommitID, name string, maxBytes int64, checker authz.SubRepoPermissionChecker) ([]byte, error) {
	if Mocks.ReadFile != nil {
		return Mocks.ReadFile(commit, name)
	}
	a := actor.FromContext(ctx)
	if hasAccess, err := authz.FilterActorPath(ctx, checker, a, repo, name); err != nil {
		return nil, err
	} else if !hasAccess {
		return nil, os.ErrNotExist
	}

	span, ctx := ot.StartSpanFromContext(ctx, "Git: ReadFile")
	span.SetTag("Name", name)
	defer span.Finish()

	if err := checkSpecArgSafety(string(commit)); err != nil {
		return nil, err
	}

	name = util.Rel(name)
	b, err := readFileBytes(ctx, db, repo, commit, name, maxBytes)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// NewFileReader returns an io.ReadCloser reading from the named file at commit.
// The caller should always close the reader after use
func NewFileReader(ctx context.Context, db database.DB, repo api.RepoName, commit api.CommitID, name string, checker authz.SubRepoPermissionChecker) (io.ReadCloser, error) {
	if Mocks.NewFileReader != nil {
		return Mocks.NewFileReader(commit, name)
	}
	a := actor.FromContext(ctx)
	if hasAccess, err := authz.FilterActorPath(ctx, checker, a, repo, name); err != nil {
		return nil, err
	} else if !hasAccess {
		return nil, os.ErrNotExist
	}

	span, ctx := ot.StartSpanFromContext(ctx, "Git: GetFileReader")
	span.SetTag("Name", name)
	defer span.Finish()

	name = util.Rel(name)
	br, err := newBlobReader(ctx, db, repo, commit, name)
	if err != nil {
		return nil, errors.Wrapf(err, "getting blobReader for %q", name)
	}
	return br, nil
}

func readFileBytes(ctx context.Context, db database.DB, repo api.RepoName, commit api.CommitID, name string, maxBytes int64) ([]byte, error) {
	br, err := newBlobReader(ctx, db, repo, commit, name)
	if err != nil {
		return nil, err
	}
	defer br.Close()

	r := io.Reader(br)
	if maxBytes > 0 {
		r = io.LimitReader(r, maxBytes)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// blobReader, which should be created using newBlobReader, is a struct that allows
// us to get a ReadCloser to a specific named file at a specific commit
type blobReader struct {
	db     database.DB
	ctx    context.Context
	repo   api.RepoName
	commit api.CommitID
	name   string
	cmd    *gitserver.Cmd
	rc     io.ReadCloser
}

func newBlobReader(ctx context.Context, db database.DB, repo api.RepoName, commit api.CommitID, name string) (*blobReader, error) {
	if err := ensureAbsoluteCommit(commit); err != nil {
		return nil, err
	}

	cmd := gitserver.DefaultClient.Command("git", "show", string(commit)+":"+name)
	cmd.Repo = repo
	stdout, err := gitserver.StdoutReader(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return &blobReader{
		db:     db,
		ctx:    ctx,
		repo:   repo,
		commit: commit,
		name:   name,
		cmd:    cmd,
		rc:     stdout,
	}, nil
}

func (br *blobReader) Read(p []byte) (int, error) {
	n, err := br.rc.Read(p)
	if err != nil {
		return n, br.convertError(err)
	}
	return n, nil
}

func (br *blobReader) Close() error {
	return br.rc.Close()
}

// convertError converts an error returned from 'git show' into a more appropriate error type
func (br *blobReader) convertError(err error) error {
	if err == nil {
		return nil
	}
	if err == io.EOF {
		return err
	}
	if strings.Contains(err.Error(), "exists on disk, but not in") || strings.Contains(err.Error(), "does not exist") {
		return &os.PathError{Op: "open", Path: br.name, Err: os.ErrNotExist}
	}
	if strings.Contains(err.Error(), "fatal: bad object ") {
		// Could be a git submodule.
		fi, err := Stat(br.ctx, br.db, authz.DefaultSubRepoPermsChecker, br.repo, br.commit, br.name)
		if err != nil {
			return err
		}
		// Return EOF for a submodule for now which indicates zero content
		if fi.Mode()&ModeSubmodule != 0 {
			return io.EOF
		}
	}
	return errors.WithMessage(err, fmt.Sprintf("git command %v failed (output: %q)", br.cmd.Args, err))
}
