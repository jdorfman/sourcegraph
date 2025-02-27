// Package buildkite defines data types that reflect Buildkite's YAML pipeline format.
//
// Usage:
//
//    pipeline := buildkite.Pipeline{}
//    pipeline.AddStep("check_mark", buildkite.Cmd("./dev/check/all.sh"))
package buildkite

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/grafana/regexp"

	"github.com/sourcegraph/sourcegraph/lib/errors"
)

type featureFlags struct {
	// StatelessBuild triggers a stateless build by overriding the default queue to send the build on the stateles
	// agents and forces a MainDryRun type build to avoid impacting normal builds.
	//
	// It is meant to test the stateless builds without any side effects.
	StatelessBuild bool
}

// FeatureFlags are for experimenting with CI pipeline features. Use sparingly!
var FeatureFlags = featureFlags{
	StatelessBuild: os.Getenv("CI_FEATURE_FLAG_STATELESS") == "true" ||
		// Always process retries on stateless agents.
		// TODO: remove when we switch over entirely to stateless agents
		os.Getenv("BUILDKITE_REBUILT_FROM_BUILD_NUMBER") != "" ||
		// Roll out to 75% of builds
		rand.NewSource(time.Now().UnixNano()).Int63()%100 < 10,
}

type Pipeline struct {
	Env    map[string]string `json:"env,omitempty"`
	Steps  []interface{}     `json:"steps"`
	Notify []slackNotifier   `json:"notify,omitempty"`

	// Group, if provided, indicates this Pipeline is actually a group of steps.
	// See: https://buildkite.com/docs/pipelines/group-step
	Group

	// BeforeEveryStepOpts are e.g. commands that are run before every AddStep, similar to
	// Plugins.
	BeforeEveryStepOpts []StepOpt `json:"-"`

	// AfterEveryStepOpts are e.g. that are run at the end of every AddStep, helpful for
	// post-processing
	AfterEveryStepOpts []StepOpt `json:"-"`
}

var nonAlphaNumeric = regexp.MustCompile("[^a-zA-Z0-9]+")

func (p *Pipeline) EnsureUniqueKeys() error {
	occurences := map[string]int{}
	for _, step := range p.Steps {
		if s, ok := step.(*Step); ok {
			if s.Key == "" {
				s.Key = nonAlphaNumeric.ReplaceAllString(s.Label, "")
			}
			occurences[s.Key] += 1
		}
	}
	for k, count := range occurences {
		if count > 1 {
			return errors.Newf("non unique key on step with key %q", k)
		}
	}
	return nil
}

type Group struct {
	Group string `json:"group,omitempty"`
	Key   string `json:"key,omitempty"`
}

type BuildOptions struct {
	Message  string                 `json:"message,omitempty"`
	Commit   string                 `json:"commit,omitempty"`
	Branch   string                 `json:"branch,omitempty"`
	MetaData map[string]interface{} `json:"meta_data,omitempty"`
	Env      map[string]string      `json:"env,omitempty"`
}

func (bo BuildOptions) MarshalJSON() ([]byte, error) {
	type buildOptions BuildOptions
	boCopy := buildOptions(bo)
	// Buildkite pipeline upload command will interpolate if it sees a $var
	// which can cause the pipeline generation to fail because that
	// variable do not exists.
	// By replacing $ into $$ in the commit messages we can prevent those
	// failures to happen.
	//
	// https://buildkite.com/docs/agent/v3/cli-pipeline#environment-variable-substitution
	boCopy.Message = strings.ReplaceAll(boCopy.Message, "$", `$$`)
	return json.Marshal(boCopy)
}

func (bo BuildOptions) MarshalYAML() ([]byte, error) {
	type buildOptions BuildOptions
	boCopy := buildOptions(bo)
	// Buildkite pipeline upload command will interpolate if it sees a $var
	// which can cause the pipeline generation to fail because that
	// variable do not exists.
	// By replacing $ into $$ in the commit messages we can prevent those
	// failures to happen.
	//
	// https://buildkite.com/docs/agent/v3/cli-pipeline#environment-variable-substitution
	boCopy.Message = strings.ReplaceAll(boCopy.Message, "$", `$$`)
	return yaml.Marshal(boCopy)
}

