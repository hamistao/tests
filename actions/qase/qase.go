package qase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/sirupsen/logrus"
)

type TestSuiteSchema struct {
	Projects []string                  `json:"projects,omitempty" yaml:"projects,omitempty"`
	Suite    string                    `json:"suite,omitempty" yaml:"suite,omitempty"`
	Cases    []upstream.TestCaseCreate `json:"cases,omitempty" yaml:"cases,omitempty"`
}

type Service struct {
	Client *upstream.APIClient
}

const (
	schemas        = "schemas.yaml"
	failStatus     = "failed"
	skippedStatus  = "skipped"
	requestLimit   = 100
	runSourceID    = 16
	recurringRunID = 1
)

// SetupQaseClient creates a new Qase client from the api token environment variable QASE_AUTOMATION_TOKEN
func SetupQaseClient() *Service {
	cfg := upstream.NewConfiguration()
	cfg.AddDefaultHeader("Token", os.Getenv(QaseTokenEnvVar))
	return &Service{
		Client: upstream.NewAPIClient(cfg),
	}
}

// GetTestSuite retrieves a Test Suite by name within a specified Qase Project if it exists
func (q *Service) GetTestSuite(project, suite string, parentID upstream.NullableInt64) (*upstream.Suite, error) {
	logrus.Debugf("Getting test suite \"%s\" in project %s\n", suite, project)

	var numOfSuites int32 = 1
	var offSetCount int32 = 0
	suiteRequest := q.Client.SuitesAPI.GetSuites(context.Background(), project)

	for numOfSuites > 0 {
		suiteRequest = suiteRequest.Offset(offSetCount)
		suiteRequest = suiteRequest.Limit(requestLimit)
		suiteRequest = suiteRequest.Search(suite)

		suites, _, err := suiteRequest.Execute()
		if err != nil {
			return nil, err
		}

		for _, result := range suites.Result.Entities {
			resultID := result.ParentId.Get()
			parentID := parentID.Get()

			isMatchingID := false
			if parentID == nil && resultID == parentID {
				isMatchingID = true
			} else if parentID != nil && resultID != nil {
				if *parentID == *resultID {
					isMatchingID = true
				}
			}

			if isMatchingID && *result.Title == suite {
				return &result, nil
			}
		}

		numOfSuites = *suites.Result.Count
		offSetCount += numOfSuites
	}

	return nil, fmt.Errorf("test suite \"%s\" not found in project %s", suite, project)
}

// CreateTestSuite creates a new Test Suite within a specified Qase Project
func (q *Service) CreateTestSuite(project string, suite upstream.SuiteCreate) (int64, error) {
	logrus.Debugf("Creating test suite \"%s\" in project %s\n", suite.Title, project)
	suiteRequest := q.Client.SuitesAPI.CreateSuite(context.Background(), project)

	suiteRequest = suiteRequest.SuiteCreate(suite)
	id, _, err := suiteRequest.Execute()
	if err != nil {
		return 0, fmt.Errorf("failed to create test suite: \"%s\". Error: %v", suite.Title, err)
	}
	return *id.Result.Id, nil
}

// createTestCase creates a new test in qase
func (q *Service) createTestCase(project string, testCase upstream.TestCaseCreate) error {
	testRequest := q.Client.CasesAPI.CreateCase(context.Background(), project)

	testRequest = testRequest.TestCaseCreate(testCase)
	_, _, err := testRequest.Execute()
	if err != nil {
		return fmt.Errorf("failed to create test case: \"%s\". Error: %v", testCase.Title, err)
	}

	return nil
}

// updateTestCase updates an existing test in qase
func (q *Service) updateTestCase(project string, testCase upstream.TestCaseUpdate, id int32) error {
	testRequest := q.Client.CasesAPI.UpdateCase(context.Background(), project, id)

	testRequest = testRequest.TestCaseUpdate(testCase)
	_, _, err := testRequest.Execute()
	if err != nil {
		return fmt.Errorf("failed to update test case: \"%s\". Error: %v", *testCase.Title, err)
	}
	return nil
}

// createSuitePath creates a series of nested test suites from a / deliniated string
func createSuitePath(client *Service, suiteName, project string) (int64, error) {
	suites := strings.Split(suiteName, "/")
	testSuiteId := int64(0)
	var parentID *int64

	for _, suite := range suites {
		testSuite, err := client.GetTestSuite(project, suite, *upstream.NewNullableInt64(parentID))
		if testSuite != nil {
			testSuiteId = *testSuite.Id
		}

		if err != nil && testSuite != nil {
			logrus.Error("Could not determine test suite:", err)
			return 0, err
		} else if err != nil {
			logrus.Debugf("Error obtaining test suite: %s", err)
			suiteBody := upstream.SuiteCreate{Title: suite}
			if testSuiteId != 0 {
				suiteBody.ParentId = *upstream.NewNullableInt64(&testSuiteId)
			}
			testSuiteId, _ = client.CreateTestSuite(project, suiteBody)
		}

		parentID = &testSuiteId
	}

	return testSuiteId, nil
}

