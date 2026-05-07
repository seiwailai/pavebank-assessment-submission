package uat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

var (
	godogOptions = &godog.Options{}

	uatTagsFlag           = flag.String("uat-tags", "", "tag expression for selecting UAT scenarios")
	uatFeaturesFlag       = flag.String("uat-features", "", "comma-separated UAT feature paths")
	uatFormatFlag         = flag.String("uat-format", "", "godog output format")
	uatBaseURLFlag        = flag.String("uat-base-url", "", "base URL for the running UAT target")
	uatManageRuntimeFlag  = flag.Bool("uat-manage-runtime", false, "start and stop an isolated local runtime for UAT")
	uatEnabledFlag        = flag.Bool("uat-enabled", false, "enable UAT execution; UAT is opt-in in broad test runs")
	uatAppPortFlag        = flag.Int("uat-app-port", 4400, "port for the isolated Encore app runtime")
	uatTemporalPortFlag   = flag.Int("uat-temporal-port", 8233, "host port for the isolated Temporal container")
	uatNamespaceFlag      = flag.String("uat-namespace", "", "Encore namespace for isolated UAT infrastructure")
	uatStartupTimeoutFlag = flag.Duration("uat-startup-timeout", 2*time.Minute, "startup timeout for isolated UAT runtime")
	uatTemporalImageFlag  = flag.String("uat-temporal-image", "temporalio/auto-setup:1.27.2", "Temporal Docker image for isolated UAT runtime")
	uatResetDBFlag        = flag.Bool("uat-reset-db", true, "reset fees database before and after UAT execution")
	uatScenarioPauseFlag  = flag.Duration("uat-scenario-pause", 3*time.Second, "pause before each scenario starts")
	uatRequestPauseFlag   = flag.Duration("uat-request-pause", time.Second, "pause between requests within a scenario after the first request")
	uatHTTPTimeoutFlag    = flag.Duration("uat-http-timeout", 45*time.Second, "HTTP client timeout for UAT API calls")

	activeRuntime *managedRuntime
	uatEnabled    bool
)

func init() {
	godog.BindCommandLineFlags("godog.", godogOptions)
}

func TestMain(m *testing.M) {
	flag.Parse()
	applyOptionDefaults()
	uatEnabled = shouldRunUAT()

	if !uatEnabled {
		os.Exit(m.Run())
	}

	var cleanup func()
	var err error

	if shouldManageRuntime() {
		fmt.Fprintf(os.Stderr, "UAT: starting managed runtime (namespace=%s, app_port=%d, temporal_port=%d)\n", effectiveNamespace(), *uatAppPortFlag, *uatTemporalPortFlag)
		activeRuntime, err = startManagedRuntime()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to start managed UAT runtime: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "UAT: managed runtime ready at %s\n", activeRuntime.BaseURL())
		cleanup = func() {
			if stopErr := activeRuntime.Stop(); stopErr != nil {
				fmt.Fprintf(os.Stderr, "failed to stop managed UAT runtime cleanly: %v\n", stopErr)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "UAT: using existing runtime at %s\n", currentBaseURL())
	}

	if shouldResetDB() {
		if err := resetFeesDatabase(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to reset fees database before UAT: %v\n", err)
			if cleanup != nil {
				cleanup()
			}
			os.Exit(1)
		}
	}

	exitCode := m.Run()

	if shouldResetDB() {
		if err := resetFeesDatabase(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to reset fees database after UAT: %v\n", err)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}

	if cleanup != nil {
		cleanup()
	}

	os.Exit(exitCode)
}

func TestUAT(t *testing.T) {
	t.Helper()
	if !uatEnabled {
		t.Skip("UAT skipped by default in broad test runs. Run UAT explicitly with -run TestUAT, or set UAT_ENABLED=1 / -uat-enabled.")
	}

	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options:             godogOptions,
	}

	if suite.Run() != 0 {
		t.Fail()
	}
}

func applyOptionDefaults() {
	if *uatFormatFlag != "" {
		godogOptions.Format = *uatFormatFlag
	}
	if godogOptions.Format == "" {
		godogOptions.Format = getenv("UAT_FORMAT", "pretty")
	}
	if *uatFeaturesFlag != "" {
		godogOptions.Paths = resolvePaths(*uatFeaturesFlag)
	}
	if len(godogOptions.Paths) == 0 {
		godogOptions.Paths = resolvePaths(getenv("UAT_FEATURES", "features"))
	} else {
		godogOptions.Paths = resolvePaths(strings.Join(godogOptions.Paths, ","))
	}
	if *uatTagsFlag != "" {
		godogOptions.Tags = *uatTagsFlag
	}
	if godogOptions.Tags == "" {
		godogOptions.Tags = getenv("UAT_TAGS", "~@manual")
	}
	if !godogOptions.StopOnFailure {
		godogOptions.StopOnFailure = getenv("UAT_STOP_ON_FAILURE", "") == "1"
	}
}

func shouldManageRuntime() bool {
	if *uatManageRuntimeFlag {
		return true
	}
	return getenv("UAT_MANAGE_RUNTIME", "") == "1"
}

func shouldResetDB() bool {
	raw := strings.TrimSpace(os.Getenv("UAT_RESET_DB"))
	if raw == "" {
		return *uatResetDBFlag
	}

	switch strings.ToLower(raw) {
	case "0", "false", "no":
		return false
	case "1", "true", "yes":
		return true
	default:
		return *uatResetDBFlag
	}
}

func scenarioPause() time.Duration {
	raw := strings.TrimSpace(os.Getenv("UAT_SCENARIO_PAUSE"))
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
	}
	if *uatScenarioPauseFlag < 0 {
		return 0
	}
	return *uatScenarioPauseFlag
}

