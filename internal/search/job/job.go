package job

import (
	"strings"

	"github.com/google/zoekt"
	"github.com/grafana/regexp"
	"github.com/inconshreveable/log15"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sourcegraph/sourcegraph/internal/authz"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/endpoint"
	"github.com/sourcegraph/sourcegraph/internal/featureflag"
	"github.com/sourcegraph/sourcegraph/internal/gitserver"
	"github.com/sourcegraph/sourcegraph/internal/search"
	"github.com/sourcegraph/sourcegraph/internal/search/commit"
	"github.com/sourcegraph/sourcegraph/internal/search/filter"
	"github.com/sourcegraph/sourcegraph/internal/search/query"
	searchrepos "github.com/sourcegraph/sourcegraph/internal/search/repos"
	"github.com/sourcegraph/sourcegraph/internal/search/result"
	"github.com/sourcegraph/sourcegraph/internal/search/run"
	"github.com/sourcegraph/sourcegraph/internal/search/searchcontexts"
	"github.com/sourcegraph/sourcegraph/internal/search/searcher"
	"github.com/sourcegraph/sourcegraph/internal/search/structural"
	"github.com/sourcegraph/sourcegraph/internal/search/symbol"
	zoektutil "github.com/sourcegraph/sourcegraph/internal/search/zoekt"
	"github.com/sourcegraph/sourcegraph/lib/errors"
	"github.com/sourcegraph/sourcegraph/schema"
)

type Args struct {
	SearchInputs *run.SearchInputs
	Zoekt        zoekt.Streamer
	SearcherURLs *endpoint.Map
}