// Matches Buildkite pipeline JSON schema:
// https://github.com/buildkite/pipeline-schema/blob/master/schema.json
type Step struct {
	Label                  string                   `json:"label"`
	Key                    string                   `json:"key,omitempty"`
	Command                []string                 `json:"command,omitempty"`
	DependsOn              []string                 `json:"depends_on,omitempty"`
	AllowDependencyFailure bool                     `json:"allow_dependency_failure,omitempty"`
	TimeoutInMinutes       string                   `json:"timeout_in_minutes,omitempty"`
	Trigger                string                   `json:"trigger,omitempty"`
	Async                  bool                     `json:"async,omitempty"`
	Build                  *BuildOptions            `json:"build,omitempty"`
	Env                    map[string]string        `json:"env,omitempty"`
	Plugins                []map[string]interface{} `json:"plugins,omitempty"`
	ArtifactPaths          string                   `json:"artifact_paths,omitempty"`
	ConcurrencyGroup       string                   `json:"concurrency_group,omitempty"`
	Concurrency            int                      `json:"concurrency,omitempty"`
	Parallelism            int                      `json:"parallelism,omitempty"`
	Skip                   string                   `json:"skip,omitempty"`
	SoftFail               []softFailExitStatus     `json:"soft_fail,omitempty"`
	Retry                  *RetryOptions            `json:"retry,omitempty"`
	Agents                 map[string]string        `json:"agents,omitempty"`
	If                     string                   `json:"if,omitempty"`
}

type RetryOptions struct {
	Automatic *AutomaticRetryOptions `json:"automatic,omitempty"`
	Manual    *ManualRetryOptions    `json:"manual,omitempty"`
}

type AutomaticRetryOptions struct {
	Limit int `json:"limit,omitempty"`
}

type ManualRetryOptions struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func (p *Pipeline) AddStep(label string, opts ...StepOpt) {
	step := &Step{
		Label:   label,
		Env:     make(map[string]string),
		Agents:  make(map[string]string),
		Plugins: make([]map[string]interface{}, 0),
	}
	for _, opt := range p.BeforeEveryStepOpts {
		opt(step)
	}
	for _, opt := range opts {
		opt(step)
	}
	for _, opt := range p.AfterEveryStepOpts {
		opt(step)
	}

	p.Steps = append(p.Steps, step)
}

func (p *Pipeline) AddTrigger(label string, pipeline string, opts ...StepOpt) {
	step := &Step{
		Label:   label,
		Trigger: pipeline,
	}
	for _, opt := range opts {
		opt(step)
	}
	p.Steps = append(p.Steps, step)
}

type slackNotifier struct {
	Slack slackChannelsNotification `json:"slack"`
	If    string                    `json:"if"`
}

type slackChannelsNotification struct {
	Channels []string `json:"channels"`
	Message  string   `json:"message"`
}

// AddFailureSlackNotify configures a notify block that updates the given channel if the
// build fails.
func (p *Pipeline) AddFailureSlackNotify(channel string, mentionUserID string, err error) {
	n := slackChannelsNotification{
		Channels: []string{channel},
	}

	if mentionUserID != "" {
		n.Message = fmt.Sprintf("cc <@%s>", mentionUserID)
	} else if err != nil {
		n.Message = err.Error()
	}
	p.Notify = append(p.Notify, slackNotifier{
		Slack: n,
		If:    `build.state == "failed"`,
	})
}

func (p *Pipeline) WriteJSONTo(w io.Writer) (int64, error) {
	output, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return 0, err
	}
	n, err := w.Write(output)
	return int64(n), err
}

func (p *Pipeline) WriteYAMLTo(w io.Writer) (int64, error) {
	output, err := yaml.Marshal(p)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(output)
	return int64(n), err
}

type StepOpt func(step *Step)

// RawCmd adds a command step without any instrumentation. This is useful to
// test the instrumentation itself.
func RawCmd(command string) StepOpt {
	return func(step *Step) {
		step.Command = append(step.Command, command)
	}
}

func tracedCmd(command string) string {
	// ./tr is a symbolic link created by the .buildkite/hooks/post-checkout hook.
	// Its purpose is to keep the command excerpt in the buildkite UI clear enough to
	// see the underlying command even if prefixed by the tracing script.
	return fmt.Sprintf("./tr %s", command)
}

// Cmd adds a command step with added instrumentation for testing purposes.
func Cmd(command string) StepOpt {
	return RawCmd(tracedCmd(command))
}

type AnnotationType string

const (
	// We opt not to allow 'success' and 'info' type annotations for now to encourage
	// steps to only provide annotations that help debug failure cases. In the future
	// we can revisit this if there is a need.
	// AnnotationTypeSuccess AnnotationType = "success"
	// AnnotationTypeInfo    AnnotationType = "info"
	AnnotationTypeWarning AnnotationType = "warning"
	AnnotationTypeError   AnnotationType = "error"
)