// UploadTests either creates new Test Cases and their associated Suite or updates them if they already exist
func (q *Service) UploadTests(project string, testCases []upstream.TestCaseCreate) error {
	for _, tc := range testCases {
		matchingCases, err := q.getTestCases(project, tc)
		if err == nil && len(matchingCases) == 1 {
			logrus.Info("Updating test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tSuiteId: ", *tc.SuiteId)
			var qaseTest upstream.TestCaseUpdate
			marshaledConfig, err := json.Marshal(tc)
			if err != nil {
				return err
			}

			err = json.Unmarshal(marshaledConfig, &qaseTest)
			if err != nil {
				return err
			}

			err = q.updateTestCase(project, qaseTest, int32(*matchingCases[0].Id))
			if err != nil {
				logrus.Error(err)
				continue
			}
		} else if len(matchingCases) > 1 {
			logrus.Warningf("Skipping %s: Multiple instances of the same test case found in project %s", tc.Title, project)
			continue
		} else {
			logrus.Info("Creating test case:\n\tProject: ", project, "\n\tTitle: ", tc.Title, "\n\tDescription: ", *tc.Description, "\n\tSuiteId: ")
			err = q.createTestCase(project, tc)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// getTestCases retrieves a Test Case by name within a specified Qase Project if it exists
func (q *Service) getTestCases(project string, test upstream.TestCaseCreate) ([]upstream.TestCase, error) {
	logrus.Debugf("Getting test case \"%s\" in project %s\n", test.Title, project)
	testRequest := q.Client.CasesAPI.GetCases(context.Background(), project)

	testRequest = testRequest.Search(test.Title)

	testCases, _, err := testRequest.Execute()
	if err != nil {
		return nil, err
	}

	resultLength := len(testCases.Result.Entities)
	if resultLength == 1 {
		return testCases.Result.Entities, nil
	} else if resultLength > 1 {
		var titleMatchingEntities []upstream.TestCase
		for _, entity := range testCases.Result.Entities {
			if entity.Title == &test.Title {
				titleMatchingEntities = append(titleMatchingEntities, entity)
			}
		}

		return testCases.Result.Entities, nil
	}

	return nil, fmt.Errorf("test case \"%s\" not found in project %s", test.Title, project)
}

func (q *Service) CreateTestRun(testRunName string, projectID string, runDescription string) (*upstream.IdResponse, error) {
	runCreateBody := upstream.RunCreate{
		Title: testRunName,
	}

	if projectID == RancherManagerProjectID {
		runCreateBody = upstream.RunCreate{
			Title: testRunName,
			CustomField: &map[string]string{
				fmt.Sprintf("%d", runSourceID): fmt.Sprintf("%d", recurringRunID),
			},
		}
	}

	if runDescription != "" {
		runCreateBody.SetDescription(runDescription)
	}

	runRequest := q.Client.RunsAPI.CreateRun(context.Background(), projectID)
	runRequest = runRequest.RunCreate(runCreateBody)
	resp, _, err := runRequest.Execute()
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// CompleteTestRun complete the Qase test run
func (q *Service) CompleteTestRun(projectIDEnvVar string, testRunID int32) error {
	runRequest := q.Client.RunsAPI.CompleteRun(context.Background(), projectIDEnvVar, testRunID)
	_, _, err := runRequest.Execute()
	if err != nil {
		return err
	}

	return nil
}

// GetLatestDailyRunWithPrefix returns a number of the most recently started daily Test Runs whose title begins with the
// provided prefix within a specified Qase Project.
func (q *Service) GetLatestDailyRunsWithPrefix(project string, name string, numberOfRuns int32) ([]upstream.Run, error) {
	if numberOfRuns < 1 {
		return []upstream.Run{}, nil
	}

	now := time.Now()
	midnightToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := midnightToday.AddDate(0, 0, -int(numberOfRuns))
	logrus.Debugf("Getting runs named \"%s\" in project %s from %s\n", name, project, from)

	runRequest := q.Client.RunsAPI.GetRuns(context.Background(), project)
	runRequest = runRequest.Search(name)
	runRequest = runRequest.FromStartTime(from.Unix())

	runResponse, _, err := runRequest.Execute()
	if err != nil {
		return nil, err
	}

	runs := runResponse.Result.Entities
	if len(runs) < int(numberOfRuns) {
		return nil, fmt.Errorf("not enough test runs over the expected time period. Found %d, expected %d", len(runs), numberOfRuns)
	}

	return runs[len(runs)-int(numberOfRuns):], nil // We do some trickery with the resulting run slice to avoid returning more runs than needed.
}

// GetFailedTestsForRun returns the failed test titles results for a given Test Run.
// This returns two identically lenghed slices, the first containing the case titles and the second containing qase-api-client.Result.
func (q *Service) GetFailedTestsForRun(project string, runID int32) ([]string, []upstream.Result, error) {
	logrus.Debugf("Getting failed results for run %d in project %s\n", runID, project)

	resultRequest := q.Client.ResultsAPI.GetResults(context.Background(), project)
	resultRequest = resultRequest.Run(fmt.Sprintf("%d", runID))
	resultRequest = resultRequest.Status(failStatus)

	resultResponse, _, err := resultRequest.Execute()
	if err != nil {
		return nil, nil, err
	}
	results := resultResponse.Result.Entities

	caseTitles := make([]string, len(results))
	for i, result := range results {
		caseTitles[i], err = q.getCaseTitle(project, *result.CaseId)
		if err != nil {
			return nil, nil, err
		}
	}

	return caseTitles, results, nil
}

// getCaseTitle returns the title of a Test Case by its id within a specified Qase Project.
func (q *Service) getCaseTitle(project string, caseID int64) (string, error) {
	logrus.Debugf("Getting case titles for test case %d in project %s\n", caseID, project)

	caseRequest := q.Client.CasesAPI.GetCase(context.Background(), project, int32(caseID))
	resp, _, err := caseRequest.Execute()
	if err != nil {
		return "", err
	}

	if resp.Result == nil || resp.Result.Title == nil {
		return "", fmt.Errorf("test case %d has no title in project %s", caseID, project)
	}

	return *resp.Result.Title, nil
}