func requestPause() time.Duration {
	raw := strings.TrimSpace(os.Getenv("UAT_REQUEST_PAUSE"))
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
	}
	if *uatRequestPauseFlag < 0 {
		return 0
	}
	return *uatRequestPauseFlag
}

func requestTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("UAT_HTTP_TIMEOUT"))
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	if *uatHTTPTimeoutFlag <= 0 {
		return 45 * time.Second
	}
	return *uatHTTPTimeoutFlag
}

func resetFeesDatabase() error {
	cmd := exec.Command("encore", "db", "reset", "fees")
	cmd.Dir = repoRoot()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("encore db reset fees failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Before(func(context.Context, *godog.Scenario) (context.Context, error) {
		if pause := scenarioPause(); pause > 0 {
			time.Sleep(pause)
		}
		s := &session{
			baseURL: currentBaseURL(),
			client: &http.Client{
				Timeout: requestTimeout(),
			},
		}
		s.reset()
		return withSession(context.Background(), s), nil
	})

	ctx.Step(`^I remember "([^"]*)" as "([^"]*)"$`, func(stepCtx context.Context, name, value string) error {
		return mustSession(stepCtx).rememberValue(name, value)
	})
	ctx.Step(`^I remember "([^"]*)" as UUID "([^"]*)"$`, func(stepCtx context.Context, name, value string) error {
		return mustSession(stepCtx).rememberUUID(name, value)
	})
	ctx.Step(`^I remember "([^"]*)" as a unique idempotency key$`, func(stepCtx context.Context, name string) error {
		return mustSession(stepCtx).rememberIdempotencyKey(name)
	})
	ctx.Step(`^I remember "([^"]*)" as timestamp "([^"]*)"$`, func(stepCtx context.Context, name, expr string) error {
		return mustSession(stepCtx).rememberTimestamp(name, expr)
	})
	ctx.Step(`^I set header "([^"]*)" to "([^"]*)"$`, func(stepCtx context.Context, key, value string) error {
		return mustSession(stepCtx).setHeader(key, value)
	})
	ctx.Step(`^I set header "([^"]*)" from variable "([^"]*)"$`, func(stepCtx context.Context, key, varName string) error {
		return mustSession(stepCtx).setHeaderFromVariable(key, varName)
	})
	ctx.Step(`^I clear header "([^"]*)"$`, func(stepCtx context.Context, key string) error {
		return mustSession(stepCtx).clearHeader(key)
	})
	ctx.Step(`^I set the request body to JSON:$`, func(stepCtx context.Context, doc *godog.DocString) error {
		return mustSession(stepCtx).setRequestBody(doc)
	})
	ctx.Step(`^I wait (\d+) seconds$`, func(stepCtx context.Context, seconds int) error {
		time.Sleep(time.Duration(seconds) * time.Second)
		return nil
	})
	ctx.Step(`^I send a "([^"]*)" request to "([^"]*)"$`, func(stepCtx context.Context, method, path string) error {
		return mustSession(stepCtx).sendRequest(method, path)
	})
	ctx.Step(`^the response status should be (\d+)$`, func(stepCtx context.Context, expected int) error {
		return mustSession(stepCtx).responseStatusShouldBe(expected)
	})
	ctx.Step(`^the response body should contain "([^"]*)"$`, func(stepCtx context.Context, fragment string) error {
		return mustSession(stepCtx).responseBodyShouldContain(fragment)
	})
	ctx.Step(`^the response JSON field "([^"]*)" should equal "([^"]*)"$`, func(stepCtx context.Context, path, expected string) error {
		return mustSession(stepCtx).responseFieldShouldEqual(path, expected)
	})
	ctx.Step(`^the response JSON field "([^"]*)" should equal variable "([^"]*)"$`, func(stepCtx context.Context, path, varName string) error {
		return mustSession(stepCtx).responseFieldShouldEqualVariable(path, varName)
	})
	ctx.Step(`^the response JSON field "([^"]*)" should equal the number (\d+)$`, func(stepCtx context.Context, path string, expected int) error {
		return mustSession(stepCtx).responseFieldShouldEqualNumber(path, expected)
	})
	ctx.Step(`^the response JSON field "([^"]*)" should not be empty$`, func(stepCtx context.Context, path string) error {
		return mustSession(stepCtx).responseFieldShouldNotBeEmpty(path)
	})
	ctx.Step(`^the response JSON field "([^"]*)" should be absent$`, func(stepCtx context.Context, path string) error {
		return mustSession(stepCtx).responseFieldShouldBeAbsent(path)
	})
	ctx.Step(`^I store the response JSON field "([^"]*)" as "([^"]*)"$`, func(stepCtx context.Context, path, name string) error {
		return mustSession(stepCtx).storeResponseField(path, name)
	})
	ctx.Step(`^the response JSON array "([^"]*)" should have length (\d+)$`, func(stepCtx context.Context, path string, expected int) error {
		return mustSession(stepCtx).responseArrayShouldHaveLength(path, expected)
	})
	ctx.Step(`^within (\d+) seconds polling "([^"]*)" "([^"]*)" every (\d+) seconds the response JSON field "([^"]*)" should equal "([^"]*)"$`, func(stepCtx context.Context, withinSeconds int, method, path string, everySeconds int, field, expected string) error {
		return mustSession(stepCtx).eventuallyFieldShouldEqual(withinSeconds, method, path, everySeconds, field, expected)
	})
	ctx.Step(`^a bill in OPEN state with at least one persisted line item$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).setupOpenBillWithOneLineItem()
	})
	ctx.Step(`^I trigger close and poll GET /v1/bills/:billID at sub-second intervals$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).triggerCloseAndPollBillStatusSubSecond()
	})
	ctx.Step(`^I should observe bill_status FINALIZING at least once before CLOSED$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).assertObservedFinalizingBeforeClosed()
	})
	ctx.Step(`^a bill that is OPEN and within a few seconds of submission deadline$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).setupOpenBillNearDeadline()
	})
	ctx.Step(`^I submit N concurrent add-line-items requests with distinct idempotency keys$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).submitConcurrentAddsAroundDeadline()
	})
	ctx.Step(`^requests accepted before cutoff succeed and requests after cutoff are rejected$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).assertConcurrentAddOutcomes()
	})
	ctx.Step(`^no duplicate line item is persisted$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).assertNoDuplicatePersistedLineItems()
	})
	ctx.Step(`^a bill in OPEN state and in-flight add-line-items requests$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).setupOpenBillForCloseDuringIngestion()
	})
	ctx.Step(`^close is requested while ingestion is still in progress$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).requestCloseDuringIngestion()
	})
	ctx.Step(`^late add-line-items requests are rejected with lifecycle state conflict$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).assertLateAddsRejected()
	})
	ctx.Step(`^final total equals only accepted line items$`, func(stepCtx context.Context) error {
		return mustSession(stepCtx).assertFinalTotalMatchesAcceptedAdds()
	})
}