// ToSearchJob converts a query parse tree to the _internal_ representation
// needed to run a search routine. To understand why this conversion matters, think
// about the fact that the query parse tree doesn't know anything about our
// backends or architecture. It doesn't decide certain defaults, like whether we
// should return multiple result types (pattern matches content, or a file name,
// or a repo name). If we want to optimize a Sourcegraph query parse tree for a
// particular backend (e.g., skip repository resolution and just run a Zoekt
// query on all indexed repositories) then we need to convert our tree to
// Zoekt's internal inputs and representation. These concerns are all handled by
// toSearchJob.
func ToSearchJob(jargs *Args, q query.Q, db database.DB) (Job, error) {
	maxResults := q.MaxResults(jargs.SearchInputs.DefaultLimit())

	b, err := query.ToBasicQuery(q)
	if err != nil {
		return nil, err
	}
	types, _ := q.StringValues(query.FieldType)
	resultTypes := search.ComputeResultTypes(types, b.PatternString(), jargs.SearchInputs.PatternType)

	patternInfo := search.ToTextPatternInfo(b, resultTypes, jargs.SearchInputs.Protocol)
	if patternInfo.Pattern == "" {
		// Fallback to basic search for searching repos and files if
		// the structural search pattern is empty.
		jargs.SearchInputs.PatternType = query.SearchTypeLiteral
		patternInfo.IsStructuralPat = false
	}

	// searcher to use full deadline if timeout: set or we are streaming.
	useFullDeadline := q.Timeout() != nil || q.Count() != nil || jargs.SearchInputs.Protocol == search.Streaming

	features := toFeatures(jargs.SearchInputs.Features)
	repoOptions := toRepoOptions(q, jargs.SearchInputs.UserSettings)
	repoUniverseSearch, skipRepoSubsetSearch, onlyRunSearcher := jobMode(b, resultTypes, jargs.SearchInputs.PatternType, jargs.SearchInputs.OnSourcegraphDotCom)

	var requiredJobs, optionalJobs []Job
	addJob := func(required bool, job Job) {
		if required {
			requiredJobs = append(requiredJobs, job)
		} else {
			optionalJobs = append(optionalJobs, job)
		}
	}

	{
		// This code block creates search jobs under specific
		// conditions, and depending on generic process of `args` above.
		// It which specializes search logic in doResults. In time, all
		// of the above logic should be used to create search jobs
		// across all of Sourcegraph.

		if repoUniverseSearch {
			defaultScope, err := zoektutil.DefaultGlobalQueryScope(repoOptions)
			if err != nil {
				return nil, err
			}
			includePrivate := repoOptions.Visibility == query.Private || repoOptions.Visibility == query.Any

			if resultTypes.Has(result.TypeFile | result.TypePath) {
				typ := search.TextRequest
				zoektQuery, err := search.QueryToZoektQuery(patternInfo, &features, typ)
				if err != nil {
					return nil, err
				}

				globalZoektQuery := zoektutil.NewGlobalZoektQuery(zoektQuery, defaultScope, includePrivate)

				zoektArgs := &search.ZoektParameters{
					// TODO(rvantonder): the Query value is set when the global zoekt query is
					// enriched with private repository data in the search job's Run method, and
					// is therefore set to `nil` below.
					// Ideally, The ZoektParameters type should not expose this field for Universe text
					// searches at all, and will be removed once jobs are fully migrated.
					Query:          nil,
					Typ:            typ,
					FileMatchLimit: patternInfo.FileMatchLimit,
					Select:         patternInfo.Select,
					Zoekt:          jargs.Zoekt,
				}

				addJob(true, &zoektutil.GlobalSearch{
					GlobalZoektQuery: globalZoektQuery,
					ZoektArgs:        zoektArgs,

					RepoOptions: repoOptions,
				})
			}

			if resultTypes.Has(result.TypeSymbol) {
				typ := search.SymbolRequest
				zoektQuery, err := search.QueryToZoektQuery(patternInfo, &features, typ)
				if err != nil {
					return nil, err
				}
				globalZoektQuery := zoektutil.NewGlobalZoektQuery(zoektQuery, defaultScope, includePrivate)

				zoektArgs := &search.ZoektParameters{
					Query:          nil,
					Typ:            typ,
					FileMatchLimit: patternInfo.FileMatchLimit,
					Select:         patternInfo.Select,
					Zoekt:          jargs.Zoekt,
				}

				addJob(true, &symbol.RepoUniverseSymbolSearch{
					GlobalZoektQuery: globalZoektQuery,
					ZoektArgs:        zoektArgs,
					PatternInfo:      patternInfo,
					Limit:            maxResults,

					RepoOptions: repoOptions,
				})
			}
		}

		if resultTypes.Has(result.TypeFile|result.TypePath) && !skipRepoSubsetSearch {
			var textSearchJobs []Job
			typ := search.TextRequest
			if !onlyRunSearcher {
				zoektQuery, err := search.QueryToZoektQuery(patternInfo, &features, typ)
				if err != nil {
					return nil, err
				}
				textSearchJobs = append(textSearchJobs, &zoektutil.ZoektRepoSubsetSearch{
					Query:          zoektQuery,
					Typ:            typ,
					FileMatchLimit: patternInfo.FileMatchLimit,
					Select:         patternInfo.Select,
					Zoekt:          jargs.Zoekt,
				})
			}

			textSearchJobs = append(textSearchJobs, &searcher.Searcher{
				PatternInfo:     patternInfo,
				Indexed:         false,
				SearcherURLs:    jargs.SearcherURLs,
				UseFullDeadline: useFullDeadline,
			})

			addJob(true, &repoPagerJob{
				child:            NewParallelJob(textSearchJobs...),
				repoOptions:      repoOptions,
				useIndex:         patternInfo.Index,
				containsRefGlobs: query.ContainsRefGlobs(q),
				zoekt:            jargs.Zoekt,
			})
		}

		if resultTypes.Has(result.TypeSymbol) && patternInfo.Pattern != "" && !skipRepoSubsetSearch {
			var symbolSearchJobs []Job
			typ := search.SymbolRequest

			if !onlyRunSearcher {
				zoektQuery, err := search.QueryToZoektQuery(patternInfo, &features, typ)
				if err != nil {
					return nil, err
				}
				symbolSearchJobs = append(symbolSearchJobs, &zoektutil.ZoektSymbolSearch{
					Query:          zoektQuery,
					FileMatchLimit: patternInfo.FileMatchLimit,
					Select:         patternInfo.Select,
					Zoekt:          jargs.Zoekt,
				})
			}

			symbolSearchJobs = append(symbolSearchJobs, &searcher.SymbolSearcher{
				PatternInfo: patternInfo,
				Limit:       maxResults,
			})

			required := useFullDeadline || resultTypes.Without(result.TypeSymbol) == 0
			addJob(required, &repoPagerJob{
				child:            NewParallelJob(symbolSearchJobs...),
				repoOptions:      repoOptions,
				useIndex:         patternInfo.Index,
				containsRefGlobs: query.ContainsRefGlobs(q),
				zoekt:            jargs.Zoekt,
			})
		}

		if resultTypes.Has(result.TypeCommit) || resultTypes.Has(result.TypeDiff) {
			diff := resultTypes.Has(result.TypeDiff)
			var required bool
			if useFullDeadline {
				required = true
			} else if diff {
				required = resultTypes.Without(result.TypeDiff) == 0
			} else {
				required = resultTypes.Without(result.TypeCommit) == 0
			}
			addJob(required, &commit.CommitSearch{
				Query:                commit.QueryToGitQuery(q, diff),
				RepoOpts:             repoOptions,
				Diff:                 diff,
				HasTimeFilter:        b.Exists("after") || b.Exists("before"),
				Limit:                int(patternInfo.FileMatchLimit),
				IncludeModifiedFiles: authz.SubRepoEnabled(authz.DefaultSubRepoPermsChecker),
				Gitserver:            gitserver.DefaultClient,
			})
		}

		if jargs.SearchInputs.PatternType == query.SearchTypeStructural && patternInfo.Pattern != "" {
			typ := search.TextRequest
			zoektQuery, err := search.QueryToZoektQuery(patternInfo, &features, typ)
			if err != nil {
				return nil, err
			}
			zoektArgs := &search.ZoektParameters{
				Query:          zoektQuery,
				Typ:            typ,
				FileMatchLimit: patternInfo.FileMatchLimit,
				Select:         patternInfo.Select,
				Zoekt:          jargs.Zoekt,
			}

			searcherArgs := &search.SearcherParameters{
				SearcherURLs:    jargs.SearcherURLs,
				PatternInfo:     patternInfo,
				UseFullDeadline: useFullDeadline,
			}

			addJob(true, &structural.StructuralSearch{
				ZoektArgs:    zoektArgs,
				SearcherArgs: searcherArgs,

				NotSearcherOnly:  !onlyRunSearcher,
				UseIndex:         patternInfo.Index,
				ContainsRefGlobs: query.ContainsRefGlobs(q),
				RepoOpts:         repoOptions,
			})
		}

		if resultTypes.Has(result.TypeRepo) {
			valid := func() bool {
				fieldAllowlist := map[string]struct{}{
					query.FieldRepo:               {},
					query.FieldContext:            {},
					query.FieldType:               {},
					query.FieldDefault:            {},
					query.FieldIndex:              {},
					query.FieldCount:              {},
					query.FieldTimeout:            {},
					query.FieldFork:               {},
					query.FieldArchived:           {},
					query.FieldVisibility:         {},
					query.FieldCase:               {},
					query.FieldRepoHasFile:        {},
					query.FieldRepoHasCommitAfter: {},
					query.FieldPatternType:        {},
					query.FieldSelect:             {},
				}

				// Don't run a repo search if the search contains fields that aren't on the allowlist.
				exists := true
				query.VisitParameter(q, func(field, _ string, _ bool, _ query.Annotation) {
					if _, ok := fieldAllowlist[field]; !ok {
						exists = false
					}
				})
				return exists
			}

			// returns an updated RepoOptions if the pattern part of a query can be used to
			// search repos. A problematic case we check for is when the pattern contains `@`,
			// which may confuse downstream logic to interpret it as part of `repo@rev` syntax.
			addPatternAsRepoFilter := func(pattern string, opts search.RepoOptions) (search.RepoOptions, bool) {
				if pattern == "" {
					return opts, true
				}

				opts.RepoFilters = append(make([]string, 0, len(opts.RepoFilters)), opts.RepoFilters...)
				opts.CaseSensitiveRepoFilters = q.IsCaseSensitive()

				patternPrefix := strings.SplitN(pattern, "@", 2)
				if len(patternPrefix) == 1 {
					// No "@" in pattern? We're good.
					opts.RepoFilters = append(opts.RepoFilters, pattern)
					return opts, true
				}

				if patternPrefix[0] != "" {
					// Extend the repo search using the pattern value, but
					// since the pattern contains @, only search the part
					// prefixed by the first @. This because downstream
					// logic will get confused by the presence of @ and try
					// to resolve repo revisions. See #27816.
					if _, err := regexp.Compile(patternPrefix[0]); err != nil {
						// Prefix is not valid regexp, so just reject it. This can happen for patterns where we've automatically added `(...).*?(...)`
						// such as `foo @bar` which becomes `(foo).*?(@bar)`, which when stripped becomes `(foo).*?(` which is unbalanced and invalid.
						// Why is this a mess? Because validation for everything, including repo values, should be done up front so far possible, not downtsream
						// after possible modifications. By the time we reach this code, the pattern should already have been considered valid to continue with
						// a search. But fixing the order of concerns for repo code is not something @rvantonder is doing today.
						return search.RepoOptions{}, false
					}
					opts.RepoFilters = append(opts.RepoFilters, patternPrefix[0])
					return opts, true
				}

				// This pattern starts with @, of the form "@thing". We can't
				// consistently handle search repos of this form, because
				// downstream logic will attempt to interpret "thing" as a repo
				// revision, may fail, and cause us to raise an alert for any
				// non `type:repo` search. Better to not attempt a repo search.
				return search.RepoOptions{}, false
			}

			if valid() {
				if repoOptions, ok := addPatternAsRepoFilter(patternInfo.Pattern, repoOptions); ok {
					var mode search.GlobalSearchMode
					if repoUniverseSearch {
						mode = search.ZoektGlobalSearch
					}
					if skipRepoSubsetSearch {
						mode = search.SkipUnindexed
					}
					addJob(true, &run.RepoSearch{
						Zoekt:           jargs.Zoekt,
						SearcherURLs:    jargs.SearcherURLs,
						RepoOptions:     repoOptions,
						Features:        features,
						UseFullDeadline: useFullDeadline,
						Query:           q,
						PatternInfo:     patternInfo,
						Mode:            mode,
					})
				}
			}
		}
	}

	addJob(true, &searchrepos.ComputeExcludedRepos{
		Options: repoOptions,
	})

	job := NewPriorityJob(
		NewParallelJob(requiredJobs...),
		NewParallelJob(optionalJobs...),
	)

	checker := authz.DefaultSubRepoPermsChecker
	if authz.SubRepoEnabled(checker) {
		job = NewFilterJob(job)
	}

	return job, nil
}

