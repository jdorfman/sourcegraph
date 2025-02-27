package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/sourcegraph/sourcegraph/internal/extsvc/auth"
	"github.com/sourcegraph/sourcegraph/internal/httptestutil"
	"github.com/sourcegraph/sourcegraph/internal/rcache"
	"github.com/sourcegraph/sourcegraph/internal/testutil"
)

func TestNewRepoCache(t *testing.T) {
	cmpOpts := cmp.AllowUnexported(rcache.Cache{})
	t.Run("GitHub.com", func(t *testing.T) {
		url, _ := url.Parse("https://www.github.com")
		token := &auth.OAuthBearerToken{Token: "asdf"}

		// github.com caches should:
		// (1) use githubProxyURL for the prefix hash rather than the given url
		// (2) have a TTL of 10 minutes
		prefix := "gh_repo:" + token.Hash()
		got := newRepoCache(url, token)
		want := rcache.NewWithTTL(prefix, 600)
		if diff := cmp.Diff(want, got, cmpOpts); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("GitHub Enterprise", func(t *testing.T) {
		url, _ := url.Parse("https://www.sourcegraph.com")
		token := &auth.OAuthBearerToken{Token: "asdf"}

		// GitHub Enterprise caches should:
		// (1) use the given URL for the prefix hash
		// (2) have a TTL of 30 seconds
		prefix := "gh_repo:" + token.Hash()
		got := newRepoCache(url, token)
		want := rcache.NewWithTTL(prefix, 30)
		if diff := cmp.Diff(want, got, cmpOpts); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestListAffiliatedRepositories(t *testing.T) {
	tests := []struct {
		name         string
		visibility   Visibility
		affiliations []RepositoryAffiliation
		wantRepos    []*Repository
	}{
		{
			name:       "list all repositories",
			visibility: VisibilityAll,
			wantRepos: []*Repository{
				{
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzQxNTE=",
					DatabaseID:       263034151,
					NameWithOwner:    "sourcegraph-vcr-repos/private-org-repo-1",
					URL:              "https://github.com/sourcegraph-vcr-repos/private-org-repo-1",
					IsPrivate:        true,
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzQwNzM=",
					DatabaseID:       263034073,
					NameWithOwner:    "sourcegraph-vcr/private-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/private-user-repo-1",
					IsPrivate:        true,
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzM5NDk=",
					DatabaseID:       263033949,
					NameWithOwner:    "sourcegraph-vcr/public-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/public-user-repo-1",
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzM3NjE=",
					DatabaseID:       263033761,
					NameWithOwner:    "sourcegraph-vcr-repos/public-org-repo-1",
					URL:              "https://github.com/sourcegraph-vcr-repos/public-org-repo-1",
					ViewerPermission: "ADMIN",
				},
			},
		},
		{
			name:       "list public repositories",
			visibility: VisibilityPublic,
			wantRepos: []*Repository{
				{
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzM5NDk=",
					DatabaseID:       263033949,
					NameWithOwner:    "sourcegraph-vcr/public-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/public-user-repo-1",
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzM3NjE=",
					DatabaseID:       263033761,
					NameWithOwner:    "sourcegraph-vcr-repos/public-org-repo-1",
					URL:              "https://github.com/sourcegraph-vcr-repos/public-org-repo-1",
					ViewerPermission: "ADMIN",
				},
			},
		},
		{
			name:       "list private repositories",
			visibility: VisibilityPrivate,
			wantRepos: []*Repository{
				{
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzQxNTE=",
					DatabaseID:       263034151,
					NameWithOwner:    "sourcegraph-vcr-repos/private-org-repo-1",
					URL:              "https://github.com/sourcegraph-vcr-repos/private-org-repo-1",
					IsPrivate:        true,
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzQwNzM=",
					DatabaseID:       263034073,
					NameWithOwner:    "sourcegraph-vcr/private-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/private-user-repo-1",
					IsPrivate:        true,
					ViewerPermission: "ADMIN",
				},
			},
		},
		{
			name:         "list collaborator and owner affiliated repositories",
			affiliations: []RepositoryAffiliation{AffiliationCollaborator, AffiliationOwner},
			wantRepos: []*Repository{
				{
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzQwNzM=",
					DatabaseID:       263034073,
					NameWithOwner:    "sourcegraph-vcr/private-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/private-user-repo-1",
					IsPrivate:        true,
					ViewerPermission: "ADMIN",
				}, {
					ID:               "MDEwOlJlcG9zaXRvcnkyNjMwMzM5NDk=",
					DatabaseID:       263033949,
					NameWithOwner:    "sourcegraph-vcr/public-user-repo-1",
					URL:              "https://github.com/sourcegraph-vcr/public-user-repo-1",
					ViewerPermission: "ADMIN",
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, save := newV3TestClient(t, "ListAffiliatedRepositories_"+test.name)
			defer save()

			repos, _, _, err := client.ListAffiliatedRepositories(context.Background(), test.visibility, 1, test.affiliations...)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(test.wantRepos, repos); diff != "" {
				t.Fatalf("Repos mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_GetAuthenticatedOAuthScopes(t *testing.T) {
	client, save := newV3TestClient(t, "GetAuthenticatedOAuthScopes")
	defer save()

	scopes, err := client.GetAuthenticatedOAuthScopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"admin:enterprise", "admin:gpg_key", "admin:org", "admin:org_hook", "admin:public_key", "admin:repo_hook", "delete:packages", "delete_repo", "gist", "notifications", "repo", "user", "workflow", "write:discussion", "write:packages"}
	sort.Strings(scopes)
	if diff := cmp.Diff(want, scopes); diff != "" {
		t.Fatalf("Scopes mismatch (-want +got):\n%s", diff)
	}
}

// NOTE: To update VCR for this test, please use the token of "sourcegraph-vcr"
// for GITHUB_TOKEN, which can be found in 1Password.
func TestListRepositoryCollaborators(t *testing.T) {
	tests := []struct {
		name        string
		owner       string
		repo        string
		affiliation CollaboratorAffiliation
		wantUsers   []*Collaborator
	}{
		{
			name:  "public repo",
			owner: "sourcegraph-vcr-repos",
			repo:  "public-org-repo-1",
			wantUsers: []*Collaborator{
				{
					ID:         "MDQ6VXNlcjYzMjkwODUx", // sourcegraph-vcr as owner
					DatabaseID: 63290851,
				},
			},
		},
		{
			name:  "private repo",
			owner: "sourcegraph-vcr-repos",
			repo:  "private-org-repo-1",
			wantUsers: []*Collaborator{
				{
					ID:         "MDQ6VXNlcjYzMjkwODUx", // sourcegraph-vcr as owner
					DatabaseID: 63290851,
				}, {
					ID:         "MDQ6VXNlcjY2NDY0Nzcz", // sourcegraph-vcr-amy as team member
					DatabaseID: 66464773,
				}, {
					ID:         "MDQ6VXNlcjY2NDY0OTI2", // sourcegraph-vcr-bob as outside collaborator
					DatabaseID: 66464926,
				}, {
					ID:         "MDQ6VXNlcjg5NDk0ODg0", // sourcegraph-vcr-dave as team member
					DatabaseID: 89494884,
				},
			},
		},
		{
			name:        "direct collaborator outside collaborator",
			owner:       "sourcegraph-vcr-repos",
			repo:        "private-org-repo-1",
			affiliation: AffiliationDirect,
			wantUsers: []*Collaborator{
				{
					ID:         "MDQ6VXNlcjY2NDY0OTI2", // sourcegraph-vcr-bob as outside collaborator
					DatabaseID: 66464926,
				},
			},
		},
		{
			name:        "direct collaborator repo owner",
			owner:       "sourcegraph-vcr",
			repo:        "public-user-repo-1",
			affiliation: AffiliationDirect,
			wantUsers: []*Collaborator{
				{
					ID:         "MDQ6VXNlcjYzMjkwODUx", // sourcegraph-vcr as owner
					DatabaseID: 63290851,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, save := newV3TestClient(t, "ListRepositoryCollaborators_"+test.name)
			defer save()

			users, _, err := client.ListRepositoryCollaborators(context.Background(), test.owner, test.repo, 1, test.affiliation)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(test.wantUsers, users); diff != "" {
				t.Fatalf("Users mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetAuthenticatedUserOrgs(t *testing.T) {
	cli, save := newV3TestClient(t, "GetAuthenticatedUserOrgs")
	defer save()

	ctx := context.Background()
	orgs, err := cli.GetAuthenticatedUserOrgs(ctx)
	if err != nil {
		t.Fatal(err)
	}

	testutil.AssertGolden(t,
		"testdata/golden/GetAuthenticatedUserOrgs",
		update("GetAuthenticatedUserOrgs"),
		orgs,
	)
}

func TestGetAuthenticatedUserOrgDetailsAndMembership(t *testing.T) {
	cli, save := newV3TestClient(t, "GetAuthenticatedUserOrgDetailsAndMembership")
	defer save()

	ctx := context.Background()
	var err error
	orgs := make([]OrgDetailsAndMembership, 0)
	hasNextPage := true
	for page := 1; hasNextPage; page++ {
		var pageOrgs []OrgDetailsAndMembership
		pageOrgs, hasNextPage, _, err = cli.GetAuthenticatedUserOrgsDetailsAndMembership(ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		orgs = append(orgs, pageOrgs...)
	}

	for _, org := range orgs {
		if org.OrgDetails == nil {
			t.Fatal("expected org details, got nil")
		}
		if org.OrgDetails.DefaultRepositoryPermission == "" {
			t.Fatal("expected default repo permissions data")
		}
		if org.OrgMembership == nil {
			t.Fatal("expected org membership, got nil")
		}
		if org.OrgMembership.Role == "" {
			t.Fatal("expected org membership data")
		}
	}

	testutil.AssertGolden(t,
		"testdata/golden/GetAuthenticatedUserOrgDetailsAndMembership",
		update("GetAuthenticatedUserOrgDetailsAndMembership"),
		orgs,
	)
}

func TestListOrgRepositories(t *testing.T) {
	cli, save := newV3TestClient(t, "ListOrgRepositories")
	defer save()

	ctx := context.Background()
	var err error
	repos := make([]*Repository, 0)
	hasNextPage := true
	for page := 1; hasNextPage; page++ {
		var pageRepos []*Repository
		pageRepos, hasNextPage, _, err = cli.ListOrgRepositories(ctx, "sourcegraph-vcr-repos", page, "")
		if err != nil {
			t.Fatal(err)
		}
		repos = append(repos, pageRepos...)
	}

	testutil.AssertGolden(t,
		"testdata/golden/ListOrgRepositories",
		update("ListOrgRepositories"),
		repos,
	)
}

func TestListTeamRepositories(t *testing.T) {
	cli, save := newV3TestClient(t, "ListTeamRepositories")
	defer save()

	ctx := context.Background()
	var err error
	repos := make([]*Repository, 0)
	hasNextPage := true
	for page := 1; hasNextPage; page++ {
		var pageRepos []*Repository
		pageRepos, hasNextPage, _, err = cli.ListTeamRepositories(ctx, "sourcegraph-vcr-repos", "private-access", page)
		if err != nil {
			t.Fatal(err)
		}
		repos = append(repos, pageRepos...)
	}

	testutil.AssertGolden(t,
		"testdata/golden/ListTeamRepositories",
		update("ListTeamRepositories"),
		repos,
	)
}

func TestGetAuthenticatedUserTeams(t *testing.T) {
	cli, save := newV3TestClient(t, "GetAuthenticatedUserTeams")
	defer save()

	ctx := context.Background()
	var err error
	teams := make([]*Team, 0)
	hasNextPage := true
	for page := 1; hasNextPage; page++ {
		var pageTeams []*Team
		pageTeams, hasNextPage, _, err = cli.GetAuthenticatedUserTeams(ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		teams = append(teams, pageTeams...)
	}

	testutil.AssertGolden(t,
		"testdata/golden/GetAuthenticatedUserTeams",
		update("GetAuthenticatedUserTeams"),
		teams,
	)
}

func TestListRepositoryTeams(t *testing.T) {
	cli, save := newV3TestClient(t, "ListRepositoryTeams")
	defer save()

	ctx := context.Background()
	var err error
	teams := make([]*Team, 0)
	hasNextPage := true
	for page := 1; hasNextPage; page++ {
		var pageTeams []*Team
		pageTeams, hasNextPage, err = cli.ListRepositoryTeams(ctx, "sourcegraph-vcr-repos", "private-org-repo-1", page)
		if err != nil {
			t.Fatal(err)
		}
		teams = append(teams, pageTeams...)
	}

	testutil.AssertGolden(t,
		"testdata/golden/ListRepositoryTeams",
		update("ListRepositoryTeams"),
		teams,
	)
}

func TestGetOrganization(t *testing.T) {
	cli, save := newV3TestClient(t, "GetOrganization")
	defer save()

	t.Run("real org", func(t *testing.T) {
		ctx := context.Background()
		org, err := cli.GetOrganization(ctx, "sourcegraph")
		if err != nil {
			t.Fatal(err)
		}
		if org == nil {
			t.Fatal("expected org, got nil")
		}
		if org.Login != "sourcegraph" {
			t.Fatalf("expected org 'sourcegraph', got %+v", org)
		}
	})

	t.Run("actually an user", func(t *testing.T) {
		ctx := context.Background()
		_, err := cli.GetOrganization(ctx, "sourcegraph-vcr")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !IsNotFound(err) {
			t.Fatalf("expected not found, got %q", err.Error())
		}
	})
}

// ListOrganizations is primarily used for GitHub Enterprise clients. As a result we test against
// ghe.sgdev.org.  To update this test, access the GitHub Enterprise Admin Account (ghe.sgdev.org)
// with username milton in 1password. The token used for this test is named sourcegraph-vcr-token
// and is also saved in 1Password under this account.
func TestListOrganizations(t *testing.T) {
	t.Run("enterprise-integration-without-cache", func(t *testing.T) {
		cli, save := newV3TestEnterpriseClient(t, "ListOrganizations")
		defer save()

		// Simplest way to initialise a client with no cache.
		cli.orgsCache = nil

		orgs, hasNextPage, err := cli.ListOrganizations(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}

		if orgs == nil {
			t.Fatal("expected orgs but got nil")
		}

		if len(orgs) != 100 {
			t.Fatalf("expected 100 orgs but got %d", len(orgs))
		}

		if !hasNextPage {
			t.Fatalf("expected hasNextPage to be true but got %v", hasNextPage)
		}
	})

	t.Run("enterprise-integration-with-cache", func(t *testing.T) {
		rcache.SetupForTest(t)
		cli, save := newV3TestEnterpriseClient(t, "ListOrganizations")
		defer save()

		if cli.orgsCache == nil {
			t.Fatal("expected orgsCache to be initialised but is nil")
		}

		hash := cli.auth.Hash()
		expectedEtagKey := hash + "-orgs-etag-1"
		expectedOrgsKey := hash + "-orgs-1"

		// When starting from scratch, the cache should be empty.
		if val, ok := cli.orgsCache.Get(expectedEtagKey); ok {
			t.Fatalf("expected key %q to be empty in cache, but found %s", expectedEtagKey, val)
		}

		if val, ok := cli.orgsCache.Get(expectedOrgsKey); ok {
			t.Fatalf("expected key %q to be empty in cache, but found %s", expectedOrgsKey, val)
		}

		// Make the API call. This should also populate the cache.
		orgs, hasNextPage, err := cli.ListOrganizations(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}

		if orgs == nil {
			t.Fatal("expected orgs but got nil")
		}

		if len(orgs) != 100 {
			t.Fatalf("expected 100 orgs but got %d", len(orgs))
		}

		if !hasNextPage {
			t.Fatalf("expected hasNextPage to be true but got %v", hasNextPage)
		}

		rawEtag, ok := cli.orgsCache.Get(expectedEtagKey)
		if !ok {
			t.Fatalf("expected key %q to be populated in cache, but found empty", expectedEtagKey)
		}

		rawOrgs, ok := cli.orgsCache.Get(expectedOrgsKey)
		if !ok {
			t.Fatalf("expected key %q to be populated in cache, but found empty", expectedOrgsKey)
		}

		expectedOrgs, err := json.Marshal(orgs)
		if err != nil {
			t.Fatal(err)
		}

		// Verify that the value of orgs returned from the call to cli.ListOrganizations is the same
		// as the one stored in the cache.
		if diff := cmp.Diff(expectedOrgs, rawOrgs); diff != "" {
			t.Fatalf("mismatch in cached orgs and orgs returned from API: (-want +got):\n%s", diff)
		}

		// Make another API call. This should read from the cache since the resource has not been
		// modified upstream.
		refetchedOrgs, hasNextPage, err := cli.ListOrganizations(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(orgs, refetchedOrgs); diff != "" {
			t.Fatalf("mismatch in refetched orgs: (-want +got):\n%s", diff)
		}

		if !hasNextPage {
			t.Fatalf("expected hasNextPage to be true but got %v", hasNextPage)
		}

		// We want to verify that for a cached request, the correct header name and its value is
		// used. Using an httptest.NewServer helps us accomplish this. We don't care about
		// replicating the server's response here because we've already tested for that prior to
		// reacing this point.
		//
		// If the testServer exits with the fatal error, this test will panic, which is acceptable.
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get(headerIfNoneMatch); got != string(rawEtag) {
				t.Fatalf("expected request header %q to be set to %q but found %q", headerIfNoneMatch, string(rawEtag), got)
			}

			w.WriteHeader(304)
		}))

		uri, _ := url.Parse(testServer.URL)
		testCli := NewV3Client(uri, gheToken, testServer.Client())
		testCli.ListOrganizations(context.Background(), 1)
	})

	t.Run("enterprise-cache-behaviour", func(t *testing.T) {
		rcache.SetupForTest(t)

		// Marshal a list of orgs.

		// Existing list of orgs.
		mockOldOrgs, err := json.Marshal([]*Org{
			{Login: "foo"},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Updated list of orgs after an initial request is already made, used to verify that the
		// cache was updated correctly.
		mockNewOrgs, err := json.Marshal([]*Org{
			{Login: "bar"},
		})
		if err != nil {
			t.Fatal(err)
		}

		testMockOldOrgs := true
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Pretend like this is a request where the existing resource has not been modified yet.
			if testMockOldOrgs {
				testMockOldOrgs = false
				w.Write(mockOldOrgs)
				if err != nil {
					t.Fatal(err)
				}

				w.WriteHeader(304)
				return
			}

			// Pretend like this is a request where the existing request has been modified and will
			// require a cache invalidation on the client.
			_, err := w.Write(mockNewOrgs)
			if err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(200)
		}))

		uri, _ := url.Parse(testServer.URL)
		testCli := NewV3Client(uri, gheToken, testServer.Client())

		runTest := func(expectedOrgs []byte) {
			orgs, hasNextPage, err := testCli.ListOrganizations(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}

			if !hasNextPage {
				t.Fatalf("expected hasNextPage to be true but got %v", hasNextPage)
			}

			gotOrgs, err := json.Marshal(orgs)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(expectedOrgs, gotOrgs); diff != "" {
				t.Fatalf("mismatch in expected orgs and orgs returned from API: (-want +got):\n%s", diff)
			}

			key := testCli.auth.Hash() + "-orgs-1"
			gotCachedOrgs, ok := testCli.orgsCache.Get(key)
			if !ok {
				t.Fatal(err)
			}

			if diff := cmp.Diff(expectedOrgs, gotCachedOrgs); diff != "" {
				t.Fatalf("mismatch in expected orgs and orgs cached in the client: (-want +got):\n%s", diff)
			}
		}

		// Initial request.
		runTest(mockOldOrgs)

		// New request but with orgs modified.
		runTest(mockNewOrgs)
	})
}

func TestListMembers(t *testing.T) {
	tests := []struct {
		name        string
		fn          func(*V3Client) ([]*Collaborator, error)
		wantMembers []*Collaborator
	}{{
		name: "org members",
		fn: func(cli *V3Client) ([]*Collaborator, error) {
			members, _, err := cli.ListOrganizationMembers(context.Background(), "sourcegraph-vcr-repos", 1, false)
			return members, err
		},
		wantMembers: []*Collaborator{
			{ID: "MDQ6VXNlcjYzMjkwODUx", DatabaseID: 63290851}, // sourcegraph-vcr as owner
			{ID: "MDQ6VXNlcjY2NDY0Nzcz", DatabaseID: 66464773}, // sourcegraph-vcr-amy
			{ID: "MDQ6VXNlcjg5NDk0ODg0", DatabaseID: 89494884}, // sourcegraph-vcr-dave
		},
	}, {
		name: "org admins",
		fn: func(cli *V3Client) ([]*Collaborator, error) {
			members, _, err := cli.ListOrganizationMembers(context.Background(), "sourcegraph-vcr-repos", 1, true)
			return members, err
		},
		wantMembers: []*Collaborator{
			{ID: "MDQ6VXNlcjYzMjkwODUx", DatabaseID: 63290851}, // sourcegraph-vcr as owner
		},
	}, {
		name: "team members",
		fn: func(cli *V3Client) ([]*Collaborator, error) {
			members, _, err := cli.ListTeamMembers(context.Background(), "sourcegraph-vcr-repos", "private-access", 1)
			return members, err
		},
		wantMembers: []*Collaborator{
			{ID: "MDQ6VXNlcjYzMjkwODUx", DatabaseID: 63290851}, // sourcegraph-vcr
			{ID: "MDQ6VXNlcjY2NDY0Nzcz", DatabaseID: 66464773}, // sourcegraph-vcr-amy
		},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cli, save := newV3TestClient(t, t.Name())
			defer save()

			members, err := test.fn(cli)
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(test.wantMembers, members); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestV3Client_WithAuthenticator(t *testing.T) {
	uri, err := url.Parse("https://github.com")
	if err != nil {
		t.Fatal(err)
	}

	old := &V3Client{
		apiURL: uri,
		auth:   &auth.OAuthBearerToken{Token: "old_token"},
	}

	newToken := &auth.OAuthBearerToken{Token: "new_token"}
	new := old.WithAuthenticator(newToken)
	if old == new {
		t.Fatal("both clients have the same address")
	}

	if new.auth != newToken {
		t.Fatalf("token: want %q but got %q", newToken, new.auth)
	}
}

func TestV3Client_Fork(t *testing.T) {
	ctx := context.Background()
	testName := func(t *testing.T) string {
		return strings.ReplaceAll(t.Name(), "/", "_")
	}

	t.Run("success", func(t *testing.T) {
		// For this test, we only need a repository that can be forked into the
		// user's namespace and sourcegraph-testing: it doesn't matter whether it
		// already has been or not because of the way the GitHub API operates.
		// We'll use github.com/sourcegraph/automation-testing as our guinea pig.
		for name, org := range map[string]*string{
			"user":                nil,
			"sourcegraph-testing": strPtr("sourcegraph-testing"),
		} {
			t.Run(name, func(t *testing.T) {
				testName := testName(t)
				client, save := newV3TestClient(t, testName)
				defer save()

				fork, err := client.Fork(ctx, "sourcegraph", "automation-testing", org)
				assert.Nil(t, err)
				assert.NotNil(t, fork)
				if org != nil {
					owner, err := fork.Owner()
					assert.Nil(t, err)
					assert.Equal(t, *org, owner)
				}

				testutil.AssertGolden(t, testName, update(testName), fork)
			})
		}
	})

	t.Run("failure", func(t *testing.T) {
		// For this test, we need a repository that cannot be forked. Conveniently,
		// we have one at github.com/sourcegraph-testing/unforkable.
		testName := testName(t)
		client, save := newV3TestClient(t, testName)
		defer save()

		fork, err := client.Fork(ctx, "sourcegraph-testing", "unforkable", nil)
		assert.NotNil(t, err)
		assert.Nil(t, fork)

		testutil.AssertGolden(t, testName, update(testName), fork)
	})
}

func newV3TestClient(t testing.TB, name string) (*V3Client, func()) {
	t.Helper()

	cf, save := httptestutil.NewGitHubRecorderFactory(t, update(name), name)
	uri, err := url.Parse("https://github.com")
	if err != nil {
		t.Fatal(err)
	}

	doer, err := cf.Doer()
	if err != nil {
		t.Fatal(err)
	}

	return NewV3Client(uri, vcrToken, doer), save
}

func newV3TestEnterpriseClient(t testing.TB, name string) (*V3Client, func()) {
	t.Helper()

	cf, save := httptestutil.NewGitHubRecorderFactory(t, update(name), name)
	uri, err := url.Parse("https://ghe.sgdev.org/api/v3")
	if err != nil {
		t.Fatal(err)
	}

	doer, err := cf.Doer()
	if err != nil {
		t.Fatal(err)
	}

	return NewV3Client(uri, gheToken, doer), save
}

func strPtr(s string) *string { return &s }