type AnnotationOpts struct {
	// Type indicates the type annotations from this command should be uploaded as.
	// Commands that upload annotations of different levels will create separate
	// annotations.
	//
	// If no annotation type is provided, the annotation is created as an error annotation.
	Type AnnotationType

	// IncludeNames indicates whether the file names of found annotations should be
	// included in the Buildkite annotation as section titles. For example, if enabled the
	// contents of the following files:
	//
	//  - './annotations/Job log.md'
	//  - './annotations/shfmt'
	//
	// Will be included in the annotation with section titles 'Job log' and 'shfmt'.
	IncludeNames bool

	// MultiJobContext indicates that this annotation will accept input from multiple jobs
	// under this context name.
	MultiJobContext string
}

type TestReportOpts struct {
	// TestSuiteKeyVariableName is the name of the variable in gcloud secrets that holds
	// the test suite key to upload to.
	//
	// TODO: This is not finalized, see https://github.com/sourcegraph/sourcegraph/issues/31971
	TestSuiteKeyVariableName string
}

// AnnotatedCmdOpts declares options for AnnotatedCmd.
type AnnotatedCmdOpts struct {
	// AnnotationOpts configures how AnnotatedCmd picks up files left in the
	// `./annotations` directory and appends them to a shared annotation for this job.
	// If nil, AnnotatedCmd will not look for annotations.
	//
	// To get started, generate an annotation file when you want to publish an annotation,
	// typically on error, in the './annotations' directory:
	//
	//	if [ $EXIT_CODE -ne 0 ]; then
	//		echo -e "$OUT" >./annotations/shfmt
	//		echo "^^^ +++"
	//	fi
	//
	// Make sure it has a sufficiently unique name, so as to avoid conflicts if multiple
	// annotations are generated in a single job.
	//
	// Annotations can be formatted based on file extensions, for example:
	//
	// - './annotations/Job log.md' will have its contents appended as markdown
	// - './annotations/shfmt' will have its contents formatted as terminal output
	//
	// Please be considerate about what generating annotations, since they can cause a lot
	// of visual clutter in the Buildkite UI. When creating annotations:
	//
	// - keep them concise and short, to minimze the space they take up
	// - ensure they are actionable: an annotation should enable you, the CI user, to
	//    know where to go and what to do next.
	//
	// DO NOT use 'buildkite-agent annotate' or 'annotate.sh' directly in scripts.
	Annotations *AnnotationOpts

	// TestReports configures how AnnotatedCmd picks up files left in the `./test-reports`
	// directory and uploads them to Buildkite Analytics. If nil, AnnotatedCmd will not
	// look for test reports.
	//
	// To get started, generate a JUnit XML report for your tests in the './test-reports'
	// directory. Make sure it has a sufficiently unique name, so as to avoid conflicts if
	// multiple reports are generated in a single job. Consult your language's test
	// tooling for more details.
	//
	// Use TestReportOpts to configure where to publish reports too. For more details,
	// see https://buildkite.com/organizations/sourcegraph/analytics.
	//
	// DO NOT post directly to the Buildkite API or use 'upload-test-report.sh' directly
	// in scripts.
	TestReports *TestReportOpts
}

// AnnotatedCmd runs the given command and picks up annotations generated by the command:
//
// - annotations in `./annotations`
// - test reports in `./test-reports`
//
// To learn more, see the AnnotatedCmdOpts docstrings.
func AnnotatedCmd(command string, opts AnnotatedCmdOpts) StepOpt {
	// Options for annotations
	var annotateOpts string
	if opts.Annotations != nil {
		if opts.Annotations.Type == "" {
			annotateOpts += fmt.Sprintf(" -t %s", AnnotationTypeError)
		} else {
			annotateOpts += fmt.Sprintf(" -t %s", opts.Annotations.Type)
		}
		if opts.Annotations.MultiJobContext != "" {
			annotateOpts += fmt.Sprintf(" -c %q", opts.Annotations.MultiJobContext)
		}
		annotateOpts = fmt.Sprintf("%v %s", opts.Annotations.IncludeNames, strings.TrimSpace(annotateOpts))
	}

	// Options for test reports
	var testReportOpts string
	if opts.TestReports != nil {
		testReportOpts += opts.TestReports.TestSuiteKeyVariableName
	}

	// ./an is a symbolic link created by the .buildkite/hooks/post-checkout hook.
	// Its purpose is to keep the command excerpt in the buildkite UI clear enough to
	// see the underlying command even if prefixed by the annotation scraper.
	annotatedCmd := fmt.Sprintf("./an %q", tracedCmd(command))
	return flattenStepOpts(RawCmd(annotatedCmd),
		Env("ANNOTATE_OPTS", annotateOpts),
		Env("TEST_REPORT_OPTS", testReportOpts))
}