func toRepoOptions(q query.Q, userSettings *schema.Settings) search.RepoOptions {
	repoFilters, minusRepoFilters := q.Repositories()

	var settingForks, settingArchived bool
	if v := userSettings.SearchIncludeForks; v != nil {
		settingForks = *v
	}
	if v := userSettings.SearchIncludeArchived; v != nil {
		settingArchived = *v
	}

	fork := query.No
	if searchrepos.ExactlyOneRepo(repoFilters) || settingForks {
		// fork defaults to No unless either of:
		// (1) exactly one repo is being searched, or
		// (2) user/org/global setting includes forks
		fork = query.Yes
	}
	if setFork := q.Fork(); setFork != nil {
		fork = *setFork
	}

	archived := query.No
	if searchrepos.ExactlyOneRepo(repoFilters) || settingArchived {
		// archived defaults to No unless either of:
		// (1) exactly one repo is being searched, or
		// (2) user/org/global setting includes archives in all searches
		archived = query.Yes
	}
	if setArchived := q.Archived(); setArchived != nil {
		archived = *setArchived
	}

	visibilityStr, _ := q.StringValue(query.FieldVisibility)
	visibility := query.ParseVisibility(visibilityStr)

	commitAfter, _ := q.StringValue(query.FieldRepoHasCommitAfter)
	searchContextSpec, _ := q.StringValue(query.FieldContext)

	return search.RepoOptions{
		RepoFilters:       repoFilters,
		MinusRepoFilters:  minusRepoFilters,
		Dependencies:      q.Dependencies(),
		SearchContextSpec: searchContextSpec,
		OnlyForks:         fork == query.Only,
		NoForks:           fork == query.No,
		OnlyArchived:      archived == query.Only,
		NoArchived:        archived == query.No,
		Visibility:        visibility,
		CommitAfter:       commitAfter,
		Query:             q,
	}
}