func shouldRunUAT() bool {
	if *uatEnabledFlag || isTruthy(os.Getenv("UAT_ENABLED")) {
		return true
	}
	if *uatManageRuntimeFlag || strings.TrimSpace(*uatBaseURLFlag) != "" {
		return true
	}
	if isTruthy(os.Getenv("UAT_MANAGE_RUNTIME")) || strings.TrimSpace(os.Getenv("UAT_BASE_URL")) != "" {
		return true
	}
	return testRunFlagTargetsUAT()
}

func testRunFlagTargetsUAT() bool {
	for i := 0; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "-test.run=") {
			return strings.Contains(arg[len("-test.run="):], "TestUAT")
		}
		if arg == "-test.run" && i+1 < len(os.Args) {
			return strings.Contains(os.Args[i+1], "TestUAT")
		}
	}
	return false
}

func isTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func effectiveNamespace() string {
	namespace := strings.TrimSpace(*uatNamespaceFlag)
	if namespace != "" {
		return namespace
	}
	return "uat-auto"
}

type sessionContextKey struct{}

func withSession(ctx context.Context, s *session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, s)
}

func mustSession(ctx context.Context) *session {
	s, ok := ctx.Value(sessionContextKey{}).(*session)
	if !ok || s == nil {
		panic("missing scenario session in context")
	}
	return s
}

