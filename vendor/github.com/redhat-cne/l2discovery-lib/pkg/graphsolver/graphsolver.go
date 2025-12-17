package graphsolver

import (
	"strings"

	"github.com/redhat-cne/l2discovery-lib/exports"
	"github.com/sirupsen/logrus"
)

var GlobalConfig = configObject{}

type configObject struct {
	// tag for problem variables
	variableTagToInt map[string][]int
	// problem definition
	problems map[string][][][]int
	// map storing solutions
	solutions map[string]*[][]int
	l2Config  exports.L2Info
}

func (config *configObject) SetL2Config(param exports.L2Info) {
	config.l2Config = param
}
func (config *configObject) GetSolutions() map[string]*[][]int {
	return config.solutions
}
func (config *configObject) InitProblem(name string, problemStatement [][][]int, problemVariablesMapping []int) {
	if GlobalConfig.problems == nil {
		GlobalConfig.problems = make(map[string][][][]int)
	}
	GlobalConfig.problems[name] = problemStatement
	if GlobalConfig.variableTagToInt == nil {
		GlobalConfig.variableTagToInt = make(map[string][]int)
	}
	GlobalConfig.variableTagToInt[name] = problemVariablesMapping
	if GlobalConfig.solutions == nil {
		GlobalConfig.solutions = make(map[string]*[][]int)
	}
	GlobalConfig.solutions[name] = &[][]int{}
}

func (config *configObject) Run(problemName string) {
	// get all the vertices in the graph
	L := GetAllGraphVertices(len(config.l2Config.GetPtpIfList()))

	// Running solver
	PermutationsWithConstraints(config.l2Config, GlobalConfig.problems[problemName], L, 0, len(GlobalConfig.problems[problemName]), len(L), true, GlobalConfig.solutions[problemName])
}

// Prints the all solutions for each scenario, if found
func (config configObject) PrintSolutions(all bool) {
	for index, solutions := range config.solutions {
		if len(*solutions) == 0 {
			logrus.Infof("Solution for %s problem does not exists", index)
			continue
		}
		logrus.Infof("Solutions for %s problem", index)
		for _, solution := range *solutions {
			PrintSolution(config.l2Config, solution)
			logrus.Infof("---")
			if !all {
				break
			}
		}
	}
}

// Prints only the first solution for each scenario, if found
func (config configObject) PrintFirstSolution() {
	for index, solutions := range config.solutions {
		if len(*solutions) == 0 {
			logrus.Infof("Solution for %s problem does not exists", index)
			continue
		}
		logrus.Infof("First solution for %s problem", index)
		PrintSolution(config.l2Config, (*solutions)[0])
		logrus.Infof("---")
	}
}

// Prints the all solutions for each scenario, if found
func (config configObject) PrintAllSolutions() {
	config.PrintSolutions(true)
}

// list of Algorithm functions with zero params
type AlgoFunction0 int

// See applyStep
const (
	// same node
	StepNil AlgoFunction0 = iota
)

// list of Algorithm function with 1 params
type AlgoFunction1 int

// See applyStep
const (
	// same node
	StepIsPTP AlgoFunction1 = iota
	StepIsWPCNic
)

// list of Algorithm function with 2 params
type AlgoFunction2 int

// See applyStep
const (
	StepSameLan2 AlgoFunction2 = iota
	StepSameNic
	StepSameNode
)

// Negation constants for step functions
// Use these as the last element in the step definition array
const (
	Positive = 0 // Normal/positive test (e.g., SameNode returns true if nodes match)
	Negative = 1 // Negated test (e.g., SameNode with Negative returns true if nodes DON'T match)
)

// Helper functions to create step definitions with optional negation
// These make it easier to build problem definitions

// Step0 creates a step with 0 parameters
// negate: use Positive for normal test, Negative for inverted test
func Step0(fn AlgoFunction0, negate int) []int {
	return []int{int(fn), 0, negate}
}

// Step1 creates a step with 1 parameter
// negate: use Positive for normal test, Negative for inverted test
func Step1(fn AlgoFunction1, param1, negate int) []int {
	return []int{int(fn), 1, param1, negate}
}

// Step2 creates a step with 2 parameters
// negate: use Positive for normal test, Negative for inverted test
func Step2(fn AlgoFunction2, param1, param2, negate int) []int {
	return []int{int(fn), 2, param1, param2, negate}
}

