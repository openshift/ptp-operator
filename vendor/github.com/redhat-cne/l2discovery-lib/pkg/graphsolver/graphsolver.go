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

// list of Algorithm function with 1 interface param and 1 literal value param
type AlgoFunction1V int

// See applyStep
const (
	StepClockClassLessThan AlgoFunction1V = iota
	StepPTPDomainEquals
)

// OneIfaceOneValue is the param type identifier for steps with 1 interface param and 1 literal value param.
// This is separate from the standard param count values (0-4) to distinguish mixed-parameter steps.
const OneIfaceOneValue = 10

// OneIfaceTwoValues is the param type identifier for steps with 1 interface param and 2 literal value params.
const OneIfaceTwoValues = 11

// list of Algorithm function with 1 interface param and 2 literal value params
type AlgoFunction1V2 int

// See applyStep
const (
	StepClockClassLessThanInDomain AlgoFunction1V2 = iota
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

// Step1V creates a step with 1 interface parameter and 1 literal value parameter.
// ifParam is an index into the problem variables (resolved to an interface index via permutations).
// valueParam is a literal value passed directly to the function (e.g., domain number or clock class threshold).
// negate: use Positive for normal test, Negative for inverted test
func Step1V(fn AlgoFunction1V, ifParam, valueParam, negate int) []int {
	return []int{int(fn), OneIfaceOneValue, ifParam, valueParam, negate}
}

// Step1V2 creates a step with 1 interface parameter and 2 literal value parameters.
// ifParam is an index into the problem variables (resolved to an interface index via permutations).
// valueParam1 and valueParam2 are literal values passed directly to the function.
// negate: use Positive for normal test, Negative for inverted test
func Step1V2(fn AlgoFunction1V2, ifParam, valueParam1, valueParam2, negate int) []int {
	return []int{int(fn), OneIfaceTwoValues, ifParam, valueParam1, valueParam2, negate}
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

// Signature for algorithm functions with 1 interface param and 1 literal value param
type ConfigFunc1V func(exports.L2Info, int, int) bool

// Signature for algorithm functions with 1 interface param and 2 literal value params
type ConfigFunc1V2 func(exports.L2Info, int, int, int) bool

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
	if ifaceName1.NodeName != ifaceName2.NodeName {
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
func IsWPCNicWrapper(config exports.L2Info, if1 int) bool {
	return IsWpcNic(config.GetPtpIfList()[if1])
}

// ClockClassLessThan checks if any PTP Announce received on the interface
// has a clock class less than the given threshold value.
func ClockClassLessThan(ptpIf *exports.PtpIf, value int) bool {
	for _, announce := range ptpIf.Announces {
		if int(announce.ClockClass) < value {
			return true
		}
	}
	return false
}

// ClockClassLessThanWrapper is the solver wrapper for ClockClassLessThan
func ClockClassLessThanWrapper(config exports.L2Info, ifIdx, value int) bool {
	return ClockClassLessThan(config.GetPtpIfList()[ifIdx], value)
}

// PTPDomainEquals checks if any PTP Announce received on the interface
// has a domain number equal to the given value.
func PTPDomainEquals(ptpIf *exports.PtpIf, value int) bool {
	for _, announce := range ptpIf.Announces {
		if int(announce.DomainNumber) == value {
			return true
		}
	}
	return false
}

// PTPDomainEqualsWrapper is the solver wrapper for PTPDomainEquals
func PTPDomainEqualsWrapper(config exports.L2Info, ifIdx, value int) bool {
	return PTPDomainEquals(config.GetPtpIfList()[ifIdx], value)
}

// ClockClassLessThanInDomain checks if any PTP Announce received on the interface
// has BOTH a domain number equal to the given domain AND a clock class less than the
// given threshold. Both conditions must be satisfied by the same announce message.
func ClockClassLessThanInDomain(ptpIf *exports.PtpIf, clockClassThreshold, domain int) bool {
	for _, announce := range ptpIf.Announces {
		if int(announce.DomainNumber) == domain && int(announce.ClockClass) < clockClassThreshold {
			return true
		}
	}
	return false
}

// ClockClassLessThanInDomainWrapper is the solver wrapper for ClockClassLessThanInDomain
func ClockClassLessThanInDomainWrapper(config exports.L2Info, ifIdx, clockClassThreshold, domain int) bool {
	return ClockClassLessThanInDomain(config.GetPtpIfList()[ifIdx], clockClassThreshold, domain)
}

// wrapper for nil algo function
func NilWrapper() bool {
	return true
}

// Applies a single step (constraint) in the backtracking algorithm
// Step format: [FunctionCode, ParamCount, Param1, Param2, ..., NegationFlag]
// NegationFlag: 0 = Positive (normal), 1 = Negative (inverted result)
// If NegationFlag is omitted, defaults to Positive (0)
//
//nolint:funlen // dispatch table setup makes this long but splitting would hurt readability
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

	var AlgoCode1V [2]ConfigFunc1V
	AlgoCode1V[StepClockClassLessThan] = ClockClassLessThanWrapper
	AlgoCode1V[StepPTPDomainEquals] = PTPDomainEqualsWrapper

	var AlgoCode1V2 [1]ConfigFunc1V2
	AlgoCode1V2[StepClockClassLessThanInDomain] = ClockClassLessThanInDomainWrapper

	const negationIdxBase = 2 // base index for negation flag calculation

	result := true
	for _, test := range step {
		var stepResult bool
		// Get negation flag - it's the last element after all params
		// Position depends on param count: NoParam=2, OneParam=3, TwoParams=4, ThreeParams=5
		// For OneIfaceOneValue (10): negation is at index 4 (2 params after paramType)
		// For OneIfaceTwoValues (11): negation is at index 5 (3 params after paramType)
		negationIdx := negationIdxBase + test[1] // index of negation flag
		switch test[1] {
		case OneIfaceOneValue:
			// For mixed param steps: [fn, paramType, ifParam, valueParam, negate]
			negationIdx = 4
		case OneIfaceTwoValues:
			// For mixed param steps: [fn, paramType, ifParam, valueParam1, valueParam2, negate]
			negationIdx = 5
		}
		negate := false
		if len(test) > negationIdx {
			negate = test[negationIdx] == Negative
		}

		//nolint:gosec // G602: indices are bounded by step definition constants
		switch test[1] {
		case int(NoParam):
			stepResult = AlgoCode0[test[0]]()
		case int(OneParam):
			stepResult = AlgoCode1[test[0]](config, combinations[test[2]])
		case int(TwoParams):
			stepResult = AlgoCode2[test[0]](config, combinations[test[2]], combinations[test[3]])
		case int(ThreeParams):
			stepResult = AlgoCode3[test[0]](config, combinations[test[2]], combinations[test[3]], combinations[test[4]])
		case OneIfaceOneValue:
			// 1 interface param (resolved from permutations) + 1 literal value param
			stepResult = AlgoCode1V[test[0]](config, combinations[test[2]], test[3])
		case OneIfaceTwoValues:
			// 1 interface param (resolved from permutations) + 2 literal value params
			stepResult = AlgoCode1V2[test[0]](config, combinations[test[2]], test[3], test[4])
		}

		// Apply negation if requested
		if negate {
			stepResult = !stepResult
		}

		result = result && stepResult
	}
	return result
}