func jobMode(b query.Basic, resultTypes result.Types, st query.SearchType, onSourcegraphDotCom bool) (repoUniverseSearch, skipRepoSubsetSearch, onlyRunSearcher bool) {
	isGlobalSearch := func() bool {
		if st == query.SearchTypeStructural {
			return false
		}

		return query.ForAll(b.ToParseTree(), func(node query.Node) bool {
			n, ok := node.(query.Parameter)
			if !ok {
				return true
			}
			switch n.Field {
			case query.FieldContext:
				return searchcontexts.IsGlobalSearchContextSpec(n.Value)
			case query.FieldRepo:
				// We allow -repo: in global search.
				return n.Negated
			case query.FieldRepoHasFile:
				return false
			default:
				return true
			}
		})
	}

	hasGlobalSearchResultType := resultTypes.Has(result.TypeFile | result.TypePath | result.TypeSymbol)
	isIndexedSearch := b.Index() != query.No
	noPattern := b.PatternString() == ""
	noFile := !b.Exists(query.FieldFile)
	noLang := !b.Exists(query.FieldLang)
	isEmpty := noPattern && noFile && noLang

	repoUniverseSearch = isGlobalSearch() && isIndexedSearch && hasGlobalSearchResultType && !isEmpty
	// skipRepoSubsetSearch is a value that controls whether to
	// run unindexed search in a specific scenario of queries that
	// contain no repo-affecting filters (global mode). When on
	// sourcegraph.com, we resolve only a subset of all indexed
	// repos to search. This control flow implies len(searcherRepos)
	// is always 0, meaning that we should not create jobs to run
	// unindexed searcher.
	skipRepoSubsetSearch = isEmpty || (repoUniverseSearch && onSourcegraphDotCom)
	// onlyRunSearcher is a value that controls whether to run unindexed
	// search if a query triggers repoUniverseSearch. We want to run
	// searcher on unindexed repos when we run a repoUniverseSearch, but
	// only on instances where we are NOT on sourcegraph.com.
	onlyRunSearcher = repoUniverseSearch && !onSourcegraphDotCom

	return repoUniverseSearch, skipRepoSubsetSearch, onlyRunSearcher
}

