package ptp_reporter

import (
	"encoding/xml"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"

	"github.com/onsi/ginkgo/types"
)

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PTP custom reporter unit tests")
}

/*
  UTs for the ptp custom ginkgo reporter:
    1. When reporter.ReporterConfig.ReportPassed = true
	   This flag allows all By() or whatever output from the ginkgo tc to appear as system-out/system-err.
	   The UTs for this cases should check that the custom ptp report entries have been added as the last
	   lines of the system-out output.
	2. When reporter.ReporterConfig.ReportPassed = false
	   The system-out xml node should appear, containing the custom ptp report entries' lines only.

	Both cases should be covered for passing and failing ginkgo test cases.
*/

var _ = Describe("PTP xml reporter tests", func() {
	var utPtpReporter *PTPJUnitReporter
	var outputFile string

	getPtpReporterForReportPassedTcs := func(reportPassed bool) {
		f, err := os.CreateTemp("", "output")
		Expect(err).ToNot(HaveOccurred())

		f.Close()
		outputFile = f.Name()

		utPtpReporter = NewPTPJUnitReporter(outputFile)

		// Create fake ginkgo test suite. Most of the fields do not need to be initialized here.
		utPtpReporter.SpecSuiteWillBegin(config.GinkgoConfigType{}, &types.SuiteSummary{
			SuiteDescription: "My test suite",
		})

		// Override default reporter config flag.
		utPtpReporter.ReporterConfig.ReportPassed = reportPassed
	}

	AfterEach(func() {
		os.RemoveAll(outputFile)
	})

	type reportEntry struct {
		name   string
		values []interface{}
	}

	mockGinkgoTcDidRun := func(name string, state types.SpecState, existingSysOut string, reportEntries []reportEntry) {
		// Mock the function that "AddReportEntry" will use.
		GetFullGinkgoTcName = func() string { return name }

		for i := range reportEntries {
			AddReportEntry(reportEntries[i].name, reportEntries[i].values...)
		}

		spec := &types.SpecSummary{
			ComponentTexts: []string{"[Top Level]", name},
			CapturedOutput: existingSysOut,
			State:          state,
		}

		utPtpReporter.SpecWillRun(spec)
		utPtpReporter.SpecDidComplete(spec)
	}

	getTsFromXml := func() *reporters.JUnitTestSuite {

		utPtpReporter.SpecSuiteDidEnd(&types.SuiteSummary{})

		fileBytes, err := os.ReadFile(outputFile)
		Expect(err).ToNot(HaveOccurred())

		ts := reporters.JUnitTestSuite{}

		err = xml.Unmarshal(fileBytes, &ts)
		Expect(err).To(BeNil(), "failed to unmarshal junit suite's xml content")

		return &ts
	}

	When("when ts is configure to report passed and all test cases pass", func() {
		BeforeEach(func() {
			// Create a new ptp reporter for each tc.
			getPtpReporterForReportPassedTcs(true)
		})

		It("should add report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should add several report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
				{
					name:   "entry2",
					values: []interface{}{"this is custom text2"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n--\nentry2\nthis is custom text2\n"))
		})

		It("should add report entries for a single test case with previous system-out text", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "previous stdout text\n", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(1))
			Expect(ts.TestCases[0].SystemOut).To(Equal("previous stdout text\nReport Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should work for several test cases", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text 1"},
				},
			})
			mockGinkgoTcDidRun("tc2", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry2",
					values: []interface{}{"this is custom text 2"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(2))
			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text 1\n"))
			Expect(ts.TestCases[1].SystemOut).To(Equal("Report Entries:\nentry2\nthis is custom text 2\n"))
		})
	})

	When("when ts is configure to not report passed and all test cases pass", func() {
		BeforeEach(func() {
			// Create a new ptp reporter for each tc.
			getPtpReporterForReportPassedTcs(false)
		})

		It("should add report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should add several report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
				{
					name:   "entry2",
					values: []interface{}{"this is custom text2"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n--\nentry2\nthis is custom text2\n"))
		})

		// // When thet tc passed but ReportPassed was set to true, the system-out will be left empty by the
		// // junit reporter, so only the report entries should appear.
		It("should add report entries for a single test case with previous system-out text", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "previous stdout text", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(1))
			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should work for several test cases", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text 1"},
				},
			})
			mockGinkgoTcDidRun("tc2", types.SpecStatePassed, "", []reportEntry{
				{
					name:   "entry2",
					values: []interface{}{"this is custom text 2"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(2))
			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text 1\n"))
			Expect(ts.TestCases[1].SystemOut).To(Equal("Report Entries:\nentry2\nthis is custom text 2\n"))
		})
	})

	When("when test cases can fail", func() {
		BeforeEach(func() {
			// Create a new ptp reporter for each tc.
			getPtpReporterForReportPassedTcs(true)
		})

		It("should add report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStateFailed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should add several report entries for a single test case", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStateFailed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
				{
					name:   "entry2",
					values: []interface{}{"this is custom text2"},
				},
			})

			ts := getTsFromXml()

			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text1\n--\nentry2\nthis is custom text2\n"))
		})

		It("should add report entries for a single test case with previous system-out text", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStateFailed, "previous stdout text\n", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text1"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(1))
			Expect(ts.TestCases[0].SystemOut).To(Equal("previous stdout text\nReport Entries:\nentry1\nthis is custom text1\n"))
		})

		It("should work for several test cases", func() {
			mockGinkgoTcDidRun("tc1", types.SpecStateFailed, "", []reportEntry{
				{
					name:   "entry1",
					values: []interface{}{"this is custom text 1"},
				},
			})
			mockGinkgoTcDidRun("tc2", types.SpecStateFailed, "", []reportEntry{
				{
					name:   "entry2",
					values: []interface{}{"this is custom text 2"},
				},
			})

			ts := getTsFromXml()

			Expect(len(ts.TestCases)).To(Equal(2))
			Expect(ts.TestCases[0].SystemOut).To(Equal("Report Entries:\nentry1\nthis is custom text 1\n"))
			Expect(ts.TestCases[1].SystemOut).To(Equal("Report Entries:\nentry2\nthis is custom text 2\n"))
		})
	})
})