// Step3 creates a step with 3 parameters
// negate: use Positive for normal test, Negative for inverted test
func Step3(fn AlgoFunction3, param1, param2, param3, negate int) []int {
	return []int{int(fn), 3, param1, param2, param3, negate}
}

// WPCNICSubsystemID is the subsystem ID for Intel WPC NICs
const WPCNICSubsystemID = "E810-XXV-4T"

// list of Algorithm function with 3 params
type AlgoFunction3 int

// See applyStep
const (
	StepSameLan3 AlgoFunction3 = iota
)

// Signature for algorithm functions with 0 params
type ConfigFunc0 func() bool

// Signature for algorithm functions with 1 params
type ConfigFunc1 func(exports.L2Info, int) bool

// Signature for algorithm functions with 2 params
type ConfigFunc2 func(exports.L2Info, int, int) bool

// Signature for algorithm functions with 3 params
type ConfigFunc3 func(exports.L2Info, int, int, int) bool

type ConfigFunc func(exports.L2Info, []int) bool
type Algorithm struct {
	// number of interfaces to solve
	IfCount int
	// Function to run algo
	TestSolution ConfigFunc
}

// Print a single solution
func PrintSolution(config exports.L2Info, p []int) {
	i := 0
	for _, aIf := range p {
		logrus.Infof("p%d= %s", i, config.GetPtpIfList()[aIf])
		i++
	}
}

func GetAllGraphVertices(count int) (l []int) {
	for i := 0; i < count; i++ {
		l = append(l, i)
	}
	return l
}

// Recursive solver function. Creates a set of permutations and applies contraints at each step to
// reduce the solution graph and speed up execution
func PermutationsWithConstraints(config exports.L2Info, algo [][][]int, l []int, s, e, n int, result bool, solutions *[][]int) {
	if !result || len(l) < e {
		return
	}
	if s == e {
		temp := make([]int, 0)
		temp = append(temp, l...)
		temp = temp[0:e]
		logrus.Tracef("Permutations %v --  %v", temp, result)
		*solutions = append(*solutions, temp)
	} else {
		// Backtracking loop
		for i := s; i < n; i++ {
			l[i], l[s] = l[s], l[i]
			result = applyStep(config, algo[s], l[0:e])
			PermutationsWithConstraints(config, algo, l, s+1, e, n, result, solutions)
			l[i], l[s] = l[s], l[i]
		}
	}
}

// check if an interface is receiving GM
func IsPTP(config exports.L2Info, aInterface *exports.PtpIf) bool {
	for _, aIf := range config.GetPortsGettingPTP() {
		if aInterface.IfClusterIndex == aIf.IfClusterIndex {
			return true
		}
	}
	return false
}

// Checks that an if an interface receives ptp frames
func IsPTPWrapper(config exports.L2Info, if1 int) bool {
	return IsPTP(config, config.GetPtpIfList()[if1])
}

// IsWPCNicFunc is a configurable function to check if an interface is a WPC NIC
// This should be set by the test code since it requires cluster access
var IsWPCNicFunc func(config exports.L2Info, ifIndex int) bool

// IsWPCNicWrapper checks if an interface belongs to a WPC-enabled NIC
// Returns false if IsWPCNicFunc is not set
func IsWPCNicWrapper(config exports.L2Info, if1 int) bool {
	if IsWPCNicFunc == nil {
		logrus.Warn("IsWPCNicFunc not set, returning false")
		return false
	}
	return IsWPCNicFunc(config, if1)
}

// Checks if 2 interfaces are on the same node
func SameNode(if1, if2 *exports.PtpIf) bool {
	return if1.NodeName == if2.NodeName
}

// algo Wrapper for SameNode
func SameNodeWrapper(config exports.L2Info, if1, if2 int) bool {
	return SameNode(config.GetPtpIfList()[if1], config.GetPtpIfList()[if2])
}

// Checks if 3 interfaces are connected to the same LAN
func SameLan3(config exports.L2Info, if1, if2, if3 int, lans *[][]int) bool {
	if SameNode(config.GetPtpIfList()[if1], config.GetPtpIfList()[if2]) ||
		SameNode(config.GetPtpIfList()[if1], config.GetPtpIfList()[if3]) {
		return false
	}
	for _, Lan := range *lans {
		if1Present := false
		if2Present := false
		if3Present := false
		for _, aIf := range Lan {
			if aIf == if1 {
				if1Present = true
			}
			if aIf == if2 {
				if2Present = true
			}
			if aIf == if3 {
				if3Present = true
			}
		}
		if if1Present && if2Present && if3Present {
			return true
		}
	}
	return false
}