func toFeatures(flags featureflag.FlagSet) search.Features {
	if flags == nil {
		flags = featureflag.FlagSet{}
		metricFeatureFlagUnavailable.Inc()
		log15.Warn("search feature flags are not available")
	}

	return search.Features{
		ContentBasedLangFilters: flags.GetBoolOr("search-content-based-lang-detection", false),
	}
}

// toAndJob creates a new job from a basic query whose pattern is an And operator at the root.
func toAndJob(args *Args, q query.Basic, db database.DB) (Job, error) {
	// Invariant: this function is only reachable from callers that
	// guarantee a root node with one or more queryOperands.
	queryOperands := q.Pattern.(query.Operator).Operands

	// Limit the number of results from each child to avoid a huge amount of memory bloat.
	// With streaming, we should re-evaluate this number.
	//
	// NOTE: It may be possible to page over repos so that each intersection is only over
	// a small set of repos, limiting massive number of results that would need to be
	// kept in memory otherwise.
	maxTryCount := 40000

	operands := make([]Job, 0, len(queryOperands))
	for _, queryOperand := range queryOperands {
		operand, err := toPatternExpressionJob(args, q.MapPattern(queryOperand), db)
		if err != nil {
			return nil, err
		}
		operands = append(operands, NewLimitJob(maxTryCount, operand))
	}

	return NewAndJob(operands...), nil
}