type session struct {
	baseURL        string
	client         *http.Client
	headers        map[string]string
	vars           map[string]string
	requestBody    string
	lastResponse   *http.Response
	lastBody       []byte
	lastJSON       any
	sessionStarted time.Time
	requestCount   int
	statusTrace    []string
	acceptedAdds   int
	rejectedAdds   int
	acceptedTotal  int64
}

func (s *session) reset() {
	s.headers = map[string]string{}
	s.vars = map[string]string{}
	s.requestBody = ""
	s.lastResponse = nil
	s.lastBody = nil
	s.lastJSON = nil
	s.sessionStarted = time.Now().UTC()
	s.requestCount = 0
	s.statusTrace = nil
	s.acceptedAdds = 0
	s.rejectedAdds = 0
	s.acceptedTotal = 0
}

func (s *session) rememberValue(name, value string) error {
	s.vars[name] = value
	return nil
}

func (s *session) rememberUUID(name, value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return fmt.Errorf("invalid UUID %q: %w", value, err)
	}
	s.vars[name] = value
	return nil
}

func (s *session) rememberIdempotencyKey(name string) error {
	s.vars[name] = fmt.Sprintf("%s-%s", name, uuid.NewString())
	return nil
}

func (s *session) rememberTimestamp(name, expr string) error {
	ts, err := s.resolveTimeExpression(expr)
	if err != nil {
		return err
	}
	s.vars[name] = ts.Format(time.RFC3339)
	return nil
}

func (s *session) setHeader(key, value string) error {
	s.headers[key] = s.interpolate(value)
	return nil
}

func (s *session) setHeaderFromVariable(key, varName string) error {
	value, ok := s.vars[varName]
	if !ok {
		return fmt.Errorf("variable %q is not defined", varName)
	}
	s.headers[key] = value
	return nil
}

func (s *session) clearHeader(key string) error {
	delete(s.headers, key)
	return nil
}

func (s *session) setRequestBody(doc *godog.DocString) error {
	if doc == nil {
		return fmt.Errorf("request body docstring is required")
	}
	s.requestBody = s.interpolate(doc.Content)
	return nil
}

func (s *session) sendRequest(method, path string) error {
	if s.requestCount > 0 {
		if pause := requestPause(); pause > 0 {
			time.Sleep(pause)
		}
	}

	if s.lastResponse != nil && s.lastResponse.Body != nil {
		_ = s.lastResponse.Body.Close()
	}

	fullURL := s.baseURL + s.interpolate(path)
	var body io.Reader
	if s.requestBody != "" {
		body = bytes.NewBufferString(s.requestBody)
	}

	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return err
	}
	for key, value := range s.headers {
		req.Header.Set(key, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}

	//nolint:errcheck
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	s.lastResponse = resp
	s.lastBody = rawBody
	s.lastJSON = nil
	if len(bytes.TrimSpace(rawBody)) > 0 {
		var decoded any
		if err := json.Unmarshal(rawBody, &decoded); err == nil {
			s.lastJSON = decoded
		}
	}
	s.requestCount++
	return nil
}

func (s *session) responseStatusShouldBe(expected int) error {
	if s.lastResponse == nil {
		return fmt.Errorf("no response available")
	}
	if s.lastResponse.StatusCode != expected {
		return fmt.Errorf("expected status %d, got %d, body=%s", expected, s.lastResponse.StatusCode, string(s.lastBody))
	}
	return nil
}

func (s *session) responseBodyShouldContain(fragment string) error {
	if !strings.Contains(string(s.lastBody), fragment) {
		return fmt.Errorf("expected response body to contain %q, got %s", fragment, string(s.lastBody))
	}
	return nil
}