// algo wrapper for SameLan3
func SameLan3Wrapper(config exports.L2Info, if1, if2, if3 int) bool {
	return SameLan3(config, if1, if2, if3, config.GetLANs())
}

// Checks if 2 interfaces are connected to the same LAN
func SameLan2(config exports.L2Info, if1, if2 int, lans *[][]int) bool {
	for _, Lan := range *lans {
		if1Present := false
		if2Present := false
		for _, aIf := range Lan {
			if aIf == if1 {
				if1Present = true
			}
			if aIf == if2 {
				if2Present = true
			}
		}
		if if1Present && if2Present {
			return true
		}
	}
	return false
}

// wrapper for SameLan2
func SameLan2Wrapper(config exports.L2Info, if1, if2 int) bool {
	return SameLan2(config, if1, if2, config.GetLANs())
}

// Determines if 2 interfaces (ports) belong to the same NIC
func SameNic(ifaceName1, ifaceName2 *exports.PtpIf) bool {
	if ifaceName1.IfClusterIndex.NodeName != ifaceName2.IfClusterIndex.NodeName {
		return false
	}
	return ifaceName1.IfPci.Device != "" && ifaceName1.IfPci.Device == ifaceName2.IfPci.Device
}

// wrapper for SameNic
func SameNicWrapper(config exports.L2Info, if1, if2 int) bool {
	return SameNic(config.GetPtpIfList()[if1], config.GetPtpIfList()[if2])
}

// IsWpcNic determines if the NIC is intel WPC NIC
func IsWpcNic(ifaceName1 *exports.PtpIf) bool {
	return strings.Contains(ifaceName1.IfPci.Subsystem, WPCNICSubsystemID)
}

// IsWpcNicWrapper is the wrapper for IsWpcNic
func IsWpcNicWrapper(config exports.L2Info, if1 int) bool {
	return IsWpcNic(config.GetPtpIfList()[if1])
}

// wrapper for nil algo function
func NilWrapper() bool {
	return true
}

// Applies a single step (constraint) in the backtracking algorithm
// Step format: [FunctionCode, ParamCount, Param1, Param2, ..., NegationFlag]
// NegationFlag: 0 = Positive (normal), 1 = Negative (inverted result)
// If NegationFlag is omitted, defaults to Positive (0)
func applyStep(config exports.L2Info, step [][]int, combinations []int) bool {
	type paramNum int

	const (
		NoParam paramNum = iota
		OneParam
		TwoParams
		ThreeParams
		FourParams
	)
	// mapping table between :
	// AlgoFunction0, AlgoFunction1, AlgoFunction2, AlgoFunction3 and
	// function wrappers

	var AlgoCode0 [1]ConfigFunc0
	AlgoCode0[StepNil] = NilWrapper

	var AlgoCode1 [2]ConfigFunc1
	AlgoCode1[StepIsPTP] = IsPTPWrapper
	AlgoCode1[StepIsWPCNic] = IsWPCNicWrapper

	var AlgoCode2 [3]ConfigFunc2
	AlgoCode2[StepSameLan2] = SameLan2Wrapper
	AlgoCode2[StepSameNic] = SameNicWrapper
	AlgoCode2[StepSameNode] = SameNodeWrapper

	var AlgoCode3 [1]ConfigFunc3
	AlgoCode3[StepSameLan3] = SameLan3Wrapper
	const negationIdxBase = 2 // base index for negation flag calculation

	result := true
	for _, test := range step {
		var stepResult bool
		// Get negation flag - it's the last element after all params
		// Position depends on param count: NoParam=2, OneParam=3, TwoParams=4, ThreeParams=5
		negationIdx := negationIdxBase + test[1] // index of negation flag
		negate := false
		if len(test) > negationIdx {
			negate = test[negationIdx] == Negative
		}

		switch test[1] {
		case int(NoParam):
			stepResult = AlgoCode0[test[0]]()
		case int(OneParam):
			stepResult = AlgoCode1[test[0]](config, combinations[test[2]])
		case int(TwoParams):
			stepResult = AlgoCode2[test[0]](config, combinations[test[2]], combinations[test[3]])
		case int(ThreeParams):
			stepResult = AlgoCode3[test[0]](config, combinations[test[2]], combinations[test[3]], combinations[test[4]])
		}

		// Apply negation if requested
		if negate {
			stepResult = !stepResult
		}

		result = result && stepResult
	}
	return result
}