// toOrJob creates a new job from a basic query whose pattern is an Or operator at the top level
func toOrJob(args *Args, q query.Basic, db database.DB) (Job, error) {
	// Invariant: this function is only reachable from callers that
	// guarantee a root node with one or more queryOperands.
	queryOperands := q.Pattern.(query.Operator).Operands

	operands := make([]Job, 0, len(queryOperands))
	for _, term := range queryOperands {
		operand, err := toPatternExpressionJob(args, q.MapPattern(term), db)
		if err != nil {
			return nil, err
		}
		operands = append(operands, operand)
	}
	return NewOrJob(operands...), nil
}

func toPatternExpressionJob(args *Args, q query.Basic, db database.DB) (Job, error) {
	switch term := q.Pattern.(type) {
	case query.Operator:
		if len(term.Operands) == 0 {
			return NewNoopJob(), nil
		}

		switch term.Kind {
		case query.And:
			return toAndJob(args, q, db)
		case query.Or:
			return toOrJob(args, q, db)
		case query.Concat:
			return ToSearchJob(args, q.ToParseTree(), db)
		}
	case query.Pattern:
		return ToSearchJob(args, q.ToParseTree(), db)
	case query.Parameter:
		// evaluatePatternExpression does not process Parameter nodes.
		return NewNoopJob(), nil
	}
	// Unreachable.
	return nil, errors.Errorf("unrecognized type %T in evaluatePatternExpression", q.Pattern)
}

func ToEvaluateJob(args *Args, q query.Basic, db database.DB) (Job, error) {
	maxResults := q.ToParseTree().MaxResults(args.SearchInputs.DefaultLimit())
	timeout := search.TimeoutDuration(q)

	var (
		job Job
		err error
	)
	if q.Pattern == nil {
		job, err = ToSearchJob(args, query.ToNodes(q.Parameters), db)
	} else {
		job, err = toPatternExpressionJob(args, q, db)
	}
	if err != nil {
		return nil, err
	}

	if v, _ := q.ToParseTree().StringValue(query.FieldSelect); v != "" {
		sp, _ := filter.SelectPathFromString(v) // Invariant: select already validated
		job = NewSelectJob(sp, job)
	}

	return NewTimeoutJob(timeout, NewLimitJob(maxResults, job)), nil
}

// FromExpandedPlan takes a query plan that has had all predicates expanded,
// and converts it to a job.
func FromExpandedPlan(args *Args, plan query.Plan, db database.DB) (Job, error) {
	children := make([]Job, 0, len(plan))
	for _, q := range plan {
		child, err := ToEvaluateJob(args, q, db)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return NewAlertJob(args.SearchInputs, NewOrJob(children...)), nil
}

var metricFeatureFlagUnavailable = promauto.NewCounter(prometheus.CounterOpts{
	Name: "src_search_featureflag_unavailable",
	Help: "temporary counter to check if we have feature flag available in practice.",
})