func (s *session) responseFieldShouldEqual(path, expected string) error {
	got, err := s.lookup(path)
	if err != nil {
		return err
	}
	if fmt.Sprint(got) != s.interpolate(expected) {
		return fmt.Errorf("expected %s=%q, got %v", path, expected, got)
	}
	return nil
}

func (s *session) responseFieldShouldEqualVariable(path, varName string) error {
	expected, ok := s.vars[varName]
	if !ok {
		return fmt.Errorf("variable %q is not defined", varName)
	}
	return s.responseFieldShouldEqual(path, expected)
}

func (s *session) responseFieldShouldEqualNumber(path string, expected int) error {
	got, err := s.lookup(path)
	if err != nil {
		return err
	}

	switch value := got.(type) {
	case float64:
		if int(value) != expected {
			return fmt.Errorf("expected %s=%d, got %v", path, expected, value)
		}
	case int:
		if value != expected {
			return fmt.Errorf("expected %s=%d, got %v", path, expected, value)
		}
	default:
		if fmt.Sprint(got) != strconv.Itoa(expected) {
			return fmt.Errorf("expected %s=%d, got %v", path, expected, got)
		}
	}
	return nil
}

func (s *session) responseFieldShouldNotBeEmpty(path string) error {
	got, err := s.lookup(path)
	if err != nil {
		return err
	}
	if fmt.Sprint(got) == "" || fmt.Sprint(got) == "<nil>" {
		return fmt.Errorf("expected %s to be non-empty", path)
	}
	return nil
}

func (s *session) responseFieldShouldBeAbsent(path string) error {
	_, err := s.lookup(path)
	if err == nil {
		return fmt.Errorf("expected %s to be absent, but it was present", path)
	}
	return nil
}

func (s *session) storeResponseField(path, name string) error {
	got, err := s.lookup(path)
	if err != nil {
		return err
	}
	s.vars[name] = fmt.Sprint(got)
	return nil
}

func (s *session) responseArrayShouldHaveLength(path string, expected int) error {
	got, err := s.lookup(path)
	if err != nil {
		return err
	}
	items, ok := got.([]any)
	if !ok {
		return fmt.Errorf("field %s is not an array", path)
	}
	if len(items) != expected {
		return fmt.Errorf("expected %s length %d, got %d", path, expected, len(items))
	}
	return nil
}

func (s *session) eventuallyFieldShouldEqual(withinSeconds int, method, path string, everySeconds int, field, expected string) error {
	deadline := time.Now().Add(time.Duration(withinSeconds) * time.Second)
	interval := time.Duration(everySeconds) * time.Second
	if interval <= 0 {
		interval = time.Second
	}

	var lastErr error
	for time.Now().Before(deadline) {
		if err := s.sendRequest(method, path); err != nil {
			lastErr = err
		} else if err := s.responseFieldShouldEqual(field, expected); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("condition not met before timeout")
	}
	return lastErr
}

func (s *session) lookup(path string) (any, error) {
	if s.lastJSON == nil {
		return nil, fmt.Errorf("response is not JSON: %s", string(s.lastBody))
	}

	current := s.lastJSON
	for _, segment := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field %s not found in %s", segment, path)
		}
		value, exists := obj[segment]
		if !exists {
			return nil, fmt.Errorf("field %s not found in %s", segment, path)
		}
		current = value
	}
	return current, nil
}

func (s *session) interpolate(input string) string {
	re := regexp.MustCompile(`\{\{([a-zA-Z0-9_\-]+)\}\}`)
	return re.ReplaceAllStringFunc(input, func(match string) string {
		submatches := re.FindStringSubmatch(match)
		if len(submatches) != 2 {
			return match
		}
		if value, ok := s.vars[submatches[1]]; ok {
			return value
		}
		return match
	})
}

func (s *session) resolveTimeExpression(expr string) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "now" {
		return time.Now().UTC(), nil
	}

	pattern := regexp.MustCompile(`^now([+-])(\d+)([smhd])$`)
	matches := pattern.FindStringSubmatch(expr)
	if len(matches) != 4 {
		return time.Time{}, fmt.Errorf("unsupported time expression %q", expr)
	}

	amount, err := strconv.Atoi(matches[2])
	if err != nil {
		return time.Time{}, err
	}

	var duration time.Duration
	switch matches[3] {
	case "s":
		duration = time.Duration(amount) * time.Second
	case "m":
		duration = time.Duration(amount) * time.Minute
	case "h":
		duration = time.Duration(amount) * time.Hour
	case "d":
		duration = time.Duration(amount) * 24 * time.Hour
	default:
		return time.Time{}, fmt.Errorf("unsupported time unit %q", matches[3])
	}

	now := time.Now().UTC()
	if matches[1] == "-" {
		return now.Add(-duration), nil
	}
	return now.Add(duration), nil
}