func Async(async bool) StepOpt {
	return func(step *Step) {
		step.Async = async
	}
}

func Build(buildOptions BuildOptions) StepOpt {
	return func(step *Step) {
		step.Build = &buildOptions
	}
}

func ConcurrencyGroup(group string) StepOpt {
	return func(step *Step) {
		step.ConcurrencyGroup = group
	}
}

func Concurrency(limit int) StepOpt {
	return func(step *Step) {
		step.Concurrency = limit
	}
}

// Parallelism tells Buildkite to run this job multiple time in parallel,
// which is very useful to QA a flake fix.
func Parallelism(count int) StepOpt {
	return func(step *Step) {
		step.Parallelism = count
	}
}

func Env(name, value string) StepOpt {
	return func(step *Step) {
		step.Env[name] = value
	}
}

func Skip(reason string) StepOpt {
	return func(step *Step) {
		step.Skip = reason
	}
}

type softFailExitStatus struct {
	ExitStatus int `json:"exit_status"`
}

// SoftFail indicates the specified exit codes should trigger a soft fail.
// https://buildkite.com/docs/pipelines/command-step#command-step-attributes
// This function also adds a specific env var named SOFT_FAIL_EXIT_CODES, enabling
// to get exit codes from the scripts until https://github.com/sourcegraph/sourcegraph/issues/27264
// is fixed.
func SoftFail(exitCodes ...int) StepOpt {
	return func(step *Step) {
		var codes []string
		for _, code := range exitCodes {
			codes = append(codes, strconv.Itoa(code))
			step.SoftFail = append(step.SoftFail, softFailExitStatus{
				ExitStatus: code,
			})
		}
		// https://github.com/sourcegraph/sourcegraph/issues/27264
		step.Env["SOFT_FAIL_EXIT_CODES"] = strings.Join(codes, " ")
	}
}

// AutomaticRetry enables automatic retry for the step with the number of times this job can be retried.
// The maximum value this can be set to is 10.
// Docs: https://buildkite.com/docs/pipelines/command-step#automatic-retry-attributes
func AutomaticRetry(limit int) StepOpt {
	return func(step *Step) {
		step.Retry = &RetryOptions{
			Automatic: &AutomaticRetryOptions{
				Limit: limit,
			},
		}
	}
}

// DisableManualRetry disables manual retry for the step. The reason string passed
// will be displayed in a tooltip on the Retry button in the Buildkite interface.
// Docs: https://buildkite.com/docs/pipelines/command-step#manual-retry-attributes
func DisableManualRetry(reason string) StepOpt {
	return func(step *Step) {
		step.Retry = &RetryOptions{
			Manual: &ManualRetryOptions{
				Allowed: false,
				Reason:  reason,
			},
		}
	}
}

func ArtifactPaths(paths ...string) StepOpt {
	return func(step *Step) {
		step.ArtifactPaths = strings.Join(paths, ";")
	}
}

func Agent(key, value string) StepOpt {
	return func(step *Step) {
		step.Agents[key] = value
	}
}

func (p *Pipeline) AddWait() {
	p.Steps = append(p.Steps, "wait")
}

func Key(key string) StepOpt {
	return func(step *Step) {
		step.Key = key
	}
}

func Plugin(name string, plugin interface{}) StepOpt {
	return func(step *Step) {
		wrapper := map[string]interface{}{}
		wrapper[name] = plugin
		step.Plugins = append(step.Plugins, wrapper)
	}
}

func DependsOn(dependency ...string) StepOpt {
	return func(step *Step) {
		step.DependsOn = append(step.DependsOn, dependency...)
	}
}

// IfReadyForReview causes this step to only be added if this build is associated with a
// pull request that is also ready for review.
func IfReadyForReview() StepOpt {
	return func(step *Step) {
		step.If = "build.pull_request.id != null && !build.pull_request.draft"
	}
}

// AllowDependencyFailure enables `allow_dependency_failure` attribute on the step.
// Such a step will run when the depended-on jobs complete, fail or even did not run.
// See extended docs here: https://buildkite.com/docs/pipelines/dependencies#allowing-dependency-failures
func AllowDependencyFailure() StepOpt {
	return func(step *Step) {
		step.AllowDependencyFailure = true
	}
}

// flattenStepOpts conveniently turns a list of StepOpt into a single StepOpt.
// It is useful to build helpers that can then be used when defining operations,
// when the helper wraps multiple stepOpts at once.
func flattenStepOpts(stepOpts ...StepOpt) StepOpt {
	return func(step *Step) {
		for _, stepOpt := range stepOpts {
			stepOpt(step)
		}
	}
}