// This is a copy/paste of the original JUnitReporter test cases, to make sure
// the ptp reporter does not break any standard functionality.

var _ = Describe("JUnit Reporter regressions for the PTP reporter", func() {
	var (
		outputFile string
		reporter   *PTPJUnitReporter
	)
	testSuiteTime := 12456999 * time.Microsecond
	reportedSuiteTime := 12.456

	readOutputFile := func() reporters.JUnitTestSuite {
		bytes, err := os.ReadFile(outputFile)
		Expect(err).ToNot(HaveOccurred())
		var suite reporters.JUnitTestSuite
		err = xml.Unmarshal(bytes, &suite)
		Expect(err).ToNot(HaveOccurred())
		return suite
	}

	BeforeEach(func() {
		f, err := os.CreateTemp("", "output")
		Expect(err).ToNot(HaveOccurred())
		f.Close()
		outputFile = f.Name()

		reporter = NewPTPJUnitReporter(outputFile)

		reporter.SpecSuiteWillBegin(config.GinkgoConfigType{}, &types.SuiteSummary{
			SuiteDescription:           "My test suite",
			NumberOfSpecsThatWillBeRun: 1,
		})
	})

	AfterEach(func() {
		os.RemoveAll(outputFile)
	})

	Describe("when configured with ReportPassed, and test has passed", func() {
		BeforeEach(func() {
			beforeSuite := &types.SetupSummary{
				State: types.SpecStatePassed,
			}
			reporter.BeforeSuiteDidRun(beforeSuite)

			afterSuite := &types.SetupSummary{
				State: types.SpecStatePassed,
			}
			reporter.AfterSuiteDidRun(afterSuite)

			// Set the ReportPassed config flag, in order to show captured output when tests have passed.
			reporter.ReporterConfig.ReportPassed = true

			spec := &types.SpecSummary{
				ComponentTexts: []string{"[Top Level]", "A", "B", "C"},
				CapturedOutput: "Test scenario...",
				State:          types.SpecStatePassed,
				RunTime:        5 * time.Second,
			}
			reporter.SpecWillRun(spec)
			reporter.SpecDidComplete(spec)

			reporter.SpecSuiteDidEnd(&types.SuiteSummary{
				NumberOfSpecsThatWillBeRun: 1,
				NumberOfFailedSpecs:        0,
				RunTime:                    testSuiteTime,
			})
		})

		It("should record the test as passing, including detailed output", func() {
			output := readOutputFile()
			Expect(output.Name).To(Equal("My test suite"))
			Expect(output.Tests).To(Equal(1))
			Expect(output.Failures).To(Equal(0))
			Expect(output.Time).To(Equal(reportedSuiteTime))
			Expect(output.Errors).To(Equal(0))
			Expect(output.TestCases).To(HaveLen(1))
			Expect(output.TestCases[0].Name).To(Equal("A B C"))
			Expect(output.TestCases[0].ClassName).To(Equal("My test suite"))
			Expect(output.TestCases[0].FailureMessage).To(BeNil())
			Expect(output.TestCases[0].Skipped).To(BeNil())
			Expect(output.TestCases[0].Time).To(Equal(5.0))
			Expect(output.TestCases[0].SystemOut).To(ContainSubstring("Test scenario"))
		})
	})

	Describe("when configured with ReportFile <file path>", func() {
		BeforeEach(func() {
			beforeSuite := &types.SetupSummary{
				State: types.SpecStatePassed,
			}
			reporter.BeforeSuiteDidRun(beforeSuite)

			afterSuite := &types.SetupSummary{
				State: types.SpecStatePassed,
			}
			reporter.AfterSuiteDidRun(afterSuite)

			reporter.SpecSuiteWillBegin(config.GinkgoConfigType{}, &types.SuiteSummary{
				SuiteDescription:           "My test suite",
				NumberOfSpecsThatWillBeRun: 1,
			})

			// Set the ReportFile config flag with a new directory and new file path to be created.
			d := os.TempDir()
			f, err := os.CreateTemp(d, "output")
			Expect(err).ToNot(HaveOccurred())
			f.Close()
			outputFile = f.Name()
			err = os.RemoveAll(outputFile)
			Expect(err).ToNot(HaveOccurred())
			reporter.ReporterConfig.ReportFile = outputFile

			spec := &types.SpecSummary{
				ComponentTexts: []string{"[Top Level]", "A", "B", "C"},
				CapturedOutput: "Test scenario...",
				State:          types.SpecStatePassed,
				RunTime:        5 * time.Second,
			}
			reporter.SpecWillRun(spec)
			reporter.SpecDidComplete(spec)

			reporter.SpecSuiteDidEnd(&types.SuiteSummary{
				NumberOfSpecsThatWillBeRun: 1,
				NumberOfFailedSpecs:        0,
				RunTime:                    testSuiteTime,
			})

		})

		It("should create the report (and parent directories) as specified by ReportFile path", func() {
			output := readOutputFile()
			Expect(output.Name).To(Equal("My test suite"))
			Expect(output.Tests).To(Equal(1))
			Expect(output.Failures).To(Equal(0))
			Expect(output.Time).To(Equal(reportedSuiteTime))
			Expect(output.Errors).To(Equal(0))
			Expect(output.TestCases).To(HaveLen(1))
			Expect(output.TestCases[0].Name).To(Equal("A B C"))
			Expect(output.TestCases[0].ClassName).To(Equal("My test suite"))
			Expect(output.TestCases[0].FailureMessage).To(BeNil())
			Expect(output.TestCases[0].Skipped).To(BeNil())
			Expect(output.TestCases[0].Time).To(Equal(5.0))
		})
	})

	Describe("when the BeforeSuite fails", func() {
		var beforeSuite *types.SetupSummary

		BeforeEach(func() {
			beforeSuite = &types.SetupSummary{
				State:   types.SpecStateFailed,
				RunTime: 3 * time.Second,
				Failure: types.SpecFailure{
					Message: "failed to setup",
				},
			}
			reporter.BeforeSuiteDidRun(beforeSuite)

			reporter.SpecSuiteDidEnd(&types.SuiteSummary{
				NumberOfSpecsThatWillBeRun: 1,
				NumberOfFailedSpecs:        1,
				RunTime:                    testSuiteTime,
			})
		})

		It("should record the test as having failed", func() {
			output := readOutputFile()
			Expect(output.Name).To(Equal("My test suite"))
			Expect(output.Tests).To(Equal(1))
			Expect(output.Failures).To(Equal(1))
			Expect(output.Time).To(Equal(reportedSuiteTime))
			Expect(output.Errors).To(Equal(0))
			Expect(output.TestCases[0].Name).To(Equal("BeforeSuite"))
			Expect(output.TestCases[0].Time).To(Equal(3.0))
			Expect(output.TestCases[0].ClassName).To(Equal("My test suite"))
			Expect(output.TestCases[0].FailureMessage.Type).To(Equal("Failure"))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring("failed to setup"))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(beforeSuite.Failure.ComponentCodeLocation.String()))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(beforeSuite.Failure.Location.String()))
			Expect(output.TestCases[0].Skipped).To(BeNil())
		})
	})

	Describe("when the AfterSuite fails", func() {
		var afterSuite *types.SetupSummary

		BeforeEach(func() {
			afterSuite = &types.SetupSummary{
				State:   types.SpecStateFailed,
				RunTime: 3 * time.Second,
				Failure: types.SpecFailure{
					Message: "failed to setup",
				},
			}
			reporter.AfterSuiteDidRun(afterSuite)

			reporter.SpecSuiteDidEnd(&types.SuiteSummary{
				NumberOfSpecsThatWillBeRun: 1,
				NumberOfFailedSpecs:        1,
				RunTime:                    testSuiteTime,
			})
		})

		It("should record the test as having failed", func() {
			output := readOutputFile()
			Expect(output.Name).To(Equal("My test suite"))
			Expect(output.Tests).To(Equal(1))
			Expect(output.Failures).To(Equal(1))
			Expect(output.Time).To(Equal(reportedSuiteTime))
			Expect(output.Errors).To(Equal(0))
			Expect(output.TestCases[0].Name).To(Equal("AfterSuite"))
			Expect(output.TestCases[0].Time).To(Equal(3.0))
			Expect(output.TestCases[0].ClassName).To(Equal("My test suite"))
			Expect(output.TestCases[0].FailureMessage.Type).To(Equal("Failure"))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring("failed to setup"))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(afterSuite.Failure.ComponentCodeLocation.String()))
			Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(afterSuite.Failure.Location.String()))
			Expect(output.TestCases[0].Skipped).To(BeNil())
		})
	})

	specStateCases := []struct {
		state   types.SpecState
		message string

		// Only for SpecStatePanicked.
		forwardedPanic string
	}{
		{types.SpecStateFailed, "Failure", ""},
		{types.SpecStateTimedOut, "Timeout", ""},
		{types.SpecStatePanicked, "Panic", "artifical panic"},
	}

	for _, specStateCase := range specStateCases {
		specStateCase := specStateCase
		Describe("a failing test", func() {
			var spec *types.SpecSummary
			BeforeEach(func() {
				spec = &types.SpecSummary{
					ComponentTexts: []string{"[Top Level]", "A", "B", "C"},
					State:          specStateCase.state,
					RunTime:        5 * time.Second,
					Failure: types.SpecFailure{
						Message:        "I failed",
						ForwardedPanic: specStateCase.forwardedPanic,
					},
				}
				reporter.SpecWillRun(spec)
				reporter.SpecDidComplete(spec)

				reporter.SpecSuiteDidEnd(&types.SuiteSummary{
					NumberOfSpecsThatWillBeRun: 1,
					NumberOfFailedSpecs:        1,
					RunTime:                    testSuiteTime,
				})
			})

			It("should record test as failing", func() {
				output := readOutputFile()
				Expect(output.Name).To(Equal("My test suite"))
				Expect(output.Tests).To(Equal(1))
				Expect(output.Failures).To(Equal(1))
				Expect(output.Time).To(Equal(reportedSuiteTime))
				Expect(output.Errors).To(Equal(0))
				Expect(output.TestCases[0].Name).To(Equal("A B C"))
				Expect(output.TestCases[0].ClassName).To(Equal("My test suite"))
				Expect(output.TestCases[0].FailureMessage.Type).To(Equal(specStateCase.message))
				Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring("I failed"))
				Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(spec.Failure.ComponentCodeLocation.String()))
				Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring(spec.Failure.Location.String()))
				Expect(output.TestCases[0].Skipped).To(BeNil())
				if specStateCase.state == types.SpecStatePanicked {
					Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring("\nPanic: " + specStateCase.forwardedPanic + "\n"))
					Expect(output.TestCases[0].FailureMessage.Message).To(ContainSubstring("\nFull stack:\n" + spec.Failure.Location.FullStackTrace))
				}
			})
		})
	}

	for _, specStateCase := range []types.SpecState{types.SpecStatePending, types.SpecStateSkipped} {
		specStateCase := specStateCase
		Describe("a skipped test", func() {
			var spec *types.SpecSummary
			BeforeEach(func() {
				spec = &types.SpecSummary{
					ComponentTexts: []string{"[Top Level]", "A", "B", "C"},
					State:          specStateCase,
					RunTime:        5 * time.Second,
					Failure: types.SpecFailure{
						Message: "skipped reason",
					},
				}
				reporter.SpecWillRun(spec)
				reporter.SpecDidComplete(spec)

				reporter.SpecSuiteDidEnd(&types.SuiteSummary{
					NumberOfSpecsThatWillBeRun: 1,
					NumberOfFailedSpecs:        0,
					RunTime:                    testSuiteTime,
				})
			})

			It("should record test as failing", func() {
				output := readOutputFile()
				Expect(output.Tests).To(Equal(1))
				Expect(output.Failures).To(Equal(0))
				Expect(output.Time).To(Equal(reportedSuiteTime))
				Expect(output.Errors).To(Equal(0))
				Expect(output.TestCases[0].Name).To(Equal("A B C"))
				Expect(output.TestCases[0].Skipped.Message).To(ContainSubstring("skipped reason"))
			})
		})
	}
})