func currentBaseURL() string {
	if *uatBaseURLFlag != "" {
		return *uatBaseURLFlag
	}
	if activeRuntime != nil {
		return activeRuntime.BaseURL()
	}
	return getenv("UAT_BASE_URL", "http://127.0.0.1:4000")
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func splitPaths(raw string) []string {
	parts := strings.Split(raw, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}

func resolvePaths(raw string) []string {
	_, currentFile, _, ok := runtime.Caller(0)
	packageDir := "."
	if ok {
		packageDir = filepath.Dir(currentFile)
	}

	resolved := make([]string, 0, len(splitPaths(raw)))
	for _, part := range splitPaths(raw) {
		if pathExists(part) {
			resolved = append(resolved, part)
			continue
		}

		packageCandidate := filepath.Join(packageDir, part)
		if pathExists(packageCandidate) {
			resolved = append(resolved, packageCandidate)
			continue
		}

		resolved = append(resolved, part)
	}
	return resolved
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *session) setupOpenBillWithOneLineItem() error {
	unique := uuid.NewString()
	startAt := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	endAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	deadline := time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339)

	if err := s.setHeader("Content-Type", "application/json"); err != nil {
		return err
	}
	if err := s.setHeader("Idempotency-Key", "manual-create-"+unique); err != nil {
		return err
	}
	createBody := fmt.Sprintf(`{
  "account_id":"acct-manual-%s",
  "external_reference_id":"bill-manual-%s",
  "currency_code":"USD",
  "billing_period_start_at":"%s",
  "billing_period_end_at":"%s",
  "line_items_submission_deadline":"%s"
}`, unique, unique, startAt, endAt, deadline)
	if err := s.setRequestBody(&godog.DocString{Content: createBody}); err != nil {
		return err
	}
	if err := s.sendRequest("POST", "/v1/bills"); err != nil {
		return err
	}
	if err := s.responseStatusShouldBe(201); err != nil {
		return err
	}
	if err := s.storeResponseField("bill_id", "manual_bill_id"); err != nil {
		return err
	}

	if err := s.setHeader("Idempotency-Key", "manual-add-"+unique); err != nil {
		return err
	}
	addBody := fmt.Sprintf(`{
  "currency_code":"USD",
  "line_items":[
    {
      "external_reference_id":"line-manual-%s",
      "occurred_at":"%s",
      "amount_minor":100
    }
  ]
}`, unique, startAt)
	if err := s.setRequestBody(&godog.DocString{Content: addBody}); err != nil {
		return err
	}
	if err := s.sendRequest("POST", "/v1/bills/{{manual_bill_id}}/line-items"); err != nil {
		return err
	}
	return s.responseStatusShouldBe(200)
}

func (s *session) triggerCloseAndPollBillStatusSubSecond() error {
	unique := uuid.NewString()
	if err := s.setHeader("Idempotency-Key", "manual-close-"+unique); err != nil {
		return err
	}
	if err := s.setRequestBody(&godog.DocString{Content: `{}`}); err != nil {
		return err
	}
	if err := s.sendRequest("POST", "/v1/bills/{{manual_bill_id}}/close"); err != nil {
		return err
	}
	if err := s.responseStatusShouldBe(202); err != nil {
		return err
	}

	s.statusTrace = s.statusTrace[:0]
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.sendRequest("GET", "/v1/bills/{{manual_bill_id}}"); err != nil {
			return err
		}
		if s.lastResponse.StatusCode != 200 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		value, err := s.lookup("bill_status")
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		status := fmt.Sprint(value)
		s.statusTrace = append(s.statusTrace, status)
		if status == "CLOSED" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("did not reach CLOSED during sub-second polling window")
}

func (s *session) assertObservedFinalizingBeforeClosed() error {
	finalizingIndex := -1
	closedIndex := -1
	for i, status := range s.statusTrace {
		if status == "FINALIZING" && finalizingIndex == -1 {
			finalizingIndex = i
		}
		if status == "CLOSED" && closedIndex == -1 {
			closedIndex = i
		}
	}
	if finalizingIndex == -1 {
		return fmt.Errorf("FINALIZING was not observed; trace=%v", s.statusTrace)
	}
	if closedIndex == -1 {
		return fmt.Errorf("CLOSED was not observed; trace=%v", s.statusTrace)
	}
	if finalizingIndex > closedIndex {
		return fmt.Errorf("FINALIZING observed after CLOSED; trace=%v", s.statusTrace)
	}
	return nil
}

func (s *session) setupOpenBillNearDeadline() error {
	unique := uuid.NewString()
	startAt := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	endAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	deadlineAt := time.Now().UTC().Add(6 * time.Second).Format(time.RFC3339)
	s.vars["manual_deadline_at"] = deadlineAt

	if err := s.setHeader("Content-Type", "application/json"); err != nil {
		return err
	}
	if err := s.setHeader("Idempotency-Key", "manual-near-deadline-create-"+unique); err != nil {
		return err
	}
	body := fmt.Sprintf(`{
  "account_id":"acct-near-deadline-%s",
  "external_reference_id":"bill-near-deadline-%s",
  "currency_code":"USD",
  "billing_period_start_at":"%s",
  "billing_period_end_at":"%s",
  "line_items_submission_deadline":"%s"
}`, unique, unique, startAt, endAt, deadlineAt)
	if err := s.setRequestBody(&godog.DocString{Content: body}); err != nil {
		return err
	}
	if err := s.sendRequest("POST", "/v1/bills"); err != nil {
		return err
	}
	if err := s.responseStatusShouldBe(201); err != nil {
		return err
	}
	return s.storeResponseField("bill_id", "manual_bill_id")
}

func (s *session) submitConcurrentAddsAroundDeadline() error {
	deadlineRaw, ok := s.vars["manual_deadline_at"]
	if !ok {
		return errors.New("manual_deadline_at not set")
	}
	deadlineAt, err := time.Parse(time.RFC3339, deadlineRaw)
	if err != nil {
		return err
	}

	type addResult struct {
		status int
		amount int64
	}

	sendAdd := func(refSuffix string, amount int64) (addResult, error) {
		idem := "manual-concurrent-add-" + uuid.NewString()
		body := fmt.Sprintf(`{
  "currency_code":"USD",
  "line_items":[
    {
      "external_reference_id":"line-concurrent-%s",
      "occurred_at":"%s",
      "amount_minor":%d
    }
  ]
}`, refSuffix, time.Now().UTC().Format(time.RFC3339), amount)

		req, reqErr := http.NewRequest("POST", s.baseURL+s.interpolate("/v1/bills/{{manual_bill_id}}/line-items"), bytes.NewBufferString(body))
		if reqErr != nil {
			return addResult{}, reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idem)
		resp, doErr := s.client.Do(req)
		if doErr != nil {
			return addResult{}, doErr
		}

		//nolint:errcheck
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return addResult{status: resp.StatusCode, amount: amount}, nil
	}

	results := make([]addResult, 0, 8)
	var early sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		early.Add(1)
		go func(i int) {
			defer early.Done()
			r, callErr := sendAdd(fmt.Sprintf("early-%d", i), 100)
			if callErr != nil {
				return
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(i)
	}
	early.Wait()

	sleep := time.Until(deadlineAt.Add(500 * time.Millisecond))
	if sleep > 0 {
		time.Sleep(sleep)
	}

	var late sync.WaitGroup
	for i := 0; i < 4; i++ {
		late.Add(1)
		go func(i int) {
			defer late.Done()
			r, callErr := sendAdd(fmt.Sprintf("late-%d", i), 100)
			if callErr != nil {
				return
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(i)
	}
	late.Wait()

	s.acceptedAdds = 0
	s.rejectedAdds = 0
	s.acceptedTotal = 0
	for _, r := range results {
		if r.status == 200 {
			s.acceptedAdds++
			s.acceptedTotal += r.amount
			continue
		}
		if r.status >= 400 {
			s.rejectedAdds++
		}
	}
	return nil
}

func (s *session) assertConcurrentAddOutcomes() error {
	if s.acceptedAdds == 0 {
		return fmt.Errorf("expected at least one accepted add; accepted=%d rejected=%d", s.acceptedAdds, s.rejectedAdds)
	}
	if s.rejectedAdds == 0 {
		return fmt.Errorf("expected at least one rejected add; accepted=%d rejected=%d", s.acceptedAdds, s.rejectedAdds)
	}
	return nil
}

func (s *session) assertNoDuplicatePersistedLineItems() error {
	if err := s.sendRequest("GET", "/v1/bills/{{manual_bill_id}}/line-items"); err != nil {
		return err
	}
	if err := s.responseStatusShouldBe(200); err != nil {
		return err
	}
	value, err := s.lookup("items")
	if err != nil {
		return err
	}
	items, ok := value.([]any)
	if !ok {
		return errors.New("items is not an array")
	}
	if len(items) != s.acceptedAdds {
		return fmt.Errorf("persisted items=%d, accepted adds=%d", len(items), s.acceptedAdds)
	}
	return nil
}

func (s *session) setupOpenBillForCloseDuringIngestion() error {
	return s.setupOpenBillNearDeadline()
}

func (s *session) requestCloseDuringIngestion() error {
	type addResult struct {
		status int
		amount int64
	}
	results := make(chan addResult, 8)
	sendAdd := func(ref string, delay time.Duration) {
		time.Sleep(delay)
		idem := "manual-ingest-add-" + uuid.NewString()
		body := fmt.Sprintf(`{
  "currency_code":"USD",
  "line_items":[
    {
      "external_reference_id":"%s",
      "occurred_at":"%s",
      "amount_minor":100
    }
  ]
}`, ref, time.Now().UTC().Format(time.RFC3339))
		req, err := http.NewRequest("POST", s.baseURL+s.interpolate("/v1/bills/{{manual_bill_id}}/line-items"), bytes.NewBufferString(body))
		if err != nil {
			results <- addResult{status: 0}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idem)
		resp, err := s.client.Do(req)
		if err != nil {
			results <- addResult{status: 0}
			return
		}
		//nolint:errcheck
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		results <- addResult{status: resp.StatusCode, amount: 100}
	}

	go sendAdd("line-ingest-1", 0)
	go sendAdd("line-ingest-2", 200*time.Millisecond)
	go sendAdd("line-ingest-3", 2*time.Second)
	go sendAdd("line-ingest-4", 3*time.Second)

	time.Sleep(300 * time.Millisecond)
	if err := s.closeManualBill(); err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := s.sendRequest("GET", "/v1/bills/{{manual_bill_id}}"); err != nil {
			return err
		}
		if err := s.responseStatusShouldBe(200); err != nil {
			return err
		}
		v, err := s.lookup("bill_status")
		if err == nil && fmt.Sprint(v) == "CLOSED" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	s.acceptedAdds = 0
	s.rejectedAdds = 0
	s.acceptedTotal = 0
	for i := 0; i < 4; i++ {
		r := <-results
		if r.status == 200 {
			s.acceptedAdds++
			s.acceptedTotal += r.amount
		}
		if r.status >= 400 {
			s.rejectedAdds++
		}
	}
	return nil
}

func (s *session) assertLateAddsRejected() error {
	if s.rejectedAdds == 0 {
		return fmt.Errorf("expected at least one rejected add while/after closing, got accepted=%d rejected=%d", s.acceptedAdds, s.rejectedAdds)
	}
	return nil
}

func (s *session) assertFinalTotalMatchesAcceptedAdds() error {
	if err := s.sendRequest("GET", "/v1/bills/{{manual_bill_id}}"); err != nil {
		return err
	}
	if err := s.responseStatusShouldBe(200); err != nil {
		return err
	}
	if err := s.responseFieldShouldEqual("bill_status", "CLOSED"); err != nil {
		return err
	}
	got, err := s.lookup("final_total_amount_minor")
	if err != nil {
		return err
	}
	total := int64(0)
	switch v := got.(type) {
	case float64:
		total = int64(v)
	case int64:
		total = v
	default:
		return fmt.Errorf("unexpected final_total_amount_minor type %T", got)
	}
	if total != s.acceptedTotal {
		return fmt.Errorf("final_total_amount_minor=%d, accepted_total=%d", total, s.acceptedTotal)
	}
	return nil
}

func (s *session) closeManualBill() error {
	if err := s.setHeader("Content-Type", "application/json"); err != nil {
		return err
	}
	if err := s.setHeader("Idempotency-Key", "manual-close-"+uuid.NewString()); err != nil {
		return err
	}
	if err := s.setRequestBody(&godog.DocString{Content: `{}`}); err != nil {
		return err
	}
	if err := s.sendRequest("POST", "/v1/bills/{{manual_bill_id}}/close"); err != nil {
		return err
	}
	return s.responseStatusShouldBe(202)
}
